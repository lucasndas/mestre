package conformance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/privdir"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/store"
	machinedb "github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/pkg/clock"
)

// HostConfig configures the engine side of a run.
type HostConfig struct {
	// Root is a short folder this process owns (a Unix socket path holds
	// about 100 bytes): the machine database, the sockets and the logs of
	// the terminals go in it.
	Root string
	// PTYBinary is the mestre-pty and PTYVersion its version.
	PTYBinary, PTYVersion string
	// Suite is MESTRE_TEST_SUITE: the fast suite starts no process.
	Suite string
	// CgroupBase is a delegated cgroup v2 folder for the terminals (Linux);
	// empty identifies the callers by the process tree.
	CgroupBase string
	// Layout are the folders of the Mestre the fixed rules close to the
	// agents (classes.DefaultLayout of the instance).
	Layout classes.Layout
	Clock  clock.Clock
	Logger *slog.Logger
}

// Host is the engine side of the conformance: terminals, the local channel
// and the hook engine with the tap.
type Host struct {
	cfg     HostConfig
	clk     clock.Clock
	log     *slog.Logger
	db      *store.DB
	sys     terminal.System
	spawner *terminal.DirectSpawner
	mgr     *terminal.Manager
	dir     *localchan.Directory
	sock    string
	// Rules are the effective rules of the characters of the runs.
	Rules *classes.Store
	tap   *tap

	stopServe func()
	served    chan struct{}

	mu       sync.Mutex
	chars    map[string]classes.Env
	sessions map[string]*Session
	contexts map[string]string
}

// NewHost opens the engine side in cfg.Root and starts serving the local
// channel; Close ends it and every terminal still running.
func NewHost(ctx context.Context, cfg HostConfig) (*Host, error) {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	h := &Host{cfg: cfg, clk: cfg.Clock, log: log, sys: terminal.NewSystem(), sock: filepath.Join(cfg.Root, "cli.sock"),
		chars: map[string]classes.Env{}, sessions: map[string]*Session{}, contexts: map[string]string{}}
	if err := privdir.Ensure(cfg.Root); err != nil {
		return nil, err
	}
	db, err := machinedb.Open(ctx, filepath.Join(cfg.Root, "maquina.db"), store.Options{})
	if err != nil {
		return nil, err
	}
	h.db = db
	h.spawner = &terminal.DirectSpawner{Suite: cfg.Suite, System: h.sys, CgroupBase: cfg.CgroupBase}
	h.dir = localchan.NewDirectory(h.sys)
	h.mgr, err = terminal.NewManager(terminal.ManagerConfig{
		DB: db, Clock: h.clk, System: h.sys, Spawner: h.spawner, MachineID: "conformidade",
		SocketDir: filepath.Join(cfg.Root, "s"), LogDir: filepath.Join(cfg.Root, "l"),
		PTYBinary:  func(string) (string, error) { return cfg.PTYBinary, nil },
		PTYVersion: cfg.PTYVersion, Logger: log,
		OnEvent:    h.onTerminal,
		OnStarting: h.dir.Starting, OnDiscarded: h.dir.Remove, OnEnded: h.dir.Remove,
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	h.Rules = &classes.Store{DB: db, Clock: h.clk, Agents: filepath.Join(cfg.Root, "agentes")}
	engine := &hook.Engine{Clock: h.clk, Rules: h.Rules, Ledger: h.Rules, Agents: h.Rules.Agents, Characters: h, Contexts: h, Logger: log}
	h.tap = newTap(h.clk, engine)
	h.tap.degraded = h.degradedDecision
	mode := localchan.ModeTree
	if cfg.CgroupBase != "" {
		mode = localchan.ModeCgroup
	}
	srv := localchan.NewServer(localchan.Config{
		Clock: h.clk,
		Identifier: localchan.NewSystemIdentifier(localchan.SystemConfig{
			Terminals: h.dir, Jobs: h.dir, System: h.sys, Mode: mode, CgroupBase: cfg.CgroupBase,
		}),
		Local:    map[string]localchan.Handler{localchan.CmdHook: h.tap},
		Instance: "conformidade", EngineVersion: "conformidade",
		OnAudit: h.tap.audit,
		Logger:  log,
	})
	ln, err := localchan.Listen(h.sock, localchan.NoAccess{})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	sctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	h.stopServe, h.served = cancel, make(chan struct{})
	go func() {
		defer close(h.served)
		_ = srv.Serve(sctx, ln)
	}()
	return h, nil
}

// Endpoint is the MESTRE_ENDPOINT of the characters of this host.
func (h *Host) Endpoint() string { return localchan.EndpointURL(h.sock) }

// Events are the hook calls the host received.
func (h *Host) Events() *Log { return h.tap.log }

// Pause makes the engine stop answering the hooks of a character (on) or
// answer again (off): step 6, the degraded mode.
func (h *Host) Pause(agentID string, on bool) { h.tap.pause(agentID, on) }

// degradedDecision is the decision `mestre hook` takes for a character
// without the engine: the snapshot the engine wrote for it and the fixed
// rules (the class of the run has no blocking hooks).
func (h *Host) degradedDecision(ctx context.Context, ev providers.Event) classes.Decision {
	env, err := h.Env(ctx, ev.AgentID)
	if err != nil || !classes.ValidAgentID(ev.AgentID) {
		return classes.Decision{Verdict: classes.Deny}
	}
	env.Layout = h.cfg.Layout
	var snap *classes.Rules
	if b, err := os.ReadFile(filepath.Join(h.Rules.Agents, ev.AgentID, classes.SnapshotName)); err == nil {
		snap, _ = classes.ParseRules(b)
	}
	return classes.DecideDegraded(ctx, env, snap, ev)
}

// Env implements hook.Characters: the characters of the runs.
func (h *Host) Env(_ context.Context, agentID string) (classes.Env, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	env, ok := h.chars[agentID]
	if !ok {
		return classes.Env{}, fmt.Errorf("conformance host: unknown character %q", agentID)
	}
	return env, nil
}

// SetContext gives the text the engine answers at the first turn_start
// (or session_start) of a character: the Mestre layer when it travels by
// the hook (providers.LayerHook).
func (h *Host) SetContext(agentID, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.contexts[agentID] = text
}

// Context implements hook.Contexts: the layer of SetContext, once.
func (h *Host) Context(_ context.Context, ev providers.Event) (string, error) {
	if ev.Event != providers.EventSessionStart && ev.Event != providers.EventTurnStart {
		return "", nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	text := h.contexts[ev.AgentID]
	delete(h.contexts, ev.AgentID)
	return text, nil
}

// Close ends the terminals still running and the local channel.
func (h *Host) Close() {
	h.mu.Lock()
	ss := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		ss = append(ss, s)
	}
	h.mu.Unlock()
	for _, s := range ss {
		_ = s.Stop(context.Background())
	}
	h.stopServe()
	<-h.served
	_ = h.db.Close()
}

func (h *Host) onTerminal(ev terminal.Event) {
	h.mu.Lock()
	s := h.sessions[ev.Terminal]
	h.mu.Unlock()
	if s == nil {
		return
	}
	switch ev.Kind {
	case terminal.EventStable:
		select {
		case s.stable <- struct{}{}:
		default:
		}
	case terminal.EventExit, terminal.EventLost:
		s.endOnce.Do(func() { close(s.ended) })
	}
}

// StartSpec is one terminal of a character.
type StartSpec struct {
	// ID is the terminal; AgentID the character.
	ID, AgentID string
	Launch      providers.Launch
	// Env is the environment the permission pipeline decides with.
	Env classes.Env
}

// Start creates the terminal of a character and follows its raw output.
func (h *Host) Start(ctx context.Context, spec StartSpec) (*Session, error) {
	h.mu.Lock()
	h.chars[spec.AgentID] = spec.Env
	s := &Session{host: h, id: spec.ID, agent: spec.AgentID, stable: make(chan struct{}, 1), ended: make(chan struct{})}
	h.sessions[spec.ID] = s
	h.mu.Unlock()
	if spec.Launch.Verify != nil {
		if err := spec.Launch.Verify(); err != nil {
			h.forget(spec.ID)
			return nil, err
		}
	}
	term, err := h.mgr.Create(ctx, terminal.CreateSpec{
		ID: spec.ID, CharacterID: spec.AgentID, Dir: spec.Env.Worktree, Class: "conformidade",
		Path: spec.Launch.Path, Args: spec.Launch.Args, Env: spec.Launch.Env, WorkDir: spec.Launch.Dir,
	})
	if err != nil {
		h.forget(spec.ID)
		return nil, err
	}
	s.term = term
	rec, err := h.mgr.Record(ctx, spec.ID)
	if err == nil {
		s.follow, err = terminal.Dial(ctx, h.clk, rec.SocketPath, rec.Secret, 0)
	}
	if err == nil {
		_, err = s.follow.Follow(ctx, 0, func(f ptywire.Frame) { s.raw.write(f.Data) })
	}
	if err != nil {
		_ = s.Stop(ctx)
		return nil, fmt.Errorf("follow terminal %s: %w", spec.ID, err)
	}
	go func() {
		select {
		case <-term.Exited():
			s.endOnce.Do(func() { close(s.ended) })
		case <-s.ended:
		}
	}()
	return s, nil
}

func (h *Host) forget(id string) {
	h.mu.Lock()
	delete(h.sessions, id)
	h.mu.Unlock()
}

// Session is the terminal of a character.
type Session struct {
	host   *Host
	id     string
	agent  string
	term   *terminal.Terminal
	follow *terminal.Client
	raw    rawBuffer
	stable chan struct{}

	ended   chan struct{}
	endOnce sync.Once
	stopped sync.Once
}

// ID is the terminal.
func (s *Session) ID() string { return s.id }

// Input types into the terminal (implements providers.Keyboard).
func (s *Session) Input(ctx context.Context, data []byte) error { return s.term.Input(ctx, data) }

// sessionReader gives the supervision the screen as it is now.
type sessionReader struct{ s *Session }

// ReadScreen implements providers.ScreenReader.
func (r sessionReader) ReadScreen(ctx context.Context) (string, error) { return r.s.Screen(ctx) }

// Screen is the rendered screen.
func (s *Session) Screen(ctx context.Context) (string, error) {
	sc, err := s.term.Screen(ctx, 0)
	return sc.Text, err
}

// Stable signals each stable screen.
func (s *Session) Stable() <-chan struct{} { return s.stable }

// Ended is closed when the agent ended.
func (s *Session) Ended() <-chan struct{} { return s.ended }

// Raw is the output of the terminal from its start (at most rawMax bytes).
func (s *Session) Raw() []byte { return s.raw.bytes() }

// Stop ends the terminal: the signal, then the forced kill of the tree.
func (s *Session) Stop(ctx context.Context) error {
	var err error
	s.stopped.Do(func() {
		if s.follow != nil {
			_ = s.follow.Close()
		}
		if s.term != nil {
			_, err = s.term.Stop(ctx, terminal.StopPlan{TerminateWait: 5 * time.Second})
			s.term.Detach()
			s.host.dir.Remove(s.id)
		}
		s.host.forget(s.id)
		s.endOnce.Do(func() { close(s.ended) })
	})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// rawMax bounds the raw output a session keeps.
const rawMax = 4 << 20

type rawBuffer struct {
	mu sync.Mutex
	b  []byte
}

func (r *rawBuffer) write(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.b)+len(p) <= rawMax {
		r.b = append(r.b, p...)
	}
}

func (r *rawBuffer) bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.b...)
}
