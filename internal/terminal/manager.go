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
	"time"

	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/pkg/clock"
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
	// HandshakeTimeout (default 5 s) bounds connecting to a mestre-pty.
	HandshakeTimeout time.Duration
	// AckDelay (default 200 ms): see DefaultAckDelay.
	AckDelay time.Duration
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
		cfg.HandshakeTimeout = ptywire.HandshakeTimeout
	}
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	for _, d := range []string{cfg.SocketDir, cfg.LogDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, fmt.Errorf("create %s: %w", d, err)
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
	abort := func(err error) (*Terminal, error) {
		m.discard(rec, sp.ProcID)
		return nil, err
	}
	if err := m.reg.setProcess(ctx, rec.ID, sp.ProcID); err != nil { // 3
		return abort(err)
	}
	c, err := m.connect(ctx, rec) // 4
	if err != nil {
		return abort(err)
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
		_ = m.cfg.System.KillTree(ctx, p, nil)
	}
	_ = m.cfg.Spawner.KillCgroup(ctx, rec.ID)
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
	gone := !Alive(m.cfg.System, t.proc)
	if gone {
		_ = m.cfg.Spawner.KillCgroup(ctx, t.id)
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

// Records lists the registry rows.
func (m *Manager) Records(ctx context.Context) ([]Record, error) { return m.reg.list(ctx) }
