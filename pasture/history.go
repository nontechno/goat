//go:build unix

package pasture

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// History on disk.
//
// A pane's history is every line that scrolled off its screen, numbered
// from 0 in order. The newest lines are in the emulator's scrollback, in
// memory (Request.History lines); older ones are moved to disk as they are
// pushed out of memory, so memory stays bounded while history can be long.
//
// On disk, a pane's history is a series of append-only segment files,
// <history dir>/<pane>-<seq>.log, one rendered line (text with its colors
// as escape sequences) per "\n"-terminated record. When the pane's files
// exceed their byte limit, whole segments are deleted, oldest first; the
// lines they held are gone ("oldest" moves forward). The files are deleted
// with the pane, and stale ones (from a server that crashed) at start.
//
// A sparse in-memory index (one offset per indexEvery lines) makes reading
// any page cheap without holding an offset per line.

const (
	indexEvery     = 256
	segmentBytes   = 8 << 20
	writeBufBytes  = 64 << 10
	maxRecordBytes = 1 << 20 // a rendered line longer than this is cut
)

// HistoryConfig configures history on disk (Config.History).
type HistoryConfig struct {
	Dir      string // where the files go ("" = no history on disk)
	MaxBytes int64  // per pane; oldest segments are deleted beyond it
}

type segment struct {
	path  string
	seq   int
	first int64   // number of its first line
	count int64   // lines in it
	size  int64   // bytes in it
	index []int64 // offset of line first+i*indexEvery
}

// history holds one pane's lines on disk. Owned by the server loop.
type history struct {
	dir      string
	pane     int
	maxBytes int64
	log      *slog.Logger

	segs  []*segment
	f     *os.File // the last segment, open for appending
	w     *bufio.Writer
	seq   int
	base  int64 // number of the oldest line still on disk (or the end, if none)
	end   int64 // number of the next line to append
	total int64 // bytes on disk
	off   bool  // disabled (no directory, or after a write error)
}

func newHistory(cfg HistoryConfig, pane int, log *slog.Logger) *history {
	return &history{dir: cfg.Dir, pane: pane, maxBytes: cfg.MaxBytes, log: log, off: cfg.Dir == "" || cfg.MaxBytes <= 0}
}

// add appends a rendered line. Lines are numbered consecutively; a line
// that can't be stored is lost, and the history then starts after it.
func (h *history) add(line string) {
	n := h.end
	h.end++
	if h.off {
		h.base = h.end
		return
	}
	if err := h.write(n, line); err != nil {
		h.log.Error("history on disk disabled after a write error; older lines are dropped",
			"err", err)
		h.removeFiles()
		h.off = true
		h.base = h.end
	}
}

func (h *history) write(n int64, line string) error {
	seg := h.current()
	if seg == nil || seg.size >= h.segmentLimit() {
		if err := h.startSegment(n); err != nil {
			return err
		}
		seg = h.current()
	}
	if len(line) > maxRecordBytes {
		line = line[:maxRecordBytes]
	}
	line = strings.NewReplacer("\n", " ", "\r", " ").Replace(line)
	if (n-seg.first)%indexEvery == 0 {
		seg.index = append(seg.index, seg.size)
	}
	k, err := h.w.WriteString(line)
	if err == nil {
		err = h.w.WriteByte('\n')
	}
	if err != nil {
		return err
	}
	seg.size += int64(k + 1)
	seg.count++
	h.total += int64(k + 1)
	h.trim()
	return nil
}

// segmentLimit is the size at which a new segment starts: small enough
// that the limit is kept to within about a quarter (one segment).
func (h *history) segmentLimit() int64 {
	return min(segmentBytes, max(h.maxBytes/4, 64<<10))
}

func (h *history) current() *segment {
	if len(h.segs) == 0 {
		return nil
	}
	return h.segs[len(h.segs)-1]
}

func (h *history) startSegment(first int64) error {
	if err := h.closeWriter(); err != nil {
		return err
	}
	h.seq++
	path := filepath.Join(h.dir, fmt.Sprintf("%d-%d.log", h.pane, h.seq))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if len(h.segs) == 0 {
		h.base = first
	}
	h.segs = append(h.segs, &segment{path: path, seq: h.seq, first: first})
	h.f, h.w = f, bufio.NewWriterSize(f, writeBufBytes)
	h.log.Debug("history segment started", "file", path, "first_line", first)
	return nil
}

// trim deletes the oldest segments while over the byte limit (the one being
// written is always kept).
func (h *history) trim() {
	for h.total > h.maxBytes && len(h.segs) > 1 {
		s := h.segs[0]
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			h.log.Warn("cannot delete history segment", "file", s.path, "err", err)
		}
		h.total -= s.size
		h.segs = h.segs[1:]
		h.base = h.segs[0].first
		h.log.Debug("history segment dropped (over the limit)", "file", s.path,
			"lines", s.count, "oldest_line", h.base)
	}
}

func (h *history) closeWriter() error {
	if h.f == nil {
		return nil
	}
	err := h.w.Flush()
	if cerr := h.f.Close(); err == nil {
		err = cerr
	}
	h.f, h.w = nil, nil
	return err
}

// discard drops everything before line n (the scrollback was cleared, e.g.
// by "clear"): the history then starts at n.
func (h *history) discard(n int64) {
	if len(h.segs) > 0 {
		h.log.Debug("history cleared", "lines", h.end-h.base)
	}
	h.removeFiles()
	h.base, h.end = n, n
}

func (h *history) removeFiles() {
	_ = h.closeWriter()
	for _, s := range h.segs {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			h.log.Warn("cannot delete history segment", "file", s.path, "err", err)
		}
	}
	h.segs, h.total = nil, 0
}

// close deletes the pane's files.
func (h *history) close() {
	if len(h.segs) > 0 {
		h.log.Debug("history files deleted", "segments", len(h.segs), "bytes", h.total)
	}
	h.removeFiles()
	h.base = h.end
}

// read returns lines [from, to) from disk (to <= h.end, from >= h.base),
// stopping early once maxBytes would be exceeded (at least one line is
// returned if any is in range).
func (h *history) read(from, to int64, maxBytes int) ([]string, error) {
	if from < h.base {
		from = h.base
	}
	if to > h.end {
		to = h.end
	}
	if from >= to {
		return nil, nil
	}
	if h.w != nil {
		if err := h.w.Flush(); err != nil {
			return nil, err
		}
	}
	var out []string
	used := 0
	for _, s := range h.segs {
		if from >= s.first+s.count || from >= to {
			continue
		}
		if to <= s.first {
			break
		}
		lines, done, err := s.read(from, min(to, s.first+s.count), maxBytes-used, len(out) == 0)
		if err != nil {
			return out, err
		}
		for _, l := range lines {
			used += len(l)
		}
		out = append(out, lines...)
		from += int64(len(lines))
		if !done {
			break // byte budget reached
		}
	}
	return out, nil
}

// read returns lines [from, to) of the segment; done is false if it
// stopped at the byte budget.
func (s *segment) read(from, to int64, budget int, mustOne bool) (lines []string, done bool, err error) {
	f, err := os.Open(s.path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	k := (from - s.first) / indexEvery
	if _, err := f.Seek(s.index[k], io.SeekStart); err != nil {
		return nil, false, err
	}
	r := bufio.NewReaderSize(f, 64<<10)
	n := s.first + k*indexEvery
	used := 0
	for n < to {
		b, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// A long record: collect it whole.
			var buf bytes.Buffer
			buf.Write(b)
			for errors.Is(err, bufio.ErrBufferFull) {
				b, err = r.ReadSlice('\n')
				buf.Write(b)
			}
			b = buf.Bytes()
		}
		if err != nil {
			return lines, false, fmt.Errorf("history %s line %d: %w", s.path, n, err)
		}
		if n >= from {
			line := string(b[:len(b)-1])
			if used+len(line) > budget && !(mustOne && len(lines) == 0) {
				return lines, false, nil
			}
			used += len(line)
			lines = append(lines, line)
		}
		n++
	}
	return lines, true, nil
}

// prepareHistoryDir creates the history directory (0700, ours) and removes
// files left there by a server that is gone (we hold the socket's lock).
func prepareHistoryDir(dir string, log *slog.Logger) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := checkPrivateDir(dir); err != nil {
		return err
	}
	stale, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	for _, p := range stale {
		_ = os.Remove(p)
	}
	if len(stale) > 0 {
		log.Info("removed stale history files", "dir", dir, "files", len(stale))
	}
	return nil
}
