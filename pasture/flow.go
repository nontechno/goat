//go:build unix

package pasture

import (
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Flow control.
//
// Output: the server counts, per attached client and pane, the bytes it
// sent and the client hasn't acknowledged yet (msgAck, sent by Pane.Read as
// it hands output to the caller). While any attached client is
// outputWindow or more behind on a pane, the server stops reading that
// pane's PTY: the program blocks on write, as with a slow local terminal.
// Nothing is lost, and a client's buffer stays within about the window.
// Panes nobody is attached to are always read.
//
// Input: the client keeps at most inputWindow bytes of a pane's input
// unacknowledged; the server acknowledges input (msgInputAck) once it is
// written to the PTY, or dropped. Pane.Write waits for room, so input the
// program doesn't read backs up in the caller, not in the server. A client
// that sends beyond the window is disconnected. The server's own replies to
// terminal queries (sent while nobody is attached) are capped separately.

const (
	outputWindow  = 1 << 20  // unacknowledged output per client and pane
	inputWindow   = 1 << 20  // unacknowledged input per client and pane
	inputSlack    = 64 << 10 // tolerance beyond inputWindow
	repliesMax    = 64 << 10 // queued emulator replies per pane
	clientAckStep = 64 << 10 // the client acknowledges output at least this often
	readAhead     = 4        // PTY reads (of up to 32 KiB) not yet handled by the loop
)

// attachment is a client's flow-control state for one pane.
type attachment struct {
	unacked  int64        // output sent, not yet acknowledged (loop-owned)
	inQueued atomic.Int64 // input queued for the PTY (loop adds, drain subtracts)
}

// encodeCount is the payload of msgAck / msgInputAck: pane id + byte count.
func encodeCount(typ byte, pane, n int) []byte {
	var b [8]byte
	binary.BigEndian.PutUint32(b[:4], uint32(pane))
	binary.BigEndian.PutUint32(b[4:], uint32(n))
	return encodeFrame(typ, b[:])
}

func decodeCount(payload []byte) (pane, n int, err error) {
	if len(payload) != 8 {
		return 0, 0, errors.New("bad acknowledgement frame")
	}
	return int(binary.BigEndian.Uint32(payload[:4])), int(binary.BigEndian.Uint32(payload[4:])), nil
}

// gate pauses a pane's PTY reader.
type gate struct {
	mu     sync.Mutex
	cond   *sync.Cond
	paused bool
	closed bool
	flag   atomic.Bool // paused, readable without the lock
}

func newGate() *gate {
	g := &gate{}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// set pauses or resumes; reports a change.
func (g *gate) set(paused bool) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paused == paused || g.closed {
		return false
	}
	g.paused = paused
	g.flag.Store(paused)
	g.cond.Broadcast()
	return true
}

func (g *gate) close() {
	g.mu.Lock()
	g.closed, g.paused = true, false
	g.flag.Store(false)
	g.cond.Broadcast()
	g.mu.Unlock()
}

// wait blocks while paused; false once closed.
func (g *gate) wait() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for g.paused && !g.closed {
		g.cond.Wait()
	}
	return !g.closed
}

func (g *gate) isPaused() bool { return g.flag.Load() }

// updateFlow pauses p's reader while an attached client is a full window
// behind, and resumes it otherwise.
func (s *Server) updateFlow(p *pane) {
	behind := false
	for c := range p.clients {
		if a := c.attached[p]; a != nil && a.unacked >= outputWindow {
			behind = true
			break
		}
	}
	if !p.gate.set(behind) {
		return
	}
	if behind {
		p.pausedAt = time.Now()
		p.pauses++
		s.log.Debug("output paused: a client is behind", "pane", p.id)
	} else {
		s.log.Debug("output resumed", "pane", p.id, "paused_for", time.Since(p.pausedAt).Round(time.Millisecond))
	}
}

// inChunk is input waiting for the PTY: from a client (owner set), or a
// reply of the server's own emulator.
type inChunk struct {
	data  []byte
	owner *attachment
	c     *client
}

// inputQueue holds input for a PTY, written by its own goroutine so that a
// program that doesn't read its input never blocks the server. It is
// bounded by flow control (client input) and repliesMax (replies).
type inputQueue struct {
	pane    int
	mu      sync.Mutex
	cond    *sync.Cond
	chunks  []inChunk
	replies int
	closed  bool
}

func newInputQueue(pane int) *inputQueue {
	q := &inputQueue{pane: pane}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// add queues client input; it is acknowledged once written (or dropped).
func (q *inputQueue) add(c *client, owner *attachment, data []byte) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		q.acked(inChunk{data: data, owner: owner, c: c})
		return
	}
	q.chunks = append(q.chunks, inChunk{data: data, owner: owner, c: c})
	q.cond.Signal()
}

// reply queues an emulator reply; false if dropped (too many waiting).
func (q *inputQueue) reply(data []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || q.replies+len(data) > repliesMax {
		return false
	}
	q.replies += len(data)
	q.chunks = append(q.chunks, inChunk{data: append([]byte(nil), data...)})
	q.cond.Signal()
	return true
}

// queued is the input waiting (for logs and tests).
func (q *inputQueue) queued() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, ch := range q.chunks {
		n += len(ch.data)
	}
	return n
}

// acked settles a chunk's flow control: the client may send more.
func (q *inputQueue) acked(ch inChunk) {
	if ch.owner == nil {
		return
	}
	ch.owner.inQueued.Add(-int64(len(ch.data)))
	ch.c.send(encodeCount(msgInputAck, q.pane, len(ch.data)))
}

// Close drops what is queued (acknowledging it) and stops the writer.
func (q *inputQueue) Close() {
	q.mu.Lock()
	chunks := q.chunks
	q.chunks, q.closed = nil, true
	q.cond.Broadcast()
	q.mu.Unlock()
	for _, ch := range chunks {
		q.acked(ch)
	}
}

// drainTo writes queued input to dst until the queue is closed. If a write
// fails, the queue is closed, so later input is acknowledged and dropped
// instead of piling up.
func (q *inputQueue) drainTo(dst io.Writer, log *slog.Logger) {
	for {
		q.mu.Lock()
		for len(q.chunks) == 0 && !q.closed {
			q.cond.Wait()
		}
		if q.closed {
			q.mu.Unlock()
			return
		}
		ch := q.chunks[0]
		q.chunks[0] = inChunk{}
		q.chunks = q.chunks[1:]
		q.mu.Unlock()

		_, err := dst.Write(ch.data)
		q.mu.Lock()
		if ch.owner == nil {
			q.replies -= len(ch.data)
		}
		q.mu.Unlock()
		q.acked(ch)
		if err != nil {
			log.Warn("writing input to the program failed; further input is dropped", "err", err)
			q.Close()
			return
		}
	}
}
