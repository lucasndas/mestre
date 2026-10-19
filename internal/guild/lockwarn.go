package guild

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// DownNotice warns the Owner once when the GitHub goes out and once when it is
// back (docs/guilda/trava-no-github.md#github-fora): it is the OnChange of the
// Fence. The first carries the diagnosis (the last error, since when, how many
// actions wait); the second, how many left the queue.
type DownNotice struct {
	// Tell sends the notice to the Owner (the Telegram and the panel).
	Tell      func(ctx context.Context, text string)
	Clock     clock.Clock
	Queued    func() int
	LastError func() error

	mu    sync.Mutex
	since time.Time
	down  bool
}

// Changed is the OnChange of the Fence.
func (n *DownNotice) Changed(st FenceState) {
	n.mu.Lock()
	switch {
	case st == FenceLockDown && !n.down:
		n.down, n.since = true, n.Clock.Now()
		since := n.since
		n.mu.Unlock()
		why := "sem detalhe"
		if n.LastError != nil && n.LastError() != nil {
			why = n.LastError().Error()
		}
		n.Tell(context.Background(), fmt.Sprintf("GitHub fora desde %s. Último erro: %s. O Telegram e o comando seguem; as ações no GitHub ficam na fila (%d até agora).", since.Format("15:04:05"), why, n.Queued()))
	case st == FenceConfirmed && n.down:
		n.down = false
		n.mu.Unlock()
		n.Tell(context.Background(), fmt.Sprintf("GitHub de volta. %d ações saem da fila, na ordem.", n.Queued()))
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
