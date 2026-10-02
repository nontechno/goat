//go:build unix

package pasture

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
)

// ErrClosed is returned once the connection to the server is gone.
var ErrClosed = errors.New("pasture: connection closed")

// ErrOverflow ends a connection whose server sent far more output than flow
// control allows (a broken or incompatible server).
var ErrOverflow = errors.New("pasture: server ignored flow control (output overflow)")

// Client is a connection to a server. It is safe for concurrent use.
type Client struct {
	conn net.Conn
	wmu  sync.Mutex // serializes frame writes

	mu      sync.Mutex
	nextID  uint32
	pending map[uint32]*call
	panes   map[int]*Pane
	err     error // why the connection ended
	done    chan struct{}
}

type call struct {
	ch chan Response
	// onOK runs in the reader goroutine, under c.mu, when the request
	// succeeded and before any later frame is read, so a new pane is
	// registered before its first output arrives.
	onOK func(Response)
}

// Dial connects to the server at sock and performs the handshake. name
// identifies this client in the server's log.
func Dial(sock, name string) (*Client, error) {
	conn, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		return nil, err
	}
	hello := mustJSON(Hello{Version: ProtocolVersion, Name: name, PID: os.Getpid()})
	if err := writeFrame(conn, msgHello, hello); err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	typ, payload, err := readFrame(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("pasture handshake: %w", err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	switch typ {
	case msgWelcome:
	case msgResponse:
		var r Response
		_ = json.Unmarshal(payload, &r)
		conn.Close()
		return nil, fmt.Errorf("pasture: %s", r.Error)
	default:
		conn.Close()
		return nil, fmt.Errorf("pasture handshake: unexpected frame type %d", typ)
	}
	c := &Client{conn: conn, pending: map[uint32]*call{}, panes: map[int]*Pane{}, done: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

// Close disconnects. The server keeps every pane running; attach to them
// again later with a new client.
func (c *Client) Close() error {
	err := c.conn.Close()
	<-c.done
	return err
}

// Done is closed when the connection has ended; Err then says why.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is why the connection ended (nil while it is up).
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func (c *Client) readLoop() {
	var err error
	defer func() {
		if err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
			err = ErrClosed
		}
		c.mu.Lock()
		c.err = err
		pending, panes := c.pending, c.panes
		c.pending, c.panes = map[uint32]*call{}, map[int]*Pane{}
		c.mu.Unlock()
		for _, cl := range pending {
			close(cl.ch)
		}
		for _, p := range panes {
			p.finish(-1, err)
		}
		close(c.done)
	}()
	for {
		var typ byte
		var payload []byte
		if typ, payload, err = readFrame(c.conn); err != nil {
			return
		}
		switch typ {
		case msgResponse:
			var r Response
			if err = json.Unmarshal(payload, &r); err != nil {
				return
			}
			c.mu.Lock()
			cl := c.pending[r.ID]
			delete(c.pending, r.ID)
			if cl != nil && cl.onOK != nil && r.Error == "" {
				cl.onOK(r)
			}
			c.mu.Unlock()
			if cl != nil {
				cl.ch <- r
			}
		case msgOutput:
			var id int
			var data []byte
			if id, data, err = decodePaneData(payload); err != nil {
				return
			}
			c.mu.Lock()
			p := c.panes[id]
			c.mu.Unlock()
			if p != nil && !p.push(data) {
				err = ErrOverflow
				return
			}
		case msgInputAck:
			var id, n int
			if id, n, err = decodeCount(payload); err != nil {
				return
			}
			c.mu.Lock()
			p := c.panes[id]
			c.mu.Unlock()
			if p != nil {
				p.inputAcked(n)
			}
		case msgExit:
			var ev ExitEvent
			if err = json.Unmarshal(payload, &ev); err != nil {
				return
			}
			c.mu.Lock()
			p := c.panes[ev.Pane]
			delete(c.panes, ev.Pane)
			c.mu.Unlock()
			if p != nil {
				p.finish(ev.Status, nil)
			}
		default:
			err = fmt.Errorf("pasture: unexpected frame type %d", typ)
			return
		}
	}
}

func (c *Client) write(typ byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return writeFrame(c.conn, typ, payload)
}

// do sends a request and waits for its response.
func (c *Client) do(r Request, onOK func(Response)) (Response, error) {
	cl := &call{ch: make(chan Response, 1), onOK: onOK}
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return Response{}, c.err
	}
	c.nextID++
	r.ID = c.nextID
	c.pending[r.ID] = cl
	c.mu.Unlock()
	if err := c.write(msgRequest, mustJSON(r)); err != nil {
		c.mu.Lock()
		delete(c.pending, r.ID)
		c.mu.Unlock()
		return Response{}, err
	}
	resp, ok := <-cl.ch
	if !ok {
		return Response{}, c.Err()
	}
	if resp.Error != "" {
		return resp, fmt.Errorf("pasture: %s", resp.Error)
	}
	return resp, nil
}

// SpawnOptions describes a program to start.
type SpawnOptions struct {
	Argv       []string // program and arguments (argv[0] is looked up in PATH)
	Env        []string // the complete environment; include TERM
	Dir        string   // absolute working directory ("" = /)
	Cols, Rows int
	History    int // history lines the server keeps in memory (0 = 2000, <0 = no history); older lines go to disk when the server has history on disk
}

// Spawn starts a program on the server. The client is attached to it:
// read its output from the returned Pane.
func (c *Client) Spawn(o SpawnOptions) (*Pane, error) {
	var p *Pane
	_, err := c.do(Request{Op: OpSpawn, Argv: o.Argv, Env: o.Env, Dir: o.Dir,
		Cols: o.Cols, Rows: o.Rows, History: o.History}, func(r Response) {
		p = newPane(c, r)
		p.Cols, p.Rows = o.Cols, o.Rows
		c.panes[p.ID] = p
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Attach attaches to a running pane. The returned Pane's Snapshot, fed to
// a new terminal emulator of Cols×Rows, recreates the screen; output
// follows through Read. history is how many scrollback lines to include
// (-1 = as many as fit, about 8 MB). A pane that exited while nobody was
// attached returns its last screen and then ends (Wait gives its status).
// Older history than the snapshot holds is available through History.
func (c *Client) Attach(id, history int) (*Pane, error) {
	var p *Pane
	_, err := c.do(Request{Op: OpAttach, Pane: id, History: history}, func(r Response) {
		p = newPane(c, r)
		p.Cols, p.Rows, p.Snapshot = r.Cols, r.Rows, r.Snapshot
		p.HistoryFirst, p.HistoryOldest = r.First, r.Oldest
		c.panes[p.ID] = p
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// HistoryPage is one page of a pane's history.
type HistoryPage struct {
	First  int64    // number of Lines[0]
	Lines  []string // oldest first; text with colors as escape sequences, no newlines
	Oldest int64    // the oldest line the server still has
	End    int64    // number of the next line to scroll off (the screen comes after)
}

// HistoryEnd, as History's before, means "up to the newest line".
const HistoryEnd int64 = -1

// More reports whether there are older lines than this page.
func (h HistoryPage) More() bool { return h.First > h.Oldest }

// History returns up to count history lines just before line `before`
// (HistoryEnd = the newest). Line numbers never change, so paging is stable
// while the program keeps writing: to go back further, call again with
// before = page.First while page.More(). A page is also limited to about
// 4 MB, and keeps its newest lines when cut.
func (c *Client) History(pane int, before int64, count int) (HistoryPage, error) {
	r, err := c.do(Request{Op: OpHistory, Pane: pane, Before: before, Count: count}, nil)
	if err != nil {
		return HistoryPage{}, err
	}
	return HistoryPage{First: r.First, Lines: r.Lines, Oldest: r.Oldest, End: r.End}, nil
}

// List describes all panes on the server.
func (c *Client) List() ([]PaneInfo, error) {
	r, err := c.do(Request{Op: OpList}, nil)
	return r.Panes, err
}

// Kill hangs up a pane (any pane, attached or not).
func (c *Client) Kill(id int) error {
	_, err := c.do(Request{Op: OpKill, Pane: id}, nil)
	return err
}

// Pane is an attached pane. It reads like the master side of a PTY:
// Read returns the program's output, Write sends it input.
type Pane struct {
	ID       int
	PID      int
	Cols     int    // size at spawn / attach
	Rows     int    //
	Snapshot []byte // attach only: recreates the screen (see Attach)
	// Attach only: number of the first history line in Snapshot, and of the
	// oldest one the server still has. Fetch the ones in between with
	// Client.History(ID, HistoryFirst, n).
	HistoryFirst  int64
	HistoryOldest int64

	c        *Client
	mu       sync.Mutex
	cond     *sync.Cond // output arrived or the pane ended
	wcond    *sync.Cond // input acknowledged or the pane ended
	buf      []byte
	consumed int   // output handed to Read, not yet acknowledged
	inFlight int   // input sent, not yet acknowledged
	done     bool  // no more output
	code     int   // exit status
	err      error // nil = the program exited normally
	wait     chan struct{}
}

func newPane(c *Client, r Response) *Pane {
	p := &Pane{ID: r.Pane, PID: r.PID, c: c, wait: make(chan struct{})}
	p.cond = sync.NewCond(&p.mu)
	p.wcond = sync.NewCond(&p.mu)
	return p
}

// push buffers output; false if the server sent far beyond the window.
func (p *Pane) push(data []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buf = append(p.buf, data...)
	p.cond.Signal()
	return len(p.buf) <= 4*outputWindow
}

func (p *Pane) inputAcked(n int) {
	p.mu.Lock()
	p.inFlight = max(p.inFlight-n, 0)
	p.wcond.Broadcast()
	p.mu.Unlock()
}

func (p *Pane) finish(code int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return
	}
	p.done, p.code, p.err = true, code, err
	p.cond.Broadcast()
	p.wcond.Broadcast()
	close(p.wait)
}

// Read returns output. After the program exits and its output is read, it
// returns io.EOF; if the connection is lost (or the pane is detached), the
// connection error or ErrClosed.
//
// Reading is what lets the program write more: the server holds a pane's
// output back once about 1 MiB of it is unread (see flow.go), so read
// continuously while attached.
func (p *Pane) Read(b []byte) (int, error) {
	p.mu.Lock()
	for len(p.buf) == 0 && !p.done {
		p.cond.Wait()
	}
	if len(p.buf) == 0 {
		err := p.err
		p.mu.Unlock()
		if err != nil {
			return 0, err
		}
		return 0, io.EOF
	}
	n := copy(b, p.buf)
	p.buf = p.buf[n:]
	if len(p.buf) == 0 {
		p.buf = nil
	}
	// Acknowledge in steps, and whenever the buffer is drained.
	p.consumed += n
	ack := 0
	if p.consumed >= clientAckStep || len(p.buf) == 0 {
		ack, p.consumed = p.consumed, 0
	}
	done := p.done
	p.mu.Unlock()
	if ack > 0 && !done {
		_ = p.c.write(msgAck, encodeCount(msgAck, p.ID, ack)[5:])
	}
	return n, nil
}

// Write sends input to the program. It waits while about 1 MiB of this
// pane's input is not yet written to the program (one that doesn't read its
// input makes Write wait, as a full PTY would). It fails once the pane has
// ended or the connection is lost.
func (p *Pane) Write(b []byte) (int, error) {
	n := 0
	for len(b) > 0 {
		chunk := b[:min(len(b), maxInputChunk)]
		p.mu.Lock()
		for !p.done && p.inFlight > 0 && p.inFlight+len(chunk) > inputWindow {
			p.wcond.Wait()
		}
		if p.done {
			err := p.err
			p.mu.Unlock()
			if err == nil {
				err = io.ErrClosedPipe
			}
			return n, err
		}
		p.inFlight += len(chunk)
		p.mu.Unlock()
		if err := p.c.write(msgInput, encodePaneData(msgInput, p.ID, chunk)[5:]); err != nil {
			return n, err
		}
		n += len(chunk)
		b = b[len(chunk):]
	}
	return n, nil
}

// Resize sets the pane's size; the program gets SIGWINCH.
func (p *Pane) Resize(cols, rows int) error {
	_, err := p.c.do(Request{Op: OpResize, Pane: p.ID, Cols: cols, Rows: rows}, nil)
	return err
}

// Signal sends sig to the pane's foreground process group.
func (p *Pane) Signal(sig syscall.Signal) error {
	_, err := p.c.do(Request{Op: OpSignal, Pane: p.ID, Signal: int(sig)}, nil)
	return err
}

// Kill hangs up the program (SIGHUP to its process group; SIGKILL if it
// is still there after a few seconds). Wait reports when it has ended.
func (p *Pane) Kill() error { return p.c.Kill(p.ID) }

// Detach stops receiving the pane's output; the program keeps running on
// the server. Read then returns ErrClosed.
func (p *Pane) Detach() error {
	_, err := p.c.do(Request{Op: OpDetach, Pane: p.ID}, func(Response) { delete(p.c.panes, p.ID) })
	if err == nil {
		p.finish(-1, ErrClosed)
	}
	return err
}

// Close is Detach.
func (p *Pane) Close() error { return p.Detach() }

// Wait blocks until the program exits (or the pane is detached or the
// connection lost) and returns its exit status (128+n for signal n).
func (p *Pane) Wait() (int, error) {
	<-p.wait
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.code, p.err
}

// Exited is closed when Wait would return.
func (p *Pane) Exited() <-chan struct{} { return p.wait }
