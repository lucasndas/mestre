package app

import (
	"context"
	"errors"
	"sync"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/terminal"
	"github.com/mestre-ia/mestre/internal/termstream"
)

// maxKeysInput is the most a person types in one message of the panel.
const maxKeysInput = 64 << 10

// The machine side of the terminal in the browser (docs/arquitetura/terminal.md
// and docs/conectividade/protocolo.md#canal-do-navegador): on terminal.replay a
// machine, a Tower or the Citadel's own engine, reads its terminal from the
// buffer of the mestre-pty and sends it to the Citadel in frames 0x01, then goes
// on sending what the terminal prints, until terminal.unfollow or the end of the
// connection. Nothing is kept: the buffer is the one of the mestre-pty.

// frameLink is what the stream needs of the connection to the Citadel
// (*protocol.Link).
type frameLink interface {
	SendData(ctx context.Context, f protocol.DataFrame) error
	Send(ctx context.Context, o protocol.Outgoing) (string, error)
	Session() *protocol.Session
}

// terminalServer answers terminal.replay from the terminals of a machine.
type terminalServer struct {
	src termstream.Source

	mu      sync.Mutex
	streams map[string]*streamRef // by stream id
}

func newTerminalServer(src termstream.Source) *terminalServer {
	return &terminalServer{src: src, streams: map[string]*streamRef{}}
}

// streamRef is a running stream; the pointer tells it from the one that replaced it.
type streamRef struct{ cancel context.CancelFunc }

// errStreamGone ends a stream whose connection is not the one it began on.
var errStreamGone = errors.New("app: the connection of the terminal stream ended")

// serve starts the stream of a replay request on the link; it returns at once,
// the stream runs in a goroutine that ends with unfollow, a newer stream of the
// same id, the end of the connection or the end of the terminal.
func (s *terminalServer) serve(link frameLink, req protocol.TerminalReplay) error {
	sid := req.Stream
	if sid == "" {
		sid = req.TerminalID
	}
	frameID, err := protocol.ParseULID(sid)
	if err != nil {
		return protocol.NewError(protocol.CodeInvalidPayload, "terminal.replay: the stream %q is not a ULID", sid)
	}
	sess := link.Session()
	if sess == nil {
		return protocol.NewError(protocol.CodeUnavailable, "terminal.replay: the connection is gone")
	}
	ctx, cancel := context.WithCancel(context.Background())
	ref := &streamRef{cancel}
	s.mu.Lock()
	if old := s.streams[sid]; old != nil {
		old.cancel() // the Citadel asks again (after a reconnection): the new one replaces the old
	}
	s.streams[sid] = ref
	s.mu.Unlock()
	go func() {
		defer func() {
			cancel()
			s.mu.Lock()
			if s.streams[sid] == ref { // a newer stream of the same id stays
				delete(s.streams, sid)
			}
			s.mu.Unlock()
		}()
		out := &linkSink{link: link, sess: sess, id: frameID, terminal: req.TerminalID, stream: req.Stream}
		_ = termstream.Run(ctx, s.src, req.TerminalID, req.From, out)
	}()
	return nil
}

// stop ends a stream.
func (s *terminalServer) stop(stream string) {
	s.mu.Lock()
	ref := s.streams[stream]
	s.mu.Unlock()
	if ref != nil {
		ref.cancel()
	}
}

// linkSink sends a stream over the connection it began on.
type linkSink struct {
	link     frameLink
	sess     *protocol.Session
	id       protocol.ULID
	terminal string
	stream   string
}

func (l *linkSink) Chunk(ctx context.Context, off uint64, data []byte) error {
	if l.link.Session() != l.sess {
		return errStreamGone
	}
	return l.link.SendData(ctx, protocol.DataFrame{Type: protocol.DataTerminal, ID: l.id, Pos: off, Data: data})
}

func (l *linkSink) Replayed(ctx context.Context, from, to uint64, lost bool) error {
	if l.link.Session() != l.sess {
		return errStreamGone
	}
	_, err := l.link.Send(ctx, protocol.Outgoing{Type: protocol.TypeTerminalReplayed, Transient: true,
		Payload: protocol.TerminalReplayed{TerminalID: l.terminal, From: from, To: to, Lost: lost, Stream: l.stream}})
	return err
}

// runtimeSource is the buffers of the terminals of a Runtime.
type runtimeSource struct{ rt *characters.Runtime }

func (r runtimeSource) Replay(ctx context.Context, id string, from uint64) (terminal.Replayed, error) {
	return r.rt.Replay(ctx, id, from)
}

func (r runtimeSource) WaitOutput(ctx context.Context, id string, after uint64) error {
	return r.rt.WaitOutput(ctx, id, after)
}

// registerTowerTerminals puts in the router of a Tower the handlers of
// terminal.replay and terminal.unfollow. Both come only from the Citadel.
func (e *Engine) registerTowerTerminals(router *protocol.Router) {
	var srv *terminalServer
	var once sync.Once
	server := func() *terminalServer {
		once.Do(func() { srv = newTerminalServer(runtimeSource{e.rt.rt}) })
		return srv
	}
	check := func(typ string, m *protocol.Message) error {
		if !e.fromCitadel(m.Peer) {
			return protocol.NewError(protocol.CodeForbidden, "%s: only the Citadel gives this command", typ)
		}
		if e.rt == nil {
			return protocol.NewError(protocol.CodeUnavailable, "%s: %v", typ, errTerminalsNotWired)
		}
		return nil
	}
	router.HandleFunc(protocol.TypeTerminalReplay, func(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
		if err := check(protocol.TypeTerminalReplay, m); err != nil {
			return nil, err
		}
		p, ok := m.Payload.(*protocol.TerminalReplay)
		if !ok {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "terminal.replay: %T", m.Payload)
		}
		if _, known := e.rt.rt.AgentOf(p.TerminalID); !known {
			return nil, protocol.NewError(protocol.CodeNotFound, "terminal.replay: no terminal %s on this machine", p.TerminalID)
		}
		return nil, server().serve(m.Link, *p)
	})
	router.HandleFunc(protocol.TypeTerminalUnfollow, func(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
		if err := check(protocol.TypeTerminalUnfollow, m); err != nil {
			return nil, err
		}
		p, ok := m.Payload.(*protocol.TerminalUnfollow)
		if !ok {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "terminal.unfollow: %T", m.Payload)
		}
		server().stop(p.Stream)
		return nil, nil
	})
}
