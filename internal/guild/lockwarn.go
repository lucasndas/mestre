package guild

import (
	"context"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// DownNotice warns the Owner once when the GitHub goes out and once when it is
// back (docs/guilda/trava-no-github.md#github-fora): it is the OnChange of the
// Fence. The first carries the diagnosis (the last error, since when, how many
// actions wait); the second, how many left the queue.
type DownNotice struct {
	// Tell sends the notice to the Owner (the Telegram and the panel).
	Tell  func(ctx context.Context, m i18n.Msg)
	Clock clock.Clock
	// Location is the time zone of the Guild, where the moment of the notice is said (nil: UTC).
	Location  *time.Location
	Queued    func() int
	LastError func() error

	mu    sync.Mutex
	since time.Time
	down  bool
}

// Changed is the OnChange of the Fence. Tell must not block (it queues the
// message): it runs on the goroutine that checks the lock.
func (n *DownNotice) Changed(st FenceState) {
	if n.Tell == nil || n.Clock == nil {
		return
	}
	queued := func() int {
		if n.Queued == nil {
			return 0
		}
		return n.Queued()
	}
	n.mu.Lock()
	switch {
	case st == FenceDemoted:
		n.down = false // the Citadel stepped down: there is no GitHub fora to show
		n.mu.Unlock()
	case st == FenceLockDown && !n.down:
		n.down, n.since = true, n.Clock.Now()
		since := n.since.UTC()
		if n.Location != nil {
			since = n.since.In(n.Location)
		}
		n.mu.Unlock()
		why := i18n.M("guild.github_down_no_detail")
		if n.LastError != nil && n.LastError() != nil {
			why = i18n.Text(n.LastError().Error()) // the error of a program: not translated
		}
		n.Tell(context.Background(), i18n.M("guild.github_down", "since", since, "why", why, "queued", queued()))
	case st == FenceConfirmed && n.down:
		n.down = false
		n.mu.Unlock()
		n.Tell(context.Background(), i18n.M("guild.github_back", "queued", queued()))
	default:
		n.mu.Unlock()
	}
}

// Down says the notice of "GitHub fora" is on (the panel shows it).
func (n *DownNotice) Down() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.down
}
