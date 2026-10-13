package localchan

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// Defaults of the server.
const (
	// DefaultReadTimeout is how long a connection may take to send its
	// request line.
	DefaultReadTimeout = 10 * time.Second
	// DefaultRetain is how long the response of a finished request is kept
	// for a resend with the same id.
	DefaultRetain = 10 * time.Minute
	// DefaultMaxRetained bounds the finished requests kept.
	DefaultMaxRetained = 4096
)

// Commands the engine resolves itself (docs/arquitetura/canal-local.md#encaminhamento-à-cidadela).
const (
	CmdHelp  = "help"
	CmdDebug = "debug"
	CmdHook  = "hook"
)

// Audit is what the engine records about a request: the trail of the
// machine reads it (docs/arquitetura/seguranca.md). Refusals are recorded
// with Code not_identified and the reason.
type Audit struct {
	At         time.Time
	ID         string
	Cmd        string
	AgentID    string
	TerminalID string
	PID        int
	// Code is the error code of the response, empty on success.
	Code string
	Exit int
	// Reason is why a caller was refused (not for the CLI: it may name
	// internals).
	Reason string
}

// Config configures a Server.
type Config struct {
	Clock      clock.Clock
	Identifier Identifier
	// Local holds the commands the engine resolves itself, besides the
	// built-in `debug` and a minimal `help`: the `hook` dispatcher (M3)
	// registers here.
	Local map[string]Handler
	// Forwarder serves every other command; nil answers
	// citadel_unavailable.
	Forwarder Forwarder

	// Heartbeat (15 s) is the interval of the sign of life sent while a
	// request runs.
	Heartbeat time.Duration
	// ReadTimeout (10 s) bounds the wait for the request line.
	ReadTimeout time.Duration
	// Retain (10 min) and MaxRetained (4096): see DefaultRetain.
	Retain      time.Duration
	MaxRetained int

	// Instance and EngineVersion are what `debug` reports.
	Instance      string
	EngineVersion string

	OnAudit func(Audit)
	Logger  *slog.Logger
}

// Server is the engine side of the local channel.
type Server struct {
	cfg   Config
	clk   clock.Clock
	log   *slog.Logger
	table *flights

	mu     sync.Mutex
	ctx    context.Context
	conns  map[net.Conn]struct{}
	closed bool
	wg     sync.WaitGroup
}

// NewServer returns a Server.
func NewServer(cfg Config) *Server {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Heartbeat == 0 {
		cfg.Heartbeat = DefaultHeartbeat
	}
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = DefaultReadTimeout
	}
	if cfg.Retain == 0 {
		cfg.Retain = DefaultRetain
	}
	if cfg.MaxRetained == 0 {
		cfg.MaxRetained = DefaultMaxRetained
	}
	if cfg.Forwarder == nil {
		cfg.Forwarder = UnavailableForwarder{}
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Server{
		cfg: cfg, clk: cfg.Clock, log: log,
		table: newFlights(cfg.Clock, cfg.Retain, cfg.MaxRetained),
		conns: map[net.Conn]struct{}{},
	}
}

// Serve accepts connections on ln until ctx ends or ln fails, then closes
// ln and every connection and waits for the requests in progress (their
// context ends with ctx). Requests run with ctx, not with the connection.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	stop := context.AfterFunc(ctx, func() {
		_ = ln.Close()
		s.closeConns()
	})
	defer stop()
	defer s.wg.Wait()
	defer s.closeConns()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept on the local channel: %w", err)
		}
		if !s.track(c) {
			_ = c.Close()
			return nil
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.untrack(c)
			s.serveConn(ctx, c)
		}()
	}
}

func (s *Server) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) untrack(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	_ = c.Close()
}

func (s *Server) closeConns() {
	s.mu.Lock()
	s.closed = true
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// serveConn serves one connection: one request, and its response.
func (s *Server) serveConn(ctx context.Context, c net.Conn) {
	// The peer is read before anything else: the caller can die, and its
	// PID be reused, at any moment after the accept.
	peer, peerErr := s.cfg.Identifier.Accept(c)
	defer func() { peer.Close() }() // a peer that failed to be read may still hold a pidfd

	br := bufio.NewReaderSize(c, 64<<10)
	timer := s.clk.AfterFunc(s.cfg.ReadTimeout, func() { _ = c.Close() })
	line, err := readLine(br)
	timer.Stop()
	if err != nil {
		if errors.Is(err, ErrTooLarge) {
			s.refuse(c, Version, "", Fail(CodeTooLarge, fmt.Sprintf("the request is over %d bytes", MaxLine),
				"send big content as a file path (--file, --attach)"))
		}
		return
	}

	req, resp, ok := s.parse(line)
	if !ok {
		s.refuse(c, resp.V, req.ID, resp)
		return
	}

	caller, why := s.identify(ctx, peer, peerErr, req)
	if why != nil {
		s.audit(Audit{ID: req.ID, Cmd: req.Cmd, AgentID: req.AgentID, PID: peer.PID, Code: CodeNotIdentified,
			Exit: ExitDenied, Reason: why.Error()})
		s.log.Warn("local channel call refused", "cmd", req.Cmd, "agent", req.AgentID, "pid", peer.PID, "reason", why.Error())
		s.refuse(c, req.V, req.ID, Fail(CodeNotIdentified,
			"the caller could not be identified as a process of the terminal of this character",
			"run mestre debug"))
		return
	}

	f, owner, err := s.table.join(flightKey{agent: caller.verifiedAgent(), id: req.ID}, req.Cmd)
	if err != nil {
		s.refuse(c, req.V, req.ID, Fail(CodeBadRequest, err.Error(), ""))
		return
	}
	if owner {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.execute(ctx, f, req, caller)
		}()
	}

	// The CLI sends nothing more: whatever arrives, or the end of the
	// stream, says the connection is gone. Closing it does not cancel the
	// request.
	gone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, br)
		close(gone)
	}()
	s.stream(c, req, f, gone)
}

func (c Caller) verifiedAgent() string {
	if c.Identified {
		return c.AgentID
	}
	return ""
}

// parse decodes and validates the request line. On failure ok is false and
// resp is the error to send; resp.V is the version to answer in.
func (s *Server) parse(line []byte) (req Request, resp Response, ok bool) {
	var head struct {
		V  int    `json:"v"`
		ID string `json:"id"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return req, Fail(CodeBadRequest, "the request is not a JSON object: "+err.Error(), "").withV(Version), false
	}
	req.ID = head.ID
	// The version comes first: a request of a version this engine does not
	// know may have another shape.
	if head.V < MinVersion || head.V > Version {
		return req, Fail(CodeProtocolMismatch,
			fmt.Sprintf("the engine speaks local channel versions %d to %d, the request is version %d", MinVersion, Version, head.V),
			"update mestre").withV(Version), false
	}
	if err := json.Unmarshal(line, &req); err != nil {
		return req, Fail(CodeBadRequest, "malformed request: "+err.Error(), "").withV(head.V), false
	}
	switch {
	case req.ID == "" || len(req.ID) > 128:
		return req, Fail(CodeBadRequest, "the request has no valid id", "").withV(head.V), false
	case req.Cmd == "":
		return req, Fail(CodeBadRequest, "the request has no cmd", "").withV(head.V), false
	}
	return req, Response{}, true
}

func (r Response) withV(v int) Response {
	r.V = v
	return r
}

// openCommands run for callers that are not identified, and say so.
func tolerant(cmd string) bool { return cmd == CmdHelp || cmd == CmdDebug }

// identify decides who calls. A request that names a character must come
// from a process of a terminal of that character; one that names none can
// only run help, debug and hook (docs/cli/convencoes.md#identificação-do-chamador).
// For help and debug a failed identification is not an error: the command
// runs and reports that the caller is not identified.
func (s *Server) identify(ctx context.Context, peer Peer, peerErr error, req Request) (Caller, error) {
	claimed := Caller{AgentID: req.AgentID, PID: peer.PID}
	if req.AgentID == "" {
		if tolerant(req.Cmd) || req.Cmd == CmdHook {
			return claimed, nil
		}
		return Caller{}, ErrNoAgent
	}
	var (
		id  Identity
		err = peerErr
	)
	if err == nil {
		id, err = s.cfg.Identifier.Identify(ctx, peer, req.AgentID)
	}
	if err != nil {
		if tolerant(req.Cmd) {
			return claimed, nil
		}
		return Caller{}, err
	}
	return Caller{Identified: true, AgentID: id.AgentID, TerminalID: id.TerminalID, PID: id.PID}, nil
}

// refuse sends an error that is not stored: the request did not run.
func (s *Server) refuse(c net.Conn, v int, id string, resp Response) {
	resp.V, resp.ID = v, id
	if b, err := encodeFinal(resp); err == nil {
		_, _ = c.Write(b)
	}
}

// execute runs the request and stores its response.
func (s *Server) execute(ctx context.Context, f *flight, req Request, caller Caller) {
	call := &Call{Request: req, Caller: caller, publish: f.publish}
	resp := s.dispatch(ctx, call)
	resp.V, resp.ID, resp.Partial = req.V, req.ID, false
	if resp.Error != nil {
		resp.OK = false
		if resp.Error.Details == nil {
			resp.Error.Details = map[string]any{}
		}
	}
	f.finish(resp, s.clk.Now())
	a := Audit{At: s.clk.Now(), ID: req.ID, Cmd: req.Cmd, AgentID: caller.verifiedAgent(), TerminalID: caller.TerminalID,
		PID: caller.PID, Exit: resp.ExitCode()}
	if resp.Error != nil {
		a.Code = resp.Error.Code
	}
	s.audit(a)
}

// dispatch routes the request: help, debug and hook are the engine's; the
// rest goes to the Citadel. A handler that panics answers `internal`.
func (s *Server) dispatch(ctx context.Context, call *Call) (resp Response) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("a command of the local channel panicked", "cmd", call.Request.Cmd, "panic", fmt.Sprint(r))
			resp = Fail(CodeInternal, "internal error while serving the request", "see the engine log")
		}
	}()
	if h, ok := s.cfg.Local[call.Request.Cmd]; ok {
		return h.Handle(ctx, call)
	}
	switch call.Request.Cmd {
	case CmdDebug:
		return s.debug(call)
	case CmdHelp:
		return OK(map[string]any{"commands": []string{}})
	case CmdHook:
		return Fail(CodeUnavailable, "this engine has no hook dispatcher", "")
	}
	return s.cfg.Forwarder.Forward(ctx, call)
}

// debug is the engine's part of `mestre debug`: its versions and whether it
// accepts the identification of the caller (items 1, 3 and 4).
func (s *Server) debug(call *Call) Response {
	return OK(map[string]any{
		"engine": map[string]any{
			"version":      s.cfg.EngineVersion,
			"instance":     s.cfg.Instance,
			"protocol":     Version,
			"min_protocol": MinVersion,
		},
		"caller": map[string]any{
			"agent_id":   call.Caller.AgentID,
			"identified": call.Caller.Identified,
			"terminal":   call.Caller.TerminalID,
		},
	})
}

func (s *Server) audit(a Audit) {
	if a.At.IsZero() {
		a.At = s.clk.Now()
	}
	if f := s.cfg.OnAudit; f != nil {
		f(a)
	}
}

// stream writes to the connection the partials of the request, a sign of
// life every Heartbeat, and the final response; it returns when the
// response was written or the connection is gone. The request itself does
// not depend on it: another connection with the same id gets the same.
func (s *Server) stream(c net.Conn, req Request, f *flight, gone <-chan struct{}) {
	tick := s.clk.NewTicker(s.cfg.Heartbeat)
	defer tick.Stop()
	next := 0
	for {
		parts, changed, final := f.snapshot(next)
		for _, p := range parts {
			if !s.write(c, req, p) {
				return
			}
		}
		next += len(parts)
		if final != nil {
			if b, err := encodeFinal(*final); err == nil {
				_, _ = c.Write(b)
			}
			return
		}
		select {
		case <-changed:
		case <-tick.C():
			if !s.write(c, req, json.RawMessage(`{"status":"alive"}`)) {
				return
			}
		case <-gone:
			return
		}
	}
}

func (s *Server) write(c net.Conn, req Request, data json.RawMessage) bool {
	b, err := encodePartial(req.V, req.ID, data)
	if err != nil {
		return false
	}
	_, err = c.Write(b)
	return err == nil
}
