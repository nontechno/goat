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
			if p != nil {
				p.push(data)
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
	History    int // scrollback lines the server keeps (0 = 5000, <0 = none)
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
// (-1 = all). A pane that exited while nobody was attached returns its
// last screen and then ends (Wait gives its status).
func (c *Client) Attach(id, history int) (*Pane, error) {
	var p *Pane
	_, err := c.do(Request{Op: OpAttach, Pane: id, History: history}, func(r Response) {
		p = newPane(c, r)
		p.Cols, p.Rows, p.Snapshot = r.Cols, r.Rows, r.Snapshot
		c.panes[p.ID] = p
	})
	if err != nil {
		return nil, err
	}
	return p, nil
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

	c    *Client
	mu   sync.Mutex
	cond *sync.Cond
	buf  []byte
	done bool  // no more output
	code int   // exit status
	err  error // nil = the program exited normally
	wait chan struct{}
}

func newPane(c *Client, r Response) *Pane {
	p := &Pane{ID: r.Pane, PID: r.PID, c: c, wait: make(chan struct{})}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *Pane) push(data []byte) {
	p.mu.Lock()
	p.buf = append(p.buf, data...)
	p.cond.Signal()
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
	close(p.wait)
}

// Read returns output. After the program exits and its output is read, it
// returns io.EOF; if the connection is lost (or the pane is detached), the
// connection error or ErrClosed.
func (p *Pane) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.buf) == 0 && !p.done {
		p.cond.Wait()
	}
	if len(p.buf) > 0 {
		n := copy(b, p.buf)
		p.buf = p.buf[n:]
		if len(p.buf) == 0 {
			p.buf = nil
		}
		return n, nil
	}
	if p.err != nil {
		return 0, p.err
	}
	return 0, io.EOF
}

// Write sends input to the program.
func (p *Pane) Write(b []byte) (int, error) {
	n := 0
	for len(b) > 0 {
		chunk := b[:min(len(b), maxInputChunk)]
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
