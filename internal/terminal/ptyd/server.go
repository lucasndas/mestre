package ptyd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/pkg/clock"
)

// ServerConfig configures a Server.
type ServerConfig struct {
	Secret  []byte
	Session *Session
	Clock   clock.Clock
	// Version is the version of this mestre-pty; PID its process ID.
	Version string
	PID     int
	// ProtoVersion is the protocol version announced (default
	// ptywire.ProtoVersion). A smaller one makes this server behave as the
	// older mestre-pty that had it, which is how the tests play a terminal
	// created by a previous version.
	ProtoVersion     int
	HandshakeTimeout time.Duration
	Logger           *slog.Logger
}

// Server answers the engine's connections to one Session.
type Server struct {
	cfg  ServerConfig
	sess *Session
	log  *slog.Logger

	done     chan struct{}
	doneOnce sync.Once

	mu    sync.Mutex
	peers map[*peer]struct{}
}

// NewServer returns a Server. Events of the session are broadcast to every
// authenticated connection; set SessionConfig.OnEvent to Server.Broadcast.
func NewServer(cfg ServerConfig) *Server {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.ProtoVersion == 0 {
		cfg.ProtoVersion = ptywire.ProtoVersion
	}
	if cfg.HandshakeTimeout == 0 {
		cfg.HandshakeTimeout = ptywire.HandshakeTimeout
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Server{cfg: cfg, sess: cfg.Session, log: log, done: make(chan struct{}), peers: map[*peer]struct{}{}}
}

// Done is closed when the mestre-pty should exit: the agent ended and the
// engine confirmed all of its output, or the engine asked to kill it.
func (s *Server) Done() <-chan struct{} { return s.done }

func (s *Server) finish() { s.doneOnce.Do(func() { close(s.done) }) }

// Broadcast sends an event to every authenticated connection. An event is
// a hint: a connection whose queue is full loses it and finds the truth in
// "status" (the exit and the stability are sticky there).
func (s *Server) Broadcast(m ptywire.Msg) {
	s.mu.Lock()
	peers := make([]*peer, 0, len(s.peers))
	for p := range s.peers {
		peers = append(peers, p)
	}
	s.mu.Unlock()
	for _, p := range peers {
		p.tryEnqueue(out{msg: &m})
	}
}

// Serve accepts connections until Done is closed or ln fails. It closes ln
// and every connection before returning.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-s.done:
		case <-ctx.Done():
		}
		_ = ln.Close()
	}()
	var wg sync.WaitGroup
	defer wg.Wait()
	defer s.closePeers()
	for {
		nc, err := ln.Accept()
		if err != nil {
			select {
			case <-s.done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.ServeConn(ctx, nc)
		}()
	}
}

func (s *Server) closePeers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for p := range s.peers {
		p.close()
	}
}

// ServeConn runs the handshake and then the requests of one connection.
func (s *Server) ServeConn(ctx context.Context, nc net.Conn) {
	c := ptywire.NewConn(nc)
	timer := s.cfg.Clock.AfterFunc(s.cfg.HandshakeTimeout, func() { _ = nc.Close() })
	_, err := ptywire.ServerHandshake(c, s.cfg.Secret, ptywire.Info{Proto: s.cfg.ProtoVersion, Version: s.cfg.Version, PID: s.cfg.PID})
	timer.Stop()
	if err != nil {
		// The audit trail of the machine: who failed to prove the secret.
		s.log.Warn("terminal handshake refused", "remote", nc.RemoteAddr().String(), "reason", err.Error())
		_ = nc.Close()
		return
	}
	p := newPeer(c)
	s.mu.Lock()
	s.peers[p] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.peers, p)
		s.mu.Unlock()
		p.close()
	}()
	go p.writeLoop()
	for {
		it, err := c.Read()
		if err != nil {
			return
		}
		if it.Msg == nil {
			continue // the engine does not send frames
		}
		s.dispatch(ctx, p, it.Msg)
	}
}

func replyErr(id uint64, code, text string) out {
	return out{msg: &ptywire.Msg{T: ptywire.TypeError, ID: id, Code: code, Error: text}}
}

func okMsg(id uint64) out { return out{msg: &ptywire.Msg{T: ptywire.TypeOK, ID: id}} }

func (s *Server) dispatch(ctx context.Context, p *peer, m *ptywire.Msg) {
	switch m.T {
	case ptywire.TypeStart:
		s.handleStart(ctx, p, m)
	case ptywire.TypeStatus:
		p.enqueue(out{msg: s.status(m.ID)})
	case ptywire.TypeScreen:
		s.handleScreen(ctx, p, m)
	case ptywire.TypeReplay:
		s.handleReplay(p, m)
	case ptywire.TypeFollow:
		s.handleFollow(ctx, p, m)
	case ptywire.TypeUnfollow:
		p.stopFollow()
		p.enqueue(okMsg(m.ID))
	case ptywire.TypeInput:
		if err := s.sess.Input(m.Data); err != nil {
			p.enqueue(replyErr(m.ID, ptywire.CodeNotRunning, err.Error()))
			return
		}
		p.enqueue(okMsg(m.ID))
	case ptywire.TypeAck:
		s.sess.Buffer().Ack(m.Offset)
		p.enqueue(okMsg(m.ID))
		if s.sess.Exited() && s.sess.Buffer().Acked() >= s.sess.Buffer().End() {
			// The agent ended and the engine's log holds all of its output:
			// nothing is left to keep this process for.
			p.flush()
			s.finish()
		}
	case ptywire.TypeStop:
		s.handleStop(p, m)
	case ptywire.TypePing:
		if s.cfg.ProtoVersion < ptywire.ProtoPing {
			p.enqueue(replyErr(m.ID, ptywire.CodeUnknownMessage, "unknown message ping"))
			return
		}
		p.enqueue(out{msg: &ptywire.Msg{T: ptywire.TypeOK, ID: m.ID, End: s.sess.Buffer().End()}})
	case ptywire.TypeInJob:
		s.handleInJob(p, m)
	default:
		// Additive protocol: a message from a newer engine is refused for
		// this request only, the connection goes on.
		p.enqueue(replyErr(m.ID, ptywire.CodeUnknownMessage, "unknown message "+m.T))
	}
}

// handleInJob answers whether a process is in the Job Object of the
// terminal: how the engine identifies the caller of the CLI on Windows
// (docs/arquitetura/canal-local.md#identificação-e-versão).
func (s *Server) handleInJob(p *peer, m *ptywire.Msg) {
	if s.cfg.ProtoVersion < ptywire.ProtoInJob {
		p.enqueue(replyErr(m.ID, ptywire.CodeUnknownMessage, "unknown message injob"))
		return
	}
	if m.PID <= 0 {
		p.enqueue(replyErr(m.ID, ptywire.CodeBadRequest, "injob without a pid"))
		return
	}
	in, err := s.sess.InTree(m.PID, m.Start)
	switch {
	case errors.Is(err, ErrNoJob):
		p.enqueue(replyErr(m.ID, ptywire.CodeNotSupported, err.Error()))
	case err != nil:
		p.enqueue(replyErr(m.ID, ptywire.CodeNotRunning, err.Error()))
	default:
		p.enqueue(out{msg: &ptywire.Msg{T: ptywire.TypeOK, ID: m.ID, InJob: &in}})
	}
}

func (s *Server) handleStart(ctx context.Context, p *peer, m *ptywire.Msg) {
	if m.Path == "" {
		p.enqueue(replyErr(m.ID, ptywire.CodeBadRequest, "start without a path"))
		return
	}
	pid, err := s.sess.Start(ctx, StartSpec{
		Path: m.Path, Args: m.Args, Env: m.Env, Dir: m.Dir,
		Cols: clampSize(m.Cols), Rows: clampSize(m.Rows),
	})
	switch {
	case errors.Is(err, ErrAlreadyStarted):
		p.enqueue(replyErr(m.ID, ptywire.CodeAlreadyStarted, err.Error()))
	case err != nil:
		s.log.Error("terminal start failed", "error", err)
		p.enqueue(replyErr(m.ID, ptywire.CodeStartFailed, err.Error()))
	default:
		p.enqueue(out{msg: &ptywire.Msg{T: ptywire.TypeStarted, ID: m.ID, AgentPID: pid}})
	}
}

func clampSize(v int) uint16 {
	if v <= 0 || v > 1<<14 {
		return 0
	}
	return uint16(v)
}

func (s *Server) status(id uint64) *ptywire.Msg {
	st := s.sess.State()
	buf := s.sess.Buffer()
	m := &ptywire.Msg{
		T: ptywire.TypeStatus, ID: id, State: st.State, AgentPID: st.AgentPID, Stable: st.Stable,
		Cols: int(st.Cols), Rows: int(st.Rows),
		Acked: buf.Acked(), BufferStart: buf.Start(), End: buf.End(),
	}
	if st.State == ptywire.StateExited {
		code := st.Exit.Code
		m.ExitCode, m.Signal = &code, st.Exit.Signal
	}
	return m
}

func (s *Server) handleScreen(ctx context.Context, p *peer, m *ptywire.Msg) {
	lines := 0
	if s.cfg.ProtoVersion >= ptywire.ProtoPing {
		lines = m.Lines // the older mestre-pty never had history on request
	}
	snap, err := s.sess.Screen(ctx, lines)
	if err != nil {
		p.enqueue(replyErr(m.ID, ptywire.CodeNotRunning, err.Error()))
		return
	}
	p.enqueue(out{msg: &ptywire.Msg{
		T: ptywire.TypeScreen, ID: m.ID, Text: snap.Text, History: snap.History,
		CurCol: snap.CursorCol, CurRow: snap.CursorRow, Offset: snap.Offset,
	}})
}

// handleReplay answers with a header and then the frames of the bytes it
// announces. Everything goes through the connection's single writer, so
// other messages may be interleaved between frames but never inside one.
func (s *Server) handleReplay(p *peer, m *ptywire.Msg) {
	c := s.sess.Buffer().Replay(m.From, ptywire.MaxReplay)
	p.enqueue(out{msg: &ptywire.Msg{
		T: ptywire.TypeReplay, ID: m.ID, Offset: c.Offset, Lost: c.Lost, Len: len(c.Data), End: s.sess.Buffer().End(),
	}})
	for off := 0; off < len(c.Data); off += ptywire.MaxFrame {
		end := min(off+ptywire.MaxFrame, len(c.Data))
		p.enqueue(out{frame: &ptywire.Frame{Stream: m.ID, Offset: c.Offset + uint64(off), Data: c.Data[off:end]}})
	}
}

// handleFollow starts the live stream of this connection at offset From:
// the header says where it begins and how much is lost, then every byte
// arrives in frames as it is written. A jump between two frames is a chunk
// that left the buffer before the engine could read it.
func (s *Server) handleFollow(ctx context.Context, p *peer, m *ptywire.Msg) {
	buf := s.sess.Buffer()
	off, lost := buf.Position(m.From)
	fctx := p.startFollow(ctx)
	p.enqueue(out{msg: &ptywire.Msg{T: ptywire.TypeFollow, ID: m.ID, Offset: off, Lost: lost}})
	go func() {
		next := off
		for {
			c, err := buf.Next(fctx, next, ptywire.MaxFrame)
			if err != nil {
				return
			}
			if !p.enqueueCtx(fctx, out{frame: &ptywire.Frame{Stream: m.ID, Offset: c.Offset, Data: c.Data}}) {
				return
			}
			next = c.Offset + uint64(len(c.Data))
		}
	}()
}

func (s *Server) handleStop(p *peer, m *ptywire.Msg) {
	switch m.Mode {
	case ptywire.StopSignal:
		if err := s.sess.Signal(); err != nil {
			p.enqueue(replyErr(m.ID, ptywire.CodeNotRunning, err.Error()))
			return
		}
		p.enqueue(okMsg(m.ID))
	case ptywire.StopKill:
		err := s.sess.Kill()
		if err != nil {
			s.log.Error("terminal kill failed", "error", err)
		}
		p.enqueue(okMsg(m.ID))
		p.flush()
		s.finish()
	default:
		p.enqueue(replyErr(m.ID, ptywire.CodeBadRequest, "unknown stop mode "+m.Mode))
	}
}

// out is one item for the writer of a connection: a message or a frame, or
// a marker the writer closes once everything before it was written.
type out struct {
	msg   *ptywire.Msg
	frame *ptywire.Frame
	mark  chan struct{}
}

// peer is one authenticated connection. A single goroutine writes to it.
type peer struct {
	c      *ptywire.Conn
	queue  chan out
	closed chan struct{}
	once   sync.Once

	mu           sync.Mutex
	followCancel context.CancelFunc
}

func newPeer(c *ptywire.Conn) *peer {
	return &peer{c: c, queue: make(chan out, 256), closed: make(chan struct{})}
}

func (p *peer) close() {
	p.once.Do(func() {
		close(p.closed)
		p.stopFollow()
		_ = p.c.Close()
	})
}

func (p *peer) writeLoop() {
	for {
		select {
		case it := <-p.queue:
			var err error
			switch {
			case it.msg != nil:
				err = p.c.WriteMsg(it.msg)
			case it.frame != nil:
				err = p.c.WriteFrame(it.frame)
			case it.mark != nil:
				close(it.mark)
			}
			if err != nil {
				p.close()
				return
			}
		case <-p.closed:
			return
		}
	}
}

// enqueue waits for room in the queue: a slow connection only slows its own
// requests.
func (p *peer) enqueue(it out) {
	select {
	case p.queue <- it:
	case <-p.closed:
	}
}

func (p *peer) enqueueCtx(ctx context.Context, it out) bool {
	select {
	case p.queue <- it:
		return true
	case <-p.closed:
		return false
	case <-ctx.Done():
		return false
	}
}

func (p *peer) tryEnqueue(it out) {
	select {
	case p.queue <- it:
	default:
	}
}

// flush waits until everything queued so far was written (or the
// connection ended).
func (p *peer) flush() {
	mark := make(chan struct{})
	p.enqueue(out{mark: mark})
	select {
	case <-mark:
	case <-p.closed:
	}
}

func (p *peer) startFollow(parent context.Context) context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.followCancel != nil {
		p.followCancel()
	}
	ctx, cancel := context.WithCancel(parent)
	p.followCancel = cancel
	return ctx
}

func (p *peer) stopFollow() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.followCancel != nil {
		p.followCancel()
		p.followCancel = nil
	}
}
