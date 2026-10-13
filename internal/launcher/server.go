package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/pkg/clock"
)

// StartRequest is a start-pty request the server already checked: the
// terminal is valid, the version is installed (Binary is the file), the
// socket is the one the server computes, and the secret or the descriptors
// have the shape the system expects.
type StartRequest struct {
	ID      string
	Version string
	// Binary is the mestre-pty of Version in the installed binaries.
	Binary string
	// Socket is <socket folder>/<id>.sock.
	Socket string
	// Secret is set where descriptors cannot be passed.
	Secret []byte
	// Files are the descriptors that came with the request, in the order of
	// Roles. The server closes them when Start returns.
	Files []*os.File
	Roles []string
}

// Started is the mestre-pty that started.
type Started struct {
	PID       int
	Start     string
	CgroupDir string
}

// Backend does what differs by system: creating the mestre-pty with the
// account of the agents, and ending the tree of a terminal. An error made
// with Refused is answered as a refusal; any other as a failure.
type Backend interface {
	Start(ctx context.Context, r StartRequest) (Started, error)
	Kill(ctx context.Context, id string) error
}

// PeerCheck says whether the other end of a connection may ask anything.
// who describes it for the log (even when err is set).
type PeerCheck func(c net.Conn) (who string, err error)

// The outcomes of an Event.
const (
	OutcomeOK      = "ok"
	OutcomeRefused = "refused"
	OutcomeFailed  = "failed"
)

// Event is the record of one request, kept without its body (which carries
// the secret of the terminal).
type Event struct {
	Time    time.Time
	Op      string
	ID      string
	Version string
	Peer    string
	Outcome string
	Reason  string
}

// Config configures a Server.
type Config struct {
	Backend Backend
	Check   PeerCheck
	// BinDir holds the installed binaries, <version>/mestre-pty.
	BinDir string
	// SocketDir is where the sockets of the terminals are.
	SocketDir string
	// SecretInline: the secret travels in the request (Windows); otherwise
	// it comes as the second descriptor (Linux).
	SecretInline bool
	// Audit receives the record of every request, besides the log.
	Audit  func(Event)
	Logger *slog.Logger
	Clock  clock.Clock
	// ReadTimeout bounds waiting for the request after a connection is
	// accepted (default 10 s).
	ReadTimeout time.Duration
	// WorkTimeout bounds doing it (default 30 s).
	WorkTimeout time.Duration
	// MaxConns is how many connections are served at once (default 8).
	MaxConns int
}

// Server answers the requests of the engine.
type Server struct {
	cfg Config
	clk clock.Clock
	log *slog.Logger
	sem chan struct{}
}

// NewServer checks cfg and returns a Server.
func NewServer(cfg Config) (*Server, error) {
	switch {
	case cfg.Backend == nil:
		return nil, errors.New("launcher: no backend")
	case cfg.Check == nil:
		return nil, errors.New("launcher: no peer check: it would accept anyone")
	case cfg.BinDir == "" || cfg.SocketDir == "":
		return nil, errors.New("launcher: no folder of binaries or of sockets")
	}
	s := &Server{cfg: cfg, clk: cfg.Clock, log: cfg.Logger}
	if s.clk == nil {
		s.clk = clock.Real{}
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.cfg.ReadTimeout == 0 {
		s.cfg.ReadTimeout = 10 * time.Second
	}
	if s.cfg.WorkTimeout == 0 {
		s.cfg.WorkTimeout = 30 * time.Second
	}
	if s.cfg.MaxConns == 0 {
		s.cfg.MaxConns = 8
	}
	s.sem = make(chan struct{}, s.cfg.MaxConns)
	return s, nil
}

// Serve accepts connections until ctx ends or the listener fails. It closes
// the listener, and returns after the requests being served are done.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		select {
		case s.sem <- struct{}{}:
		default:
			s.record(Event{Op: "connect", Peer: "?", Outcome: OutcomeRefused, Reason: "too many connections"})
			_ = c.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-s.sem }()
			s.ServeConn(ctx, c)
		}()
	}
}

// ServeConn serves the one request of c and closes it.
func (s *Server) ServeConn(ctx context.Context, nc net.Conn) {
	conn := NewConn(nc)
	defer conn.Close()
	who, err := s.cfg.Check(nc)
	if err != nil {
		// Whoever this is, its bytes are not read.
		s.reply(conn, s.refuse(Event{Op: "connect", Peer: who}, err))
		return
	}
	ev := Event{Peer: who}
	expire := s.clk.AfterFunc(s.cfg.ReadTimeout, func() { _ = nc.Close() })
	frame, files, err := conn.ReadFrame()
	expire.Stop()
	defer closeAll(files)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			s.reply(conn, s.refuse(ev, err))
		}
		return
	}
	req, err := decodeRequest(frame)
	if err != nil {
		s.reply(conn, s.refuse(ev, err))
		return
	}
	ev.Op, ev.ID, ev.Version = req.Op, req.ID, req.Version
	if err := validateRequest(req); err != nil {
		s.reply(conn, s.refuse(ev, err))
		return
	}
	ctx, cancel := clock.WithTimeout(ctx, s.clk, s.cfg.WorkTimeout)
	defer cancel()
	var resp Response
	switch req.Op {
	case OpStartPTY:
		resp = s.start(ctx, ev, req, files)
	default:
		resp = s.kill(ctx, ev, req)
	}
	s.reply(conn, resp)
}

func (s *Server) start(ctx context.Context, ev Event, req Request, files []*os.File) Response {
	sr, err := s.checkStart(req, files)
	if err != nil {
		return s.refuse(ev, err)
	}
	st, err := s.cfg.Backend.Start(ctx, sr)
	if err != nil {
		return s.fail(ev, err)
	}
	s.record(withOutcome(ev, OutcomeOK, fmt.Sprintf("pid %d", st.PID)))
	return Response{OK: true, PID: st.PID, Start: st.Start, CgroupDir: st.CgroupDir}
}

// checkStart is the part of a start-pty request that decides whether any
// process is created: the binary, the socket and the secret are the ones
// this launcher computes or expects.
func (s *Server) checkStart(req Request, files []*os.File) (StartRequest, error) {
	bin, err := PTYBinary(s.cfg.BinDir, req.Version)
	if err != nil {
		return StartRequest{}, err
	}
	sock, err := terminal.SocketPath(s.cfg.SocketDir, req.ID)
	if err != nil {
		return StartRequest{}, err
	}
	if req.Socket != sock {
		return StartRequest{}, fmt.Errorf("socket %q is not the one of terminal %s (%q)", req.Socket, req.ID, sock)
	}
	sr := StartRequest{ID: req.ID, Version: req.Version, Binary: bin, Socket: sock}
	if s.cfg.SecretInline {
		if len(req.Secret) != ptywire.SecretSize || len(req.Files) != 0 || len(files) != 0 {
			return StartRequest{}, errors.New("start-pty takes the secret in the request and no descriptors")
		}
		sr.Secret = req.Secret
		return sr, nil
	}
	if len(req.Secret) != 0 {
		return StartRequest{}, errors.New("start-pty takes the secret as a descriptor")
	}
	if err := checkRoles(req.Files, len(files)); err != nil {
		return StartRequest{}, err
	}
	sr.Files, sr.Roles = files, req.Files
	return sr, nil
}

func checkRoles(roles []string, got int) error {
	want := []string{RoleListen, RoleSecret}
	if len(roles) == 3 {
		want = append(want, RoleLog)
	}
	if len(roles) != len(want) || got != len(want) {
		return fmt.Errorf("start-pty takes descriptors %v, got %d for %v", want, got, roles)
	}
	for i := range want {
		if roles[i] != want[i] {
			return fmt.Errorf("start-pty takes descriptors %v, got %v", want, roles)
		}
	}
	return nil
}

func (s *Server) kill(ctx context.Context, ev Event, req Request) Response {
	if err := s.cfg.Backend.Kill(ctx, req.ID); err != nil {
		return s.fail(ev, err)
	}
	s.record(withOutcome(ev, OutcomeOK, ""))
	return Response{OK: true}
}

func (s *Server) refuse(ev Event, err error) Response {
	s.record(withOutcome(ev, OutcomeRefused, err.Error()))
	return Response{Code: CodeRefused, Error: err.Error()}
}

func (s *Server) fail(ev Event, err error) Response {
	var r refusedError
	if errors.As(err, &r) {
		return s.refuse(ev, err)
	}
	s.record(withOutcome(ev, OutcomeFailed, err.Error()))
	return Response{Code: CodeFailed, Error: err.Error()}
}

func withOutcome(ev Event, outcome, reason string) Event {
	ev.Outcome, ev.Reason = outcome, reason
	return ev
}

// record logs ev without any body and hands it to the audit hook.
func (s *Server) record(ev Event) {
	ev.Time = s.clk.Now()
	level := slog.LevelInfo
	if ev.Outcome != OutcomeOK {
		level = slog.LevelWarn
	}
	s.log.Log(context.Background(), level, "launcher request",
		"op", ev.Op, "terminal", ev.ID, "version", ev.Version, "peer", ev.Peer,
		"outcome", ev.Outcome, "reason", ev.Reason)
	if s.cfg.Audit != nil {
		s.cfg.Audit(ev)
	}
}

func (s *Server) reply(c Conn, r Response) {
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	_ = c.WriteFrame(b, nil)
}
