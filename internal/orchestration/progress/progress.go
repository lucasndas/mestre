// Package progress makes the message of progress of a workspace
// (docs/orquestracao/grao-mestre.md#mensagem-de-progresso): built by the
// engine, never by an agent, at the end of each mission and every 2 hours while
// a table is working. Counting for what needs nobody, and one line with an owner
// for each thing that needs somebody.
package progress

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/pkg/clock"
)

// DefaultEvery is how often the message goes while a table is working.
const DefaultEvery = 2 * time.Hour

// Tables gives the tables of a workspace (tables.Store).
type Tables interface {
	List(ctx context.Context, workspace string) ([]tables.Table, error)
}

// Requests gives the approval requests that wait on a table (chain.Store).
type Requests interface {
	OpenOfTable(ctx context.Context, workspace, table string) ([]chain.Request, error)
}

// Workspaces lists the active workspaces.
type Workspaces interface {
	Active(ctx context.Context) ([]string, error)
}

// Recipients says who the message of a workspace goes to.
type Recipients interface {
	Recipients(ctx context.Context, workspace string) ([]string, error)
}

// Sender delivers the text to a person (the bot; the panel shows it too).
type Sender interface {
	Send(ctx context.Context, userID, workspace, text string) error
}

// Options are the pieces of the Service; all are required but Every, Mask and
// Logger.
type Options struct {
	Clock      clock.Clock
	Tables     Tables
	Requests   Requests
	Workspaces Workspaces
	Recipients Recipients
	Sender     Sender
	Every      time.Duration
	// Mask hides secrets in what an agent or a user wrote (default: none).
	Mask   func(string) string
	Logger *slog.Logger
}

// Service makes and sends the messages.
type Service struct {
	o   Options
	clk clock.Clock
	log *slog.Logger

	mu   sync.Mutex
	last map[string]time.Time
}

// New returns the Service.
func New(o Options) (*Service, error) {
	if o.Clock == nil || o.Tables == nil || o.Requests == nil || o.Workspaces == nil || o.Recipients == nil || o.Sender == nil {
		return nil, fmt.Errorf("progress: the clock, the tables, the requests, the workspaces, the recipients and the sender are required")
	}
	if o.Every <= 0 {
		o.Every = DefaultEvery
	}
	if o.Mask == nil {
		o.Mask = func(s string) string { return s }
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Service{o: o, clk: o.Clock, log: o.Logger, last: map[string]time.Time{}}, nil
}

// Message is a message of progress.
type Message struct {
	Text string
	// Tables is how many tables the workspace has that are not over, and
	// Working how many of them are working.
	Tables, Working int
}

func title(t tables.Table) string {
	if t.EpicTitle != "" {
		return t.EpicTitle
	}
	return t.ID
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// owner is who a level is, as the person reads it.
func owner(l chain.Level) string {
	switch l.Kind {
	case chain.LevelPeople:
		return "você"
	case chain.LevelGrandMaster:
		return "o Grão-Mestre"
	case chain.LevelMesa:
		return "o Mestre da mesa"
	}
	return "alguém do nível " + l.Name
}

// Compose makes the message of a workspace from what the engine confirmed: a
// table counts as merged only when the engine marked it (the merge commit is
// in the mirror, [CA-GH-EV-08]); one whose pull request was merged on GitHub
// without that stays where it is. Nothing to say, no table: ok is false.
func (s *Service) Compose(ctx context.Context, workspace string) (Message, bool, error) {
	ts, err := s.o.Tables.List(ctx, workspace)
	if err != nil {
		return Message{}, false, err
	}
	var open []tables.Table
	for _, t := range ts {
		if !tables.Over(t.State) {
			open = append(open, t)
		}
	}
	if len(open) == 0 {
		return Message{}, false, nil
	}
	var merged, going, lines []string
	var next string
	working := 0
	for _, t := range open {
		if t.State == characters.TableWorking {
			working++
		}
		if t.Merged {
			merged = append(merged, title(t))
			continue
		}
		reqs, err := s.o.Requests.OpenOfTable(ctx, workspace, t.ID)
		if err != nil {
			return Message{}, false, err
		}
		waiting := false
		for _, r := range reqs {
			who := owner(r.Level)
			what := oneLine(r.About, 160)
			verb := "decidir"
			if r.PR != nil {
				what, verb = fmt.Sprintf("aprovar PR #%d", r.PR.Number), "aprovar"
				if len(r.Why) > 0 {
					what += " (" + oneLine(r.Why[0], 120) + ")"
				}
			}
			lines = append(lines, fmt.Sprintf("  🙋 %s: espera %s — %s", title(t), who, s.o.Mask(what)))
			if next == "" && r.Level.Kind == chain.LevelPeople {
				next = fmt.Sprintf("Próximo: %s volta ao trabalho quando você %s", title(t), verb)
			}
			waiting = true
		}
		switch {
		case t.Paused:
			lines = append(lines, fmt.Sprintf("  ⏸ %s: pausada — %s", title(t), s.o.Mask(oneLine(t.PausedReason, 160))))
		case !waiting:
			going = append(going, title(t))
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %d %s", workspace, len(open), plural(len(open), "mesa", "mesas"))
	if len(merged) > 0 {
		fmt.Fprintf(&b, "\n  ✅ %d %s (%s)", len(merged), plural(len(merged), "mesclada", "mescladas"), strings.Join(merged, ", "))
	}
	if len(going) > 0 {
		fmt.Fprintf(&b, "\n  🔧 %d em andamento (%s)", len(going), strings.Join(going, ", "))
	}
	for _, l := range lines {
		b.WriteString("\n" + l)
	}
	if next != "" {
		b.WriteString("\n" + next)
	}
	return Message{Text: b.String(), Tables: len(open), Working: working}, true, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Send makes the message of the workspace and sends it to the people it is
// for. It counts as the last one sent, so the 2 hours start again.
func (s *Service) Send(ctx context.Context, workspace string) error {
	m, ok, err := s.Compose(ctx, workspace)
	if err != nil || !ok {
		return err
	}
	to, err := s.o.Recipients.Recipients(ctx, workspace)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.last[workspace] = s.clk.Now()
	s.mu.Unlock()
	var first error
	for _, u := range to {
		if err := s.o.Sender.Send(ctx, u, workspace, m.Text); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// MissionEnded is told when a mission of a workspace ended: the message goes
// now.
func (s *Service) MissionEnded(ctx context.Context, workspace string) {
	if err := s.Send(ctx, workspace); err != nil {
		s.log.Warn("progress: the message of the end of a mission was not sent", "workspace", workspace, "err", err)
	}
}

// Tick sends the message of each workspace that has a table working and did
// not have one in the last Every.
func (s *Service) Tick(ctx context.Context) error {
	ws, err := s.o.Workspaces.Active(ctx)
	if err != nil {
		return err
	}
	now := s.clk.Now()
	for _, w := range ws {
		s.mu.Lock()
		last, seen := s.last[w]
		if !seen { // the first look starts the 2 hours
			s.last[w] = now
		}
		s.mu.Unlock()
		if !seen || now.Sub(last) < s.o.Every {
			continue
		}
		m, ok, err := s.Compose(ctx, w)
		if err != nil {
			s.log.Warn("progress: the message was not made", "workspace", w, "err", err)
			continue
		}
		if !ok || m.Working == 0 {
			continue
		}
		if err := s.Send(ctx, w); err != nil {
			s.log.Warn("progress: the message was not sent", "workspace", w, "err", err)
		}
	}
	return nil
}

// Loop calls Tick every minute of the clock until the context ends.
func (s *Service) Loop(ctx context.Context, every time.Duration) error {
	t := s.clk.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C():
			if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("progress: a tick failed", "err", err)
			}
		}
	}
}
