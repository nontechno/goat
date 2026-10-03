//go:build unix

package pasture

import (
	"log/slog"
	"net"
	"sync"
	"time"
)

// client is the server's side of one connection. The loop owns its state;
// the writer goroutine owns the socket's write side, fed through a queue so
// that a slow client never blocks the server.
type client struct {
	id       int
	conn     net.Conn
	log      *slog.Logger
	since    time.Time
	name     string
	pid      int
	welcomed bool
	closed   bool
	attached map[*pane]*attachment

	mu      sync.Mutex
	cond    *sync.Cond
	queue   [][]byte
	queued  int
	stopped bool // no more writes accepted
	flushed chan struct{}
}

func (s *Server) newClient(conn net.Conn) {
	s.nextConn++
	c := &client{
		id: s.nextConn, conn: conn, since: time.Now(),
		attached: map[*pane]*attachment{}, flushed: make(chan struct{}),
	}
	c.cond = sync.NewCond(&c.mu)
	c.log = s.log.With("client", c.id)
	s.clients[c] = true
	c.log.Debug("connection accepted")
	go c.writer()
	go c.reader(s)
}

// reader decodes frames and hands them to the loop.
func (c *client) reader(s *Server) {
	defer func() {
		select {
		case s.gone <- c:
		case <-s.done:
		}
	}()
	// The hello must come promptly.
	_ = c.conn.SetReadDeadline(time.Now().Add(helloTimeout))
	first := true
	for {
		typ, payload, err := readFrame(c.conn)
		if err != nil {
			c.log.Debug("read ended", "err", err)
			return
		}
		if first {
			_ = c.conn.SetReadDeadline(time.Time{})
			first = false
		}
		select {
		case s.clientMsgs <- clientMsg{c: c, typ: typ, payload: payload}:
		case <-s.done:
			return
		}
	}
}

func (c *client) writer() {
	defer close(c.flushed)
	defer c.conn.Close()
	for {
		c.mu.Lock()
		for len(c.queue) == 0 && !c.stopped {
			c.cond.Wait()
		}
		q := c.queue
		c.queue, c.queued = nil, 0
		stopped := c.stopped
		c.mu.Unlock()
		for _, frame := range q {
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if _, err := c.conn.Write(frame); err != nil {
				c.log.Warn("write failed; disconnecting", "err", err)
				c.mu.Lock()
				c.stopped, c.queue = true, nil
				c.mu.Unlock()
				return
			}
		}
		if stopped && len(q) == 0 {
			return
		}
	}
}

// send queues a frame. A client that falls maxBacklog behind is cut off
// (its reader then reports it gone).
func (c *client) send(frame []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	c.queue = append(c.queue, frame)
	c.queued += len(frame)
	if c.queued > maxBacklog && len(c.queue) > 1 { // one frame (a large snapshot) always fits
		c.log.Error("client too slow: output backlog over limit; disconnecting", "backlog_bytes", c.queued)
		c.queue, c.stopped = nil, true
		_ = c.conn.Close()
	}
	c.cond.Signal()
}

// closeAfterFlush closes the connection once queued frames are written.
func (c *client) closeAfterFlush() {
	c.mu.Lock()
	c.stopped = true
	c.cond.Signal()
	c.mu.Unlock()
}

func (c *client) waitFlushed(d time.Duration) {
	select {
	case <-c.flushed:
	case <-time.After(max(d, time.Millisecond)):
		_ = c.conn.Close()
	}
}
