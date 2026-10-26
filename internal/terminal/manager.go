package terminal

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/privdir"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/terminal/ptywire"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// EventKind tells what an Event is.
type EventKind string

// The events a Manager reports.
const (
	// EventStable: the screen stopped changing for 500 ms.
	EventStable EventKind = "stable"
	// EventExit: the agent ended without being asked to.
	EventExit EventKind = "exit"
	// EventLost: the connection to a live mestre-pty dropped and the
	// process is gone (the terminal is marked interrupted), or it is alive
	// but cannot be reached.
	EventLost EventKind = "lost"
)

// Event is something that happened to a terminal.
type Event struct {
	Terminal string
	Kind     EventKind
	Offset   uint64
	Exit     Exit
	// Interrupted is set on EventLost when the mestre-pty is gone.
	Interrupted bool
}

// ManagerConfig configures a Manager.
type ManagerConfig struct {
	DB      *store.DB
	Clock   clock.Clock
	System  System
	Spawner Spawner
	// Access, when set (Windows), grants the logon SID of each terminal on
	// the socket of the local channel while the terminal lives; a terminal
	// whose grant fails does not start (access.go).
	Access Access
	// MachineID is written to the registry rows of this machine.
	MachineID string
	// SocketDir and LogDir hold <id>.sock and <id>.log of the terminals.
	SocketDir string
	LogDir    string
	// PTYBinary returns the executable of a mestre-pty version: each
	// version lives in its own folder and a terminal keeps the version it
	// was born with (docs/arquitetura/terminal.md#protocolo-motor--mestre-pty).
	PTYBinary func(version string) (string, error)
	// PTYVersion is the version new terminals are created with.
	PTYVersion string
	// Cols and Rows are the fixed size of new terminals (default 200x50).
	Cols, Rows int
	Masker     *logging.Masker
	Logger     *slog.Logger
	// OnEvent receives stable, exit and lost events; it must not block.
	OnEvent func(Event)
	// OnStarting is called once the mestre-pty proved the secret and before
	// the agent starts, so whoever identifies callers (the local channel)
	// already knows the terminal when the agent makes its first call. It
	// must not block. OnDiscarded is called with the id of a terminal that
	// failed to start after OnStarting.
	OnStarting  func(Starting)
	OnDiscarded func(id string)
	// OnEnded is called with the id of a terminal whose agent ended on its
	// own, after the engine swept what the agent left behind and marked the
	// row interrupted; the local channel forgets the terminal here
	// (Directory.Remove). It must not block.
	OnEnded func(id string)
	// HandshakeTimeout (default ptywire.EngineHandshakeTimeout, 35 s) bounds connecting to a mestre-pty.
	HandshakeTimeout time.Duration
	// AckDelay (default 200 ms): see DefaultAckDelay.
	AckDelay time.Duration
}

// Starting is a terminal about to start its agent.
type Starting struct {
	Record Record
	// Client is the connection to its mestre-pty.
	Client *Client
}

// Manager creates the terminals of this machine and, when the engine comes
// back, reconnects to the ones that survived.
type Manager struct {
	cfg ManagerConfig
	clk clock.Clock
	reg registry
	log *slog.Logger
}

// NewManager returns a Manager; it creates the socket and log folders.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.System == nil {
		cfg.System = NewSystem()
	}
	if cfg.Cols == 0 {
		cfg.Cols = ptywire.DefaultCols
	}
	if cfg.Rows == 0 {
		cfg.Rows = ptywire.DefaultRows
	}
	if cfg.AckDelay == 0 {
		cfg.AckDelay = DefaultAckDelay
	}
	if cfg.HandshakeTimeout == 0 {
		cfg.HandshakeTimeout = ptywire.EngineHandshakeTimeout
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	for _, d := range []string{cfg.SocketDir, cfg.LogDir} {
		if err := privdir.Ensure(d); err != nil {
			return nil, err
		}
	}
	return &Manager{cfg: cfg, clk: cfg.Clock, reg: registry{cfg.DB}, log: log}, nil
}

func (m *Manager) logPath(id string) string { return filepath.Join(m.cfg.LogDir, id+".log") }

// CreateSpec describes a terminal to create: the base is already resolved
// (executable, arguments, environment, folder) by the adapter.
type CreateSpec struct {
	ID                string
	CharacterID       string
	Dir               string
	Class             string
	ProviderSessionID string
	// Command is what runs in the terminal.
	Path string
	Args []string
	Env  []string
	// WorkDir is the folder of the process; Dir (above) is the character's.
	WorkDir string
}

// Create makes a terminal in the order the document requires (1) the row
// in terminals, with socket, secret and version; (2) the mestre-pty starts;
// (3) its PID and start identity are recorded; (4) the handshake; (5) the
// agent starts. The agent never runs without its row.
func (m *Manager) Create(ctx context.Context, spec CreateSpec) (*Terminal, error) {
	sock, err := SocketPath(m.cfg.SocketDir, spec.ID)
	if err != nil {
		return nil, err
	}
	bin, err := m.cfg.PTYBinary(m.cfg.PTYVersion)
	if err != nil {
		return nil, fmt.Errorf("mestre-pty %s: %w", m.cfg.PTYVersion, err)
	}
	secret := make([]byte, ptywire.SecretSize)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("secret: %w", err)
	}
	now := m.clk.Now()
	rec := Record{
		ID: spec.ID, CharacterID: spec.CharacterID, MachineID: m.cfg.MachineID, Dir: spec.Dir, Class: spec.Class,
		Status: StatusStarting, ProviderSessionID: spec.ProviderSessionID, SocketPath: sock, Secret: secret,
		PTYVersion: m.cfg.PTYVersion, StartedAt: now, LastSeenAt: now,
	}
	if err := m.reg.insert(ctx, rec); err != nil { // 1
		return nil, err
	}
	sp, err := m.cfg.Spawner.Spawn(ctx, SpawnSpec{ // 2
		ID: spec.ID, Version: m.cfg.PTYVersion, Binary: bin, SocketPath: sock, Secret: secret,
		LogPath: filepath.Join(m.cfg.LogDir, spec.ID+".pty.log"),
	})
	if err != nil {
		m.discard(rec, ProcID{})
		return nil, err
	}
	rec.PTY = sp.ProcID
	started := false
	// The grant of the local channel comes before anything of the agent
	// runs, and a failure ends the terminal (fail closed).
	grantErr := m.grantAccess(ctx, &rec, sp.LogonSID)
	abort := func(err error) (*Terminal, error) {
		if started && m.cfg.OnDiscarded != nil {
			m.cfg.OnDiscarded(rec.ID)
		}
		m.discard(rec, sp.ProcID)
		return nil, err
	}
	if grantErr != nil {
		return abort(grantErr)
	}
	if err := m.reg.setProcess(ctx, rec.ID, sp.ProcID); err != nil { // 3
		return abort(err)
	}
	c, err := m.connect(ctx, rec) // 4
	if err != nil {
		return abort(err)
	}
	if f := m.cfg.OnStarting; f != nil {
		f(Starting{Record: rec, Client: c})
		started = true
	}
	agent, err := c.Start(ctx, ptywire.Msg{ // 5
		Path: spec.Path, Args: spec.Args, Env: spec.Env, Dir: spec.WorkDir, Cols: m.cfg.Cols, Rows: m.cfg.Rows,
	})
	if err != nil {
		_ = c.Close()
		return abort(fmt.Errorf("start the agent of terminal %s: %w", spec.ID, err))
	}
	t, err := m.newTerminal(rec, c, sp.ProcID, agent)
	if err != nil {
		_ = c.Close()
		return abort(err)
	}
	if err := t.attach(ctx); err != nil {
		t.Detach()
		return abort(err)
	}
	return t, nil
}

// discard undoes a terminal that failed to start: nothing of it stays, not
// even the row.
func (m *Manager) discard(rec Record, p ProcID) {
	ctx := context.Background()
	if p.PID != 0 {
		_ = m.killTree(ctx, rec.ID, p, nil)
	}
	_ = m.cfg.Spawner.KillCgroup(ctx, rec.ID)
	m.revokeAccess(rec.ID, rec.LogonSID)
	_ = os.Remove(rec.SocketPath)
	_ = m.reg.delete(ctx, rec.ID)
}

// connect dials the socket of rec and runs the handshake. It accepts the
// mestre-pty only if it proves the secret and the PID on the other end of
// the socket is the one recorded with the same start identity (when rec has
// no PID yet, as after a crash between starting the mestre-pty and recording
// it, the PID is taken from the socket and recorded).
func (m *Manager) connect(ctx context.Context, rec Record) (*Client, error) {
	c, err := Dial(ctx, m.clk, rec.SocketPath, rec.Secret, m.cfg.HandshakeTimeout)
	if err != nil {
		m.audit("terminal connection refused", rec.ID, err)
		return nil, err
	}
	peer, err := m.cfg.System.PeerPID(c.Net())
	if err == nil && c.Info().PID != peer {
		err = fmt.Errorf("mestre-pty says it is PID %d, the socket says %d", c.Info().PID, peer)
	}
	if err == nil && rec.PTY.PID != 0 && rec.PTY.PID != peer {
		err = fmt.Errorf("the socket is held by PID %d, the registry says %d", peer, rec.PTY.PID)
	}
	if err == nil && rec.PTY.PID != 0 && !Alive(m.cfg.System, rec.PTY) {
		err = fmt.Errorf("PID %d is not the process recorded for the mestre-pty", rec.PTY.PID)
	}
	if err != nil {
		_ = c.Close()
		m.audit("terminal peer refused", rec.ID, err)
		return nil, err
	}
	return c, nil
}

// audit records a refused connection; the audit trail of the machine reads
// these entries.
func (m *Manager) audit(msg, id string, err error) {
	m.log.Warn(msg, "terminal", id, "error", err.Error())
}

// connectionLost is called when the connection of a live terminal drops
// without the engine having asked for it.
func (m *Manager) connectionLost(t *Terminal) {
	ctx := context.Background()
	if t.client != nil { // a connection that fell for a violation of the protocol says which
		if err := t.client.Err(); err != nil {
			m.log.Warn("terminal connection lost", "terminal", t.id, "error", err.Error())
		}
	}
	gone := !Alive(m.cfg.System, t.proc)
	if gone {
		_ = m.cfg.Spawner.KillCgroup(ctx, t.id)
		m.revokeAccess(t.id, t.rec.LogonSID)
		_ = m.reg.setStatus(ctx, t.id, StatusInterrupted, m.clk.Now())
	}
	if f := m.cfg.OnEvent; f != nil {
		f(Event{Terminal: t.id, Kind: EventLost, Interrupted: gone})
	}
}

// Terminal returns the registry row of a terminal.
func (m *Manager) Record(ctx context.Context, id string) (Record, error) {
	rec, err := m.reg.get(ctx, id)
	if errors.Is(err, ErrNoTerminal) {
		return Record{}, fmt.Errorf("terminal %s: %w", id, err)
	}
	return rec, err
}

// SetStatus keeps in the registry row the state the character of the
// terminal is in (docs/orquestracao/personagens.md#estados): the registry
// writes only starting, interrupted and stopped itself, the rest of the
// states are the layer above's. A terminal that is not in the registry is
// ErrNoTerminal.
func (m *Manager) SetStatus(ctx context.Context, id, status string) error {
	if _, err := m.reg.get(ctx, id); err != nil {
		if errors.Is(err, ErrNoTerminal) {
			return fmt.Errorf("terminal %s: %w", id, err)
		}
		return err
	}
	return m.reg.setStatus(ctx, id, status, m.clk.Now())
}

// Prune deletes the rows of the terminals whose id starts with prefix and that
// are not live (stopped or interrupted), and says how many. A live terminal is
// never touched.
func (m *Manager) Prune(ctx context.Context, prefix string) (int, error) {
	rows, err := m.reg.list(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		if !strings.HasPrefix(r.ID, prefix) || r.Live() {
			continue
		}
		if err := m.reg.delete(ctx, r.ID); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// Records lists the registry rows.
func (m *Manager) Records(ctx context.Context) ([]Record, error) { return m.reg.list(ctx) }
