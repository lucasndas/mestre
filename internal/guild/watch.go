package guild

import (
	"sync"
	"time"

	"github.com/mestre-ia/mestre/pkg/clock"
)

// The notice to the Owner when the Citadel does not come back
// (docs/guilda/cidadela-e-torres.md#recuperação-e-aviso-ao-dono).
const (
	// CitadelDownNotice is how long the Citadel may be away before the Owner
	// is warned (configurable by the Owner).
	CitadelDownNotice = 15 * time.Minute
	// CitadelDownRepeat is how often the warning repeats while it stays away.
	CitadelDownRepeat = time.Hour
)

// DownWatchConfig wires a DownWatch.
type DownWatchConfig struct {
	Clock clock.Clock
	// After and Repeat default to CitadelDownNotice and CitadelDownRepeat.
	After, Repeat time.Duration
	// OnDown is the notice: the Citadel is away since since; n counts the
	// notices (1 the first). The bot in reduced mode and the fallback page
	// show it with the option to promote (milestones 5 and 7).
	OnDown func(since time.Time, n int)
	// OnBack is told the Citadel came back after a notice: the request of
	// promotion is cancelled.
	OnBack func(since time.Time)
}

// DownWatch times how long a Tower has been without its Citadel and warns
// the Owner. It is the detection of the conducting Tower; who conducts is
// the election of milestone 7.
type DownWatch struct {
	cfg DownWatchConfig

	mu    sync.Mutex
	since time.Time
	down  bool
	n     int
	timer clock.Timer
}

// NewDownWatch returns the watch.
func NewDownWatch(cfg DownWatchConfig) *DownWatch {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.After <= 0 {
		cfg.After = CitadelDownNotice
	}
	if cfg.Repeat <= 0 {
		cfg.Repeat = CitadelDownRepeat
	}
	return &DownWatch{cfg: cfg}
}

// Lost is called when the connection to the Citadel ends; it starts the time
// once.
func (w *DownWatch) Lost() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.down {
		return
	}
	w.down, w.since, w.n = true, w.cfg.Clock.Now(), 0
	w.timer = w.cfg.Clock.AfterFunc(w.cfg.After, w.fire)
}

func (w *DownWatch) fire() {
	w.mu.Lock()
	if !w.down {
		w.mu.Unlock()
		return
	}
	w.n++
	n, since := w.n, w.since
	w.mu.Unlock()
	if w.cfg.OnDown != nil {
		w.cfg.OnDown(since, n)
	}
	// The next notice is armed after this one was given, so they never
	// overlap.
	w.mu.Lock()
	if w.down && w.since.Equal(since) {
		w.timer = w.cfg.Clock.AfterFunc(w.cfg.Repeat, w.fire)
	}
	w.mu.Unlock()
}

// Back is called when a Citadel (the same or a new one) answers again.
func (w *DownWatch) Back() {
	w.mu.Lock()
	if !w.down {
		w.mu.Unlock()
		return
	}
	w.down = false
	if w.timer != nil {
		w.timer.Stop()
	}
	notified, since := w.n > 0, w.since
	w.mu.Unlock()
	if notified && w.cfg.OnBack != nil {
		w.cfg.OnBack(since)
	}
}

// Since says since when the Citadel is away, and whether it is.
func (w *DownWatch) Since() (time.Time, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.since, w.down
}
