//go:build unix

package pasture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"sync"
	"syscall"
	"time"
)

const (
	helloTimeout = 10 * time.Second
	maxBacklog   = 64 << 20 // output queued for one client before it is dropped
	writeTimeout = 30 * time.Second
)

// Server is the mux server. All state is owned by the goroutine running
// Serve; other goroutines (accept, per-client reader and writer, per-pane
// reader, reaper) talk to it only through channels.
type Server struct {
	sock string
	log  *slog.Logger
	ln   *net.UnixListener
	lock *os.File

	panes    map[int]*pane
	clients  map[*client]bool
	nextPane int
	nextConn int

	conns      chan net.Conn
	paneEvents chan paneEvent
	clientMsgs chan clientMsg
	gone       chan *client
	stop       chan string
	stopOnce   sync.Once
	done       chan struct{}
}

// Config configures a Server.
type Config struct {
	Socket string       // socket path (see DefaultSocket)
	Logger *slog.Logger // nil = discard
}

// NewServer takes the socket's lock (one server per socket) and starts
// listening. A socket left by a crashed server is replaced.
func NewServer(cfg Config) (*Server, error) {
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(discard{}, nil))
	}
	s := &Server{
		sock:       cfg.Socket,
		log:        log,
		panes:      map[int]*pane{},
		clients:    map[*client]bool{},
		conns:      make(chan net.Conn),
		paneEvents: make(chan paneEvent, 256),
		clientMsgs: make(chan clientMsg, 256),
		gone:       make(chan *client, 16),
		stop:       make(chan string, 1),
		done:       make(chan struct{}),
	}
	if s.sock == "" {
		return nil, errors.New("no socket path")
	}
	dir := filepath.Dir(s.sock)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := checkPrivateDir(dir); err != nil {
		return nil, fmt.Errorf("socket directory: %w", err)
	}
	lock, err := lockFile(s.sock + ".lock")
	if err != nil {
		return nil, fmt.Errorf("another server is running for %s (lock: %v)", s.sock, err)
	}
	// The lock file holds the server's pid, so it doubles as a pid file:
	// kill $(cat <socket>.lock)
	_ = lock.Truncate(0)
	_, _ = lock.WriteAt([]byte(fmt.Sprintf("%d\n", os.Getpid())), 0)
	// We hold the lock, so an existing socket file is stale.
	if _, err := os.Lstat(s.sock); err == nil {
		log.Warn("removing stale socket", "socket", s.sock)
		if err := os.Remove(s.sock); err != nil {
			lock.Close()
			return nil, err
		}
	}
	old := syscall.Umask(0o177) // the socket is created 0600
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: s.sock, Net: "unix"})
	syscall.Umask(old)
	if err != nil {
		lock.Close()
		return nil, err
	}
	s.ln, s.lock = ln, lock
	return s, nil
}

// Socket is the path the server listens on.
func (s *Server) Socket() string { return s.sock }

// Shutdown asks Serve to stop: every program is hung up, every client
// disconnected, the socket removed. Safe from any goroutine.
func (s *Server) Shutdown(reason string) {
	s.stopOnce.Do(func() { s.stop <- reason })
}

// Serve runs the server until Shutdown, SIGTERM or SIGINT. SIGHUP and
// SIGPIPE are ignored here (a caller may use SIGHUP, e.g. to reopen logs).
func (s *Server) Serve() error {
	defer close(s.done)
	signal.Ignore(syscall.SIGPIPE)
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)

	s.log.Info("server started", "pid", os.Getpid(), "uid", os.Getuid(), "socket", s.sock, "protocol", ProtocolVersion)
	go s.accept()

	// Keep the socket (and its directory and lock) fresh, so /tmp cleaners
	// such as systemd-tmpfiles (10 days by default) never delete them.
	touch := time.NewTicker(time.Hour)
	defer touch.Stop()

	reason := ""
	for reason == "" {
		select {
		case now := <-touch.C:
			for _, p := range []string{s.sock, s.sock + ".lock", filepath.Dir(s.sock)} {
				if err := os.Chtimes(p, now, now); err != nil {
					s.log.Warn("cannot refresh timestamp", "path", p, "err", err)
				}
			}
		case c := <-s.conns:
			s.newClient(c)
		case ev := <-s.paneEvents:
			s.safely("pane event", func() { s.handlePaneEvent(ev) })
		case m := <-s.clientMsgs:
			s.safely("client message", func() { s.handleClientMsg(m) })
		case c := <-s.gone:
			s.dropClient(c, "connection closed")
		case sig := <-sigs:
			reason = "signal " + sig.String()
		case r := <-s.stop:
			reason = r
		}
	}
	s.shutdown(reason)
	return nil
}

// safely runs fn, logging (not propagating) a panic: one bad message must
// not take down every program the server holds.
func (s *Server) safely(what string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("panic handling "+what, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
		}
	}()
	fn()
}

func (s *Server) shutdown(reason string) {
	s.log.Info("server stopping", "reason", reason, "panes", len(s.panes), "clients", len(s.clients))
	_ = s.ln.Close() // also unlinks the socket
	for _, p := range s.panes {
		p.close(s.log)
	}
	for c := range s.clients {
		c.closeAfterFlush()
	}
	deadline := time.Now().Add(time.Second)
	for c := range s.clients {
		c.waitFlushed(time.Until(deadline))
	}
	_ = os.Remove(s.sock)
	_ = os.Remove(s.sock + ".lock")
	_ = s.lock.Close()
	s.log.Info("server stopped")
}

func (s *Server) accept() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			s.log.Error("accept failed", "err", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if err := checkPeer(conn); err != nil {
			s.log.Warn("connection refused", "err", err)
			conn.Close()
			continue
		}
		select {
		case s.conns <- conn:
		case <-s.done:
			conn.Close()
			return
		}
	}
}

// Panes.

func (s *Server) handlePaneEvent(ev paneEvent) {
	p := ev.p
	if s.panes[p.id] != p {
		return // already forgotten
	}
	if !ev.exited {
		p.bytesOut += int64(len(ev.data))
		s.feed(p, ev.data)
		for c := range p.clients {
			c.send(encodePaneData(msgOutput, p.id, ev.data))
		}
		return
	}
	p.status = ev.status
	p.close(s.log)
	log := s.log.With("pane", p.id, "pid", p.pid, "status", ev.status, "argv", p.argv,
		"bytes_out", p.bytesOut, "bytes_in", p.bytesIn, "ran", time.Since(p.created).Round(time.Millisecond))
	if len(p.clients) > 0 {
		for c := range p.clients {
			c.send(encodeJSON(msgExit, ExitEvent{Pane: p.id, Status: ev.status}))
			delete(c.attached, p)
		}
		delete(s.panes, p.id)
		log.Info("pane exited", "notified", len(p.clients))
		return
	}
	if p.killed {
		delete(s.panes, p.id)
		log.Info("killed pane exited")
		return
	}
	// Nobody is watching: keep the final screen and status until a client
	// attaches (or kills it).
	p.dead = true
	log.Info("pane exited while detached; kept until the next attach")
}

func (s *Server) pane(id int) (*pane, error) {
	p := s.panes[id]
	if p == nil {
		return nil, fmt.Errorf("no pane %d", id)
	}
	return p, nil
}

func (s *Server) attach(c *client, p *pane) {
	p.clients[c] = true
	c.attached[p] = true
	p.muted.Store(true)
}

func (s *Server) detach(c *client, p *pane) {
	delete(p.clients, c)
	delete(c.attached, p)
	if len(p.clients) == 0 {
		p.muted.Store(false)
	}
}

// Clients.

type clientMsg struct {
	c       *client
	typ     byte
	payload []byte
}

func (s *Server) handleClientMsg(m clientMsg) {
	c := m.c
	if c.closed {
		return
	}
	if !c.welcomed {
		if m.typ != msgHello {
			s.dropClient(c, "first frame was not hello")
			return
		}
		var h Hello
		if err := json.Unmarshal(m.payload, &h); err != nil {
			s.dropClient(c, "bad hello: "+err.Error())
			return
		}
		c.name, c.pid = h.Name, h.PID
		c.log = c.log.With("client_name", h.Name, "client_pid", h.PID)
		if h.Version != ProtocolVersion {
			c.send(encodeJSON(msgResponse, Response{Error: fmt.Sprintf(
				"protocol version mismatch: client %d, server %d (restart the server)", h.Version, ProtocolVersion)}))
			s.dropClient(c, fmt.Sprintf("protocol version %d refused", h.Version))
			return
		}
		c.welcomed = true
		c.send(encodeJSON(msgWelcome, Welcome{Version: ProtocolVersion, PID: os.Getpid()}))
		c.log.Info("client connected")
		return
	}
	switch m.typ {
	case msgRequest:
		var r Request
		if err := json.Unmarshal(m.payload, &r); err != nil {
			s.dropClient(c, "bad request: "+err.Error())
			return
		}
		start := time.Now()
		resp, after := s.handleRequest(c, &r)
		resp.ID = r.ID
		c.send(encodeJSON(msgResponse, resp))
		if after != nil {
			after()
		}
		level := slog.LevelDebug
		if resp.Error != "" {
			level = slog.LevelWarn
		}
		c.log.Log(context.Background(), level, "request", "id", r.ID, "op", r.Op, "pane", max(resp.Pane, r.Pane),
			"error", resp.Error, "took", time.Since(start).Round(time.Microsecond))
	case msgInput:
		id, data, err := decodePaneData(m.payload)
		if err != nil {
			s.dropClient(c, err.Error())
			return
		}
		p := s.panes[id]
		switch {
		case p == nil:
			c.log.Debug("input for unknown pane dropped", "pane", id, "bytes", len(data))
		case !p.clients[c]:
			c.log.Warn("input for a pane this client is not attached to dropped", "pane", id, "bytes", len(data))
		case p.closed:
			// exited; its exit event is on the way
		default:
			p.bytesIn += int64(len(data))
			_, _ = p.in.Write(data)
		}
	default:
		s.dropClient(c, fmt.Sprintf("unexpected frame type %d", m.typ))
	}
}

// handleRequest runs a request. The optional function runs after the
// response is queued (used to send an exit event after an attach).
func (s *Server) handleRequest(c *client, r *Request) (Response, func()) {
	fail := func(err error) (Response, func()) { return Response{Error: err.Error()}, nil }
	switch r.Op {
	case OpSpawn:
		sp, err := validateSpawn(r)
		if err != nil {
			return fail(err)
		}
		p, err := s.spawn(sp)
		if err != nil {
			s.nextPane-- // id not used
			return fail(err)
		}
		s.panes[p.id] = p
		s.attach(c, p)
		c.log.Info("pane spawned", "pane", p.id, "pid", p.pid, "argv", p.argv, "dir", sp.dir,
			"size", fmt.Sprintf("%dx%d", p.cols, p.rows), "env_vars", len(sp.env), "history", sp.history)
		return Response{Pane: p.id, PID: p.pid}, nil

	case OpAttach:
		p, err := s.pane(r.Pane)
		if err != nil {
			return fail(err)
		}
		if p.clients[c] {
			return fail(fmt.Errorf("already attached to pane %d", p.id))
		}
		snap := p.snapshot(r.History)
		resp := Response{Pane: p.id, PID: p.pid, Cols: p.cols, Rows: p.rows, Snapshot: snap}
		c.log.Info("pane attached", "pane", p.id, "snapshot_bytes", len(snap), "dead", p.dead,
			"clients", len(p.clients)+1)
		if p.dead {
			// It ended while nobody watched: show it, report the exit, forget it.
			delete(s.panes, p.id)
			status := p.status
			return resp, func() {
				c.send(encodeJSON(msgExit, ExitEvent{Pane: p.id, Status: status}))
				c.log.Info("dead pane delivered and removed", "pane", p.id, "status", status)
			}
		}
		s.attach(c, p)
		return resp, nil

	case OpDetach:
		p, err := s.pane(r.Pane)
		if err != nil {
			return fail(err)
		}
		if !p.clients[c] {
			return fail(fmt.Errorf("not attached to pane %d", p.id))
		}
		s.detach(c, p)
		c.log.Info("pane detached", "pane", p.id, "clients_left", len(p.clients))
		return Response{Pane: p.id}, nil

	case OpResize:
		p, err := s.pane(r.Pane)
		if err != nil {
			return fail(err)
		}
		if err := checkSize(r.Cols, r.Rows); err != nil {
			return fail(err)
		}
		if p.cols != r.Cols || p.rows != r.Rows {
			c.log.Debug("pane resized", "pane", p.id, "from", fmt.Sprintf("%dx%d", p.cols, p.rows),
				"to", fmt.Sprintf("%dx%d", r.Cols, r.Rows))
		}
		p.resize(r.Cols, r.Rows)
		return Response{Pane: p.id}, nil

	case OpSignal:
		p, err := s.pane(r.Pane)
		if err != nil {
			return fail(err)
		}
		sig := syscall.Signal(r.Signal)
		if r.Signal <= 0 || r.Signal > 64 {
			return fail(fmt.Errorf("bad signal %d", r.Signal))
		}
		if p.closed {
			return fail(fmt.Errorf("pane %d has exited", p.id))
		}
		// The foreground process group (e.g. the program running in the
		// shell), else the pane's own process group.
		target := foregroundPgrp(p.ptmx)
		if target <= 0 {
			target = p.pid
		}
		if err := syscall.Kill(-target, sig); err != nil {
			return fail(fmt.Errorf("signal %v to process group %d: %w", sig, target, err))
		}
		c.log.Info("pane signalled", "pane", p.id, "signal", sig.String(), "pgrp", target)
		return Response{Pane: p.id}, nil

	case OpKill:
		p, err := s.pane(r.Pane)
		if err != nil {
			return fail(err)
		}
		c.log.Info("pane killed", "pane", p.id, "pid", p.pid, "dead", p.dead)
		if p.dead {
			delete(s.panes, p.id)
			return Response{Pane: p.id}, nil
		}
		// Hang it up; attached clients get the exit event once it's reaped.
		p.killed = true
		p.close(s.log)
		return Response{Pane: p.id}, nil

	case OpList:
		ids := make([]int, 0, len(s.panes))
		for id := range s.panes {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		resp := Response{Panes: make([]PaneInfo, 0, len(ids))}
		for _, id := range ids {
			resp.Panes = append(resp.Panes, s.panes[id].info())
		}
		return resp, nil
	}
	return fail(fmt.Errorf("unknown op %q", r.Op))
}

func (s *Server) dropClient(c *client, reason string) {
	if c.closed {
		return
	}
	c.closed = true
	for p := range c.attached {
		s.detach(c, p)
	}
	delete(s.clients, c)
	c.closeAfterFlush()
	level := slog.LevelInfo
	switch {
	case !c.welcomed && reason == "connection closed":
		level = slog.LevelDebug // e.g. a readiness probe
	case reason != "connection closed":
		level = slog.LevelWarn // a protocol error
	}
	c.log.Log(context.Background(), level, "client disconnected", "reason", reason, "connected_for", time.Since(c.since).Round(time.Millisecond),
		"clients_left", len(s.clients))
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
