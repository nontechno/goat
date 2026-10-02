//go:build unix

package pasture

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ProtocolVersion is checked in the handshake: a client and a server of
// different versions refuse each other (e.g. an old server still running
// after goat was upgraded).
const ProtocolVersion = 3

// Wire format: every message is a frame
//
//	type (1 byte) | length (4 bytes, big endian) | payload (length bytes)
//
// Payloads are JSON, except: input and output, which are pane id (4 bytes,
// big endian) followed by raw bytes; and the acknowledgements, which are
// pane id and a byte count (4 bytes each, big endian). See flow.go.
const (
	msgHello    byte = 1 // C→S Hello, first frame
	msgWelcome  byte = 2 // S→C Welcome (or Response with an error, then close)
	msgRequest  byte = 3 // C→S Request
	msgResponse byte = 4 // S→C Response to a Request (same ID)
	msgInput    byte = 5 // C→S pane id + bytes for the program
	msgOutput   byte = 6 // S→C pane id + bytes the program wrote
	msgExit     byte = 7 // S→C ExitEvent: an attached pane's program ended
	msgAck      byte = 8 // C→S pane id + count: output bytes consumed (flow control)
	msgInputAck byte = 9 // S→C pane id + count: input bytes written to the PTY (flow control)
)

// maxFrame bounds a payload, so a broken peer can't make the other side
// allocate without limit. Snapshots are the largest frames.
const maxFrame = 64 << 20

// maxInputChunk bounds one input frame.
const maxInputChunk = 64 << 10

// Hello is the client's first frame.
type Hello struct {
	Version int    `json:"version"`
	Name    string `json:"name"` // client name, for the log (e.g. "goat")
	PID     int    `json:"pid"`
}

// Welcome accepts a client.
type Welcome struct {
	Version int `json:"version"`
	PID     int `json:"pid"` // server pid
}

// Request operations.
const (
	OpSpawn  = "spawn"  // start a program; the client is attached to it
	OpAttach = "attach" // receive a pane's snapshot and then its output
	OpDetach = "detach" // stop receiving a pane's output
	OpResize = "resize" // set a pane's size
	OpSignal = "signal" // send a signal to a pane's process group
	OpKill   = "kill"   // hang up a pane and forget it
	OpList   = "list"   // describe all panes
	// OpHistory returns a page of a pane's history: up to Count lines just
	// before line Before (-1 = before the screen), newest kept first when
	// the page is cut at its byte limit. Page backwards by passing the
	// returned First as the next Before, until First <= Oldest.
	OpHistory = "history"
)

// Request is a client request. Which fields matter depends on Op.
type Request struct {
	ID      uint32   `json:"id"`
	Op      string   `json:"op"`
	Pane    int      `json:"pane,omitempty"`
	Argv    []string `json:"argv,omitempty"`    // spawn: program and arguments
	Env     []string `json:"env,omitempty"`     // spawn: complete environment (KEY=VALUE)
	Dir     string   `json:"dir,omitempty"`     // spawn: working directory
	Cols    int      `json:"cols,omitempty"`    // spawn, resize
	Rows    int      `json:"rows,omitempty"`    // spawn, resize
	History int      `json:"history,omitempty"` // spawn: history lines kept in memory (0 = 2000, <0 = no history); attach: lines in the snapshot (-1 = as many as fit)
	Before  int64    `json:"before,omitempty"`  // history: page ends before this line (-1 = the end)
	Count   int      `json:"count,omitempty"`   // history: most lines wanted (0 = 1000)
	Signal  int      `json:"signal,omitempty"`  // signal: signal number
}

// Response answers a Request.
type Response struct {
	ID       uint32     `json:"id"`
	Error    string     `json:"error,omitempty"`
	Pane     int        `json:"pane,omitempty"`     // spawn, attach
	PID      int        `json:"pid,omitempty"`      // spawn, attach
	Cols     int        `json:"cols,omitempty"`     // attach: size the snapshot is for
	Rows     int        `json:"rows,omitempty"`     //
	Snapshot []byte     `json:"snapshot,omitempty"` // attach
	Panes    []PaneInfo `json:"panes,omitempty"`    // list

	// History line numbers: every line that scrolled off the screen has a
	// number, from 0, that never changes. Lines before Oldest are gone
	// (discarded by the disk limit or a clear); End is the next number (the
	// screen comes after the history).
	First  int64    `json:"first,omitempty"`  // attach: first history line in the snapshot; history: number of Lines[0]
	Lines  []string `json:"lines,omitempty"`  // history: the lines, oldest first, with colors as escape sequences
	Oldest int64    `json:"oldest,omitempty"` // attach, history
	End    int64    `json:"end,omitempty"`    // attach, history
}

// PaneInfo describes a pane (list).
type PaneInfo struct {
	ID       int      `json:"id"`
	PID      int      `json:"pid"`
	Argv     []string `json:"argv"`
	Dir      string   `json:"dir"`             // where it started
	Cwd      string   `json:"cwd,omitempty"`   // current directory of the foreground process (Linux)
	Command  string   `json:"command"`         // foreground process name (Linux), else argv[0]
	Title    string   `json:"title,omitempty"` // set by the program (OSC 0/2)
	Cols     int      `json:"cols"`
	Rows     int      `json:"rows"`
	Attached int      `json:"attached"` // clients attached
	Dead     bool     `json:"dead"`     // exited while no client was attached
	Status   int      `json:"status"`   // exit status, when dead
	Created  int64    `json:"created"`  // unix seconds

	HistoryOldest    int64 `json:"history_oldest"`     // first history line still available
	HistoryEnd       int64 `json:"history_end"`        // history lines so far
	HistoryDiskBytes int64 `json:"history_disk_bytes"` // history held on disk

	Paused      bool `json:"paused,omitempty"`       // output held back: an attached client is a window behind
	InputQueued int  `json:"input_queued,omitempty"` // input bytes waiting for the program to read them
}

// ExitEvent reports that a pane's program ended. All output it wrote was
// sent before this; the pane no longer exists afterwards.
type ExitEvent struct {
	Pane   int `json:"pane"`
	Status int `json:"status"` // exit code, or 128+signal
}

func writeFrame(w io.Writer, typ byte, payload []byte) error {
	_, err := w.Write(encodeFrame(typ, payload))
	return err
}

func encodeFrame(typ byte, payload []byte) []byte {
	buf := make([]byte, 5+len(payload))
	buf[0] = typ
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[5:], payload)
	return buf
}

func encodeJSON(typ byte, v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // only our own types are marshalled
	}
	return encodeFrame(typ, b)
}

func encodePaneData(typ byte, pane int, data []byte) []byte {
	buf := make([]byte, 9+len(data))
	buf[0] = typ
	binary.BigEndian.PutUint32(buf[1:5], uint32(4+len(data)))
	binary.BigEndian.PutUint32(buf[5:9], uint32(pane))
	copy(buf[9:], data)
	return buf
}

func decodePaneData(payload []byte) (int, []byte, error) {
	if len(payload) < 4 {
		return 0, nil, errors.New("short pane frame")
	}
	return int(binary.BigEndian.Uint32(payload[:4])), payload[4:], nil
}

func readFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxFrame {
		return 0, nil, fmt.Errorf("frame of %d bytes exceeds the %d limit", n, maxFrame)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return hdr[0], payload, nil
}
