package app

import (
	"context"
	"errors"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/usage"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The use of the providers in production (docs/plataforma/limites-de-uso.md):
// the events of the characters of this machine and of the Towers reach the
// limits of use, and the providers that keep their use in a transcript or in a
// command (Codex, Antigravity) are read periodically.

// usageEvery is how often the sources of use are read.
const usageEvery = time.Minute

// UsageProfile is where the use of the account of a character is read from: the
// folder of the profile (CODEX_HOME for Codex), the environment of the account
// (the HOME of the profile for Antigravity) and the executable of the provider
// ("" is the default name). The installation gives it (M7: the accounts of the
// agents); the tests give a fake.
type UsageProfile struct {
	Dir, Command, Start string
	Env                 []string
}

// UsageSources are the pieces of the periodic reading that only the
// installation has: the account profile of a character and the runner of the
// provider commands (processes start only in the designated layers). Without
// Profile a character with a provider that is read periodically has its source
// reported as unavailable, never read from a guessed folder.
type UsageSources struct {
	Profile func(ctx context.Context, c characters.Character) (UsageProfile, bool)
	Exec    providers.ExecFunc
}

// usageTaker is the limits of use (usage.Service) as the host asks them.
type usageTaker interface {
	OnEvent(ctx context.Context, ev providers.Event)
	Reading(ctx context.Context, sub usage.Subject, u providers.Usage, rd usage.Reading) error
	SourceUnavailable(ctx context.Context, character, source, detail string) error
}

// usageReader reads, for the characters of this machine, the sources of use
// that are not events. It keeps the last event of each character (where the
// transcript is) and the cursor of the previous reading.
type usageReader struct {
	svc usageTaker
	dir interface {
		Character(ctx context.Context, id string) (usage.Subject, bool, error)
	}
	alive    func(ctx context.Context) ([]characters.Character, error)
	adapters *providers.Registry
	src      UsageSources
	machine  string
	clk      clock.Clock
	log      *slog.Logger

	mu      sync.Mutex
	last    map[string]providers.Event
	cursors map[string]usageCursor
	// reported are the characters whose missing source was already told
	reported map[string]bool
}

// usageCursor is where the reading of a transcript stopped, with the
// transcript it belongs to: a new session starts over.
type usageCursor struct{ transcript, session, cursor string }

func newUsageReader(r *usageReader) *usageReader {
	r.last, r.cursors, r.reported = map[string]providers.Event{}, map[string]usageCursor{}, map[string]bool{}
	return r
}

// Note keeps the last event of a character that tells where its transcript is.
func (u *usageReader) Note(ev providers.Event) {
	if ev.AgentID == "" || ev.TranscriptPath == "" {
		return
	}
	u.mu.Lock()
	u.last[ev.AgentID] = ev
	u.mu.Unlock()
}

// Loop reads the sources every interval until the context ends.
func (u *usageReader) Loop(ctx context.Context, every time.Duration) error {
	t := u.clk.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C():
			if err := u.Pass(ctx); err != nil {
				u.log.Warn("usage: the sources of use were not all read", "err", err)
			}
		}
	}
}

// Pass reads the source of use of each living character of this machine whose
// provider keeps it outside the events.
func (u *usageReader) Pass(ctx context.Context) error {
	alive, err := u.alive(ctx)
	if err != nil {
		return err
	}
	var errs []error
	mine := map[string]bool{}
	for _, c := range alive {
		if c.Machine == u.machine {
			mine[c.ID] = true
		}
	}
	u.prune(mine)
	for _, c := range alive {
		if c.Machine != u.machine || c.Sheet == "" {
			continue
		}
		if err := u.read(ctx, c); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (u *usageReader) read(ctx context.Context, c characters.Character) error {
	sub, ok, err := u.dir.Character(ctx, c.ID)
	if err != nil || !ok {
		return err
	}
	if sub.Provider != providers.Codex && sub.Provider != providers.Antigravity {
		return nil // the statusLine of Claude Code arrives as an event; a shell spends nothing
	}
	if c.State != characters.Busy && c.State != characters.Idle {
		return nil // only a character in activity has a source that can be missing
	}
	u.mu.Lock()
	last := u.last[c.ID]
	cur := u.cursors[c.ID]
	u.mu.Unlock()
	if last.TranscriptPath == "" && sub.Provider == providers.Codex {
		return nil // no event yet: there is no transcript to read
	}
	prof, known := UsageProfile{}, false
	if u.src.Profile != nil {
		prof, known = u.src.Profile(ctx, c)
	}
	if !known {
		return u.unavailable(ctx, c.ID, sub.Provider, "the account profile of the character is not known to this machine")
	}
	ad, err := u.adapters.For(sub.Provider)
	if err != nil {
		return err
	}
	cursor := ""
	if cur.transcript == last.TranscriptPath && cur.session == last.SessionID {
		cursor = cur.cursor // the same session goes on; a new one starts over
	}
	got, err := ad.ReadUsage(providers.UsageRequest{AgentID: c.ID, Last: last, Profile: prof.Dir, Cursor: cursor,
		Command: prof.Command, Env: prof.Env, Dir: prof.Start, OS: runtime.GOOS})
	keep := func() {
		if got.Cursor != "" {
			u.mu.Lock()
			u.cursors[c.ID] = usageCursor{transcript: last.TranscriptPath, session: last.SessionID, cursor: got.Cursor}
			u.mu.Unlock()
		}
	}
	switch {
	case errors.Is(err, providers.ErrNoUsage):
		keep() // nothing new since the last reading
		return nil
	case err != nil:
		return u.unavailable(ctx, c.ID, sub.Provider, err.Error())
	}
	if err := u.svc.Reading(ctx, sub, got, usage.Reading{Session: last.SessionID}); err != nil {
		return err // the cursor stays: the same lines are read again
	}
	keep()
	u.mu.Lock()
	delete(u.reported, c.ID)
	u.mu.Unlock()
	return nil
}

// unavailable reports a source that gave nothing, once until it gives something.
// The detail is cut and cleaned: it may carry text the character wrote.
func (u *usageReader) unavailable(ctx context.Context, id string, p providers.ID, detail string) error {
	u.mu.Lock()
	told := u.reported[id]
	u.reported[id] = true
	u.mu.Unlock()
	if told {
		return nil
	}
	return u.svc.SourceUnavailable(ctx, id, string(p), cleanDetail(detail))
}

func cleanDetail(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// prune forgets the characters that do not live on this machine any more.
func (u *usageReader) prune(mine map[string]bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for id := range u.last {
		if !mine[id] {
			delete(u.last, id)
			delete(u.cursors, id)
			delete(u.reported, id)
		}
	}
}

// hookUsage is the use of the period: the service that takes the events and
// the reader of the sources. The events are taken in order by one worker, off
// the path that answers the hook and the connection of the Tower.
type hookUsage struct {
	svc    usageTaker
	reader *usageReader
	log    *slog.Logger

	once  sync.Once
	queue chan func()
	stop  chan struct{}
}

// usageQueue is how many events wait; beyond it an event is dropped and told.
const usageQueue = 4096

func newHookUsage(svc usageTaker, reader *usageReader, log *slog.Logger) *hookUsage {
	return &hookUsage{svc: svc, reader: reader, log: log}
}

func (h *hookUsage) start() {
	h.once.Do(func() {
		h.queue, h.stop = make(chan func(), usageQueue), make(chan struct{})
		go func() {
			for {
				select {
				case f := <-h.queue:
					f()
				case <-h.stop:
					return
				}
			}
		}()
	})
}

// onEvent gives the use of the period an event of a character, wherever it ran.
func (h *hookUsage) onEvent(ev providers.Event) {
	h.reader.Note(ev)
	h.start()
	select {
	case h.queue <- func() {
		ctx, cancel := clock.WithTimeout(context.Background(), h.reader.clk, 30*time.Second)
		defer cancel()
		h.svc.OnEvent(ctx, ev)
	}:
	default:
		if h.log != nil {
			h.log.Warn("usage: an event was dropped, the queue is full", "id", ev.AgentID)
		}
	}
}

// flush waits for the events taken so far (the tests).
func (h *hookUsage) flush() {
	h.start()
	done := make(chan struct{})
	h.queue <- func() { close(done) }
	<-done
}

// close ends the worker with the period.
func (h *hookUsage) close() {
	h.start()
	close(h.stop)
}
