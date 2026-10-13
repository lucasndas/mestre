package machinesheet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Defaults of the refresh (docs/guilda/ficha-da-maquina.md#atualização-sozinha).
const (
	// DefaultInterval is how often the engine checks the fields.
	DefaultInterval = 5 * time.Minute
	// DefaultDiskChange is the part of the free disk that has to change
	// before the change is worth a machine.sheet.
	DefaultDiskChange = 0.10
)

// Sender is the link to the Citadel: *protocol.Link.
type Sender interface {
	Send(ctx context.Context, o protocol.Outgoing) (string, error)
}

// AgentConfig configures the Tower side.
type AgentConfig struct {
	Builder *Builder
	Clock   clock.Clock
	// Interval is the period of the check (DefaultInterval).
	Interval time.Duration
	// DiskChange is the fraction of the free disk below which a difference
	// is not reported (DefaultDiskChange). The disk changes all the time;
	// the sheet only needs to tell the order of magnitude.
	DiskChange float64
	// Store, when set, keeps the last sheet across restarts (see SheetStore).
	Store  SheetStore
	Logger *slog.Logger
}

// Agent keeps the sheet of this machine and tells the Citadel what changes.
type Agent struct {
	cfg  AgentConfig
	poke chan struct{}

	mu     sync.Mutex
	sender Sender
	last   Sheet // what the Citadel was told
	primed bool  // last was handed to the Citadel (hello or a first patch)
}

// NewAgent returns the agent. Attach the link before Run.
func NewAgent(cfg AgentConfig) (*Agent, error) {
	if cfg.Builder == nil || cfg.Clock == nil {
		return nil, errors.New("machinesheet: the agent needs a builder and a clock")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.DiskChange <= 0 {
		cfg.DiskChange = DefaultDiskChange
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	a := &Agent{cfg: cfg, poke: make(chan struct{}, 1)}
	if cfg.Store != nil {
		switch s, ok, err := cfg.Store.Load(context.Background()); {
		case err != nil:
			cfg.Logger.Warn("machinesheet: reading the sheet of the last run", "err", err)
		case ok:
			// Not primed: this process has told the Citadel nothing yet. The
			// sheet is where the probes that fail start from, and the revision
			// goes on from its.
			a.last = s
		}
	}
	return a, nil
}

// remember keeps the sheet for the next run. Called with the lock held.
func (a *Agent) remember(s Sheet) {
	if a.cfg.Store == nil {
		return
	}
	if err := a.cfg.Store.Save(context.Background(), s); err != nil {
		a.cfg.Logger.Warn("machinesheet: keeping the sheet for the next run", "err", err)
	}
}

// Attach sets the link machine.sheet goes through. The link exists only after
// the protocol client is built, and the client needs Hello first.
func (a *Agent) Attach(s Sender) {
	a.mu.Lock()
	a.sender = s
	a.mu.Unlock()
}

// Current is the sheet the Citadel was last told.
func (a *Agent) Current() Sheet {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last.Clone()
}

// Hello builds the sheet for the opening of a connection: the whole sheet,
// with a revision above every patch sent before, so patches that still wait
// for their ack cannot undo it. It is the `Sheet` of protocol.HelloInfo.
func (a *Agent) Hello(ctx context.Context) (protocol.Object, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur, err := a.build(ctx)
	if err != nil {
		return nil, err
	}
	cur.Rev = a.nextRev()
	a.last, a.primed = cur, true
	a.remember(cur)
	return withoutGuildFields(cur.Object()), nil
}

// Poke asks for a check now: a login made or lost, a CLI updated, the
// conformance redone, the Operator enabled or ready, the whisper-server that
// failed, a round of the checklist. It never blocks.
func (a *Agent) Poke() {
	select {
	case a.poke <- struct{}{}:
	default:
	}
}

// Run checks the sheet on every poke and every Interval until ctx ends.
func (a *Agent) Run(ctx context.Context) error {
	tk := a.cfg.Clock.NewTicker(a.cfg.Interval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tk.C():
		case <-a.poke:
		}
		if err := a.Refresh(ctx); err != nil && ctx.Err() == nil {
			a.cfg.Logger.Warn("machinesheet: refresh failed", "err", err)
		}
	}
}

// Refresh reads the probes and, if a field changed, sends machine.sheet with
// those fields only. Nothing changed, nothing is sent. A failed send leaves
// the sheet as the Citadel knew it, so the next check tries again.
func (a *Agent) Refresh(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur, err := a.build(ctx)
	if err != nil {
		return err
	}
	if a.sender == nil {
		return errors.New("machinesheet: no link to the Citadel attached")
	}
	var patch protocol.Object
	if a.primed {
		var changed bool
		if patch, changed = Diff(a.last, cur); !changed {
			return nil
		}
	} else {
		patch = withoutGuildFields(cur.Object())
	}
	cur.Rev = a.nextRev()
	patch["rev"] = cur.Object()["rev"]
	if _, err := a.sender.Send(ctx, protocol.Outgoing{Type: protocol.TypeMachineSheet, Payload: patch}); err != nil {
		return fmt.Errorf("machinesheet: send the sheet: %w", err)
	}
	a.last, a.primed = cur, true
	a.remember(cur)
	return nil
}

// build reads the probes, keeping the part a probe could not read and the
// free disk when it moved little. Failures of a probe are only logged.
func (a *Agent) build(ctx context.Context) (Sheet, error) {
	cur, err := a.cfg.Builder.Build(ctx, a.last)
	if cur.OS == "" {
		return Sheet{}, err
	}
	if err != nil {
		a.cfg.Logger.Warn("machinesheet: a probe failed", "err", err)
	}
	if a.primed {
		old, now := float64(a.last.Resources.DiskFreeBytes), float64(cur.Resources.DiskFreeBytes)
		if old > 0 && abs(now-old) <= old*a.cfg.DiskChange {
			cur.Resources.DiskFreeBytes = a.last.Resources.DiskFreeBytes
		}
	}
	cur.Rev = a.last.Rev
	return cur, nil
}

// nextRev is above the last revision and follows the clock. The last revision
// comes first, and survives a restart in the Store: a clock that went back (a
// correction of the time, a machine restored) does not make the patches look
// older than the sheet the Citadel holds.
func (a *Agent) nextRev() int64 {
	return max(a.last.Rev+1, a.cfg.Clock.Now().UnixMilli())
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func withoutGuildFields(o protocol.Object) protocol.Object {
	for _, k := range guildOwned {
		delete(o, k)
	}
	return o
}
