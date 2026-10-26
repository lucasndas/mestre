// Package progress makes the message of progress of a workspace
// (docs/orquestracao/grao-mestre.md#mensagem-de-progresso): built by the
// engine, never by an agent, at the end of each mission and every 2 hours while
// a table is working. Counting for what needs nobody, and one line with an owner
// for each thing that needs somebody.
package progress

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// DefaultEvery is how often the message goes while a table is working.
const DefaultEvery = 2 * time.Hour

// MinGap is the least time between two messages of a workspace: missions that
// end together are one message, not one each.
const MinGap = time.Minute

// maxMessage is the size of a message in characters; the bot takes 4096.
const maxMessage = 3500

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
		return nil, errors.New("progress: the clock, the tables, the requests, the workspaces, the recipients and the sender are required")
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

// safe makes text that somebody else wrote fit in one line of the message:
// secrets masked first (a cut in the middle of one would hide it from the
// mask), then one line without control or format characters, then cut.
func (s *Service) safe(text string, n int) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s.o.Mask(text))
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > n {
		return string(r[:n]) + "…"
	}
	return text
}

func (s *Service) title(t tables.Table) string {
	if t.EpicTitle != "" {
		return s.safe(t.EpicTitle, 60)
	}
	return s.safe(t.ID, 60)
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
	return "o nível " + l.Name
}

// Compose makes the message of a workspace from what the engine confirmed: a
// table counts as merged only when the engine marked it (the merge commit is
// in the mirror, CA-GH-EV-08); one whose pull request was merged on GitHub
// without that stays where it is. No table: ok is false.
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
		name := s.title(t)
		if t.Merged {
			merged = append(merged, name)
			continue
		}
		reqs, err := s.o.Requests.OpenOfTable(ctx, workspace, t.ID)
		if err != nil {
			return Message{}, false, err
		}
		for _, r := range reqs {
			what, verb := s.safe(r.About, 160), "decidir"
			if r.PR != nil {
				what, verb = fmt.Sprintf("aprovar PR #%d", r.PR.Number), "aprovar"
				if len(r.Why) > 0 {
					what += " (" + s.safe(r.Why[0], 120) + ")"
				}
			}
			lines = append(lines, fmt.Sprintf("  🙋 %s: espera %s — %s", name, owner(r.Level), what))
			if next == "" && r.Level.Kind == chain.LevelPeople {
				next = fmt.Sprintf("Próximo: %s volta ao trabalho quando você %s", name, verb)
			}
		}
		switch {
		case t.Paused:
			why := s.safe(t.PausedReason, 160)
			if why == "" {
				why = "sem motivo registrado"
			}
			lines = append(lines, fmt.Sprintf("  ⏸ %s: pausada — %s", name, why))
		case len(reqs) == 0:
			going = append(going, name)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %d %s", s.safe(workspace, 60), len(open), plural(len(open), "mesa", "mesas"))
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
	text := b.String()
	if r := []rune(text); len(r) > maxMessage {
		text = string(r[:maxMessage]) + "\n…"
	}
	return Message{Text: text, Tables: len(open), Working: working}, true, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// claim reserves the turn to send a message of a workspace: it fails when one
// went less than gap ago. The time of the last one is given back by release if
// the message did not reach anybody.
func (s *Service) claim(workspace string, gap time.Duration) (prev time.Time, had, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	prev, had = s.last[workspace]
	if had && now.Sub(prev) < gap {
		return prev, had, false
	}
	s.last[workspace] = now
	return prev, had, true
}

func (s *Service) release(workspace string, prev time.Time, had bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if had {
		s.last[workspace] = prev
	} else {
		delete(s.last, workspace)
	}
}

// deliver sends a text to the people of the workspace; the error says each
// one that was not reached, and reached is how many were.
func (s *Service) deliver(ctx context.Context, workspace, text string) (reached int, err error) {
	to, err := s.o.Recipients.Recipients(ctx, workspace)
	if err != nil {
		return 0, err
	}
	var errs []error
	for _, u := range to {
		if err := s.o.Sender.Send(ctx, u, workspace, text); err != nil {
			errs = append(errs, fmt.Errorf("user %s: %w", u, err))
			continue
		}
		reached++
	}
	return reached, errors.Join(errs...)
}

// send makes and sends the message of a workspace if gap passed since the last.
func (s *Service) send(ctx context.Context, workspace string, gap time.Duration, onlyWorking bool) error {
	m, ok, err := s.Compose(ctx, workspace)
	if err != nil || !ok || onlyWorking && m.Working == 0 {
		return err
	}
	prev, had, ok := s.claim(workspace, gap)
	if !ok {
		return nil
	}
	reached, err := s.deliver(ctx, workspace, m.Text)
	if reached == 0 {
		s.release(workspace, prev, had) // nobody got it: the next look tries again
	}
	return err
}

// Send makes the message of the workspace and sends it to the people it is
// for, unless another went in the last minute. It counts as the last one sent,
// so the 2 hours start again.
func (s *Service) Send(ctx context.Context, workspace string) error {
	return s.send(ctx, workspace, MinGap, false)
}

// MissionEnded is told when a mission of a workspace ended: the message goes
// now, if the workspace acts.
func (s *Service) MissionEnded(ctx context.Context, workspace string) {
	ws, err := s.o.Workspaces.Active(ctx)
	if err != nil || !slices.Contains(ws, workspace) {
		return
	}
	if err := s.Send(ctx, workspace); err != nil {
		s.log.Warn("progress: the message of the end of a mission was not sent", "workspace", workspace, "err", err)
	}
}

// Tick sends the message of each workspace that has a table working and did
// not have one in the last Every. The first look at a workspace starts the
// 2 hours: a restart of the engine does not send at once.
func (s *Service) Tick(ctx context.Context) error {
	ws, err := s.o.Workspaces.Active(ctx)
	if err != nil {
		return err
	}
	for _, w := range ws {
		s.mu.Lock()
		_, seen := s.last[w]
		if !seen {
			s.last[w] = s.clk.Now()
		}
		s.mu.Unlock()
		if !seen {
			continue
		}
		if err := s.send(ctx, w, s.o.Every, true); err != nil {
			s.log.Warn("progress: the message was not sent", "workspace", w, "err", err)
		}
	}
	return nil
}

// Loop calls Tick every period of the clock until the context ends.
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
