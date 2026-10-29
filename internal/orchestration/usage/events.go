package usage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/providers"
)

// Where the use of each provider comes from (docs/plataforma/limites-de-uso.md).
const (
	sourceStatusLine = "statusline"
	sourceTokenCount = "token_count"
	sourceUsageCmd   = "usage_command"
	sourceTurn       = "turn"
)

func sourceOf(p providers.ID) string {
	switch p {
	case providers.ClaudeCode:
		return sourceStatusLine
	case providers.Codex:
		return sourceTokenCount
	case providers.Antigravity:
		return sourceUsageCmd
	}
	return sourceTurn
}

// OnEvent takes an event of a character: the start and the end of a turn give
// the active time and the turns (always, whatever the provider tells), the
// usage events the tokens, the cost and the windows. The consumer of the
// events of this machine and of the Towers (agent.event) calls it for each
// one; an event of a character the registry does not know is dropped, because
// it could not be put in any quest, table or workspace.
func (s *Service) OnEvent(ctx context.Context, ev providers.Event) {
	if ev.AgentID == "" {
		return
	}
	sub, ok, err := s.o.Directory.Character(ctx, ev.AgentID)
	if err != nil {
		s.log.Warn("usage: a character was not found", "id", ev.AgentID, "err", err)
		return
	}
	if !ok {
		s.log.Warn("usage: an event of a character that is not known was dropped", "id", ev.AgentID)
		return
	}
	at := ev.ReceivedAt
	if at.IsZero() {
		at = s.clk.Now()
	}
	var errs error
	switch ev.Event {
	case providers.EventTurnStart:
		s.mu.Lock()
		s.busy[sub.ID] = at
		s.mu.Unlock()
		// the provider answers again: whatever it was waiting for is over
		errs = s.release(ctx, sub.ID, false)
	case providers.EventTurnEnd, providers.EventTurnFailure, providers.EventInterrupt:
		errs = s.finish(ctx, sub, ev, at)
	case providers.EventUsage:
		if ev.Usage != nil {
			errs = s.Reading(ctx, sub, *ev.Usage, Reading{At: at, Model: ev.Model, Session: ev.SessionID, Cumulative: sub.Provider == providers.ClaudeCode})
		}
	}
	if errs != nil {
		s.log.Warn("usage: an event was not fully applied", "id", sub.ID, "event", ev.Event, "err", errs)
	}
}

// base is a record of the character with what the registry knows of it.
func (s *Service) base(sub Subject, at time.Time, model string) Record {
	return Record{Character: sub.ID, Step: sub.Step, Quest: sub.Quest, Workspace: sub.Workspace, Table: sub.Table, Profile: sub.Profile, Provider: sub.Provider,
		Model: model, At: at, Source: sourceTurn, Origin: OriginStep, QuestName: sub.QuestName, TableName: sub.TableName}
}

// finish ends a turn: the time it was busy and one turn, with or without
// tokens. A turn that ended for lack of quota also puts the character to
// wait for it.
func (s *Service) finish(ctx context.Context, sub Subject, ev providers.Event, at time.Time) error {
	s.mu.Lock()
	start, had := s.busy[sub.ID]
	delete(s.busy, sub.ID)
	s.mu.Unlock()
	r := s.base(sub, at, ev.Model)
	r.Turns = 1
	if had && at.After(start) {
		r.Active = at.Sub(start)
	}
	err := s.Record(ctx, r)
	if ev.Event == providers.EventTurnFailure && ev.StopReason == providers.StopRateLimit {
		var until time.Time
		if ev.Usage != nil {
			until = ev.Usage.LimitsResetAt
		}
		if qerr := s.quota(ctx, sub, until); qerr != nil && err == nil {
			err = qerr
		}
	}
	return err
}

// Reading is how a normalized reading (ReadUsage, or the usage event of the
// statusLine) is taken.
type Reading struct {
	// At is when it was read (default: now).
	At    time.Time
	Model string
	// Session identifies the session of a source that gives totals.
	Session string
	// Cumulative says the tokens and the cost are the totals of the session
	// (Claude Code's statusLine): the record is the difference from the last
	// reading. Otherwise they are what happened since the last reading.
	Cumulative bool
}

// Reading takes the usage a provider reported for a character: the tokens
// and the cost become a record, the windows of the plan are kept for the
// start threshold, and a quota that ran out puts the character to wait.
func (s *Service) Reading(ctx context.Context, sub Subject, u providers.Usage, rd Reading) error {
	at := rd.At
	if at.IsZero() {
		at = s.clk.Now()
	}
	pump := false
	for _, w := range u.LimitWindows {
		cleared, err := s.st.putWindow(ctx, window{Provider: string(sub.Provider), Profile: sub.Profile, Name: w.Window, Bucket: w.Bucket, UsedPct: w.UsedPct, ResetsAt: w.ResetsAt, ReadAt: at})
		if err != nil {
			return fmt.Errorf("usage: %w", err)
		}
		pump = pump || cleared
	}
	var errs []error
	if pump && s.o.Pump != nil {
		if err := s.o.Pump.PumpAll(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if err := s.recordReading(ctx, sub, u, rd, at); err != nil {
		errs = append(errs, err)
	}
	if err := s.readQuota(ctx, sub, u); err != nil {
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("usage: %w", joinErrors(errs))
}

type joined []error

func (j joined) Error() string {
	parts := make([]string, len(j))
	for i, e := range j {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}

func (j joined) Unwrap() []error { return j }

func joinErrors(errs []error) error {
	if len(errs) == 1 {
		return errs[0]
	}
	return joined(errs)
}

func (s *Service) recordReading(ctx context.Context, sub Subject, u providers.Usage, rd Reading, at time.Time) error {
	cur := cursor{Session: rd.Session, Input: u.InputTokens, Output: u.OutputTokens, CacheRead: u.CacheReadTokens, CacheWrite: u.CacheWriteTokens, Reasoning: u.ReasoningTokens, Cost: u.CostUSD}
	delta := cur
	if rd.Cumulative {
		// a total that went back, or another session, is a new count
		if old, ok, err := s.st.cursor(ctx, sub.ID); err != nil {
			return err
		} else if ok && old.Session == cur.Session && cur.Input >= old.Input && cur.Output >= old.Output && cur.CacheRead >= old.CacheRead &&
			cur.CacheWrite >= old.CacheWrite && cur.Reasoning >= old.Reasoning && cur.Cost >= old.Cost {
			delta = cursor{Input: cur.Input - old.Input, Output: cur.Output - old.Output, CacheRead: cur.CacheRead - old.CacheRead,
				CacheWrite: cur.CacheWrite - old.CacheWrite, Reasoning: cur.Reasoning - old.Reasoning, Cost: cur.Cost - old.Cost}
		}
	}
	tokens := Tokens{Input: delta.Input, Output: delta.Output, CacheRead: delta.CacheRead, CacheWrite: delta.CacheWrite, Reasoning: delta.Reasoning}
	tokens.Known = tokens.Total() > 0
	var cost *float64
	if delta.Cost > 0 {
		c := delta.Cost
		cost = &c
	}
	if !tokens.Known && cost == nil {
		return s.keepCursor(ctx, sub.ID, rd.Cumulative, cur) // windows only: nothing was used since the last reading
	}
	r := s.base(sub, at, rd.Model)
	r.Source = sourceOf(sub.Provider)
	r.Tokens, r.CostUSD = tokens, cost
	// the totals move on only once the row is kept, or its delta would be lost
	err := s.Record(ctx, r)
	if errors.Is(err, errInsert) {
		return err
	}
	if kerr := s.keepCursor(ctx, sub.ID, rd.Cumulative, cur); kerr != nil {
		return errors.Join(err, kerr)
	}
	return err
}

func (s *Service) keepCursor(ctx context.Context, id string, cumulative bool, c cursor) error {
	if !cumulative {
		return nil
	}
	return s.st.putCursor(ctx, id, c)
}

// readQuota reads the quota out of a reading: a provider that says the limit
// was reached, or a window with nothing left, puts the character to wait; a
// reading with windows that all have room takes the wait off.
func (s *Service) readQuota(ctx context.Context, sub Subject, u providers.Usage) error {
	now := s.clk.Now()
	reached := u.LimitsReached
	until := u.LimitsResetAt
	for _, w := range u.LimitWindows {
		if w.UsedPct >= 100 {
			reached = true
			if w.ResetsAt.After(until) {
				until = w.ResetsAt
			}
		}
	}
	switch {
	case reached && (until.IsZero() || until.After(now)):
		return s.quota(ctx, sub, until)
	case !reached && len(u.LimitWindows) > 0:
		return s.release(ctx, sub.ID, true)
	}
	return nil
}

// quota puts a character to wait for the quota (blocked, reason quota),
// until the moment the provider gave, if any, and tells who has to decide
// what to do (docs/plataforma/limites-de-uso.md#aguardando-cota).
func (s *Service) quota(ctx context.Context, sub Subject, until time.Time) error {
	fresh, err := s.st.putWait(ctx, wait{Character: sub.ID, Provider: string(sub.Provider), Profile: sub.Profile, Until: until, Since: s.clk.Now()})
	if err != nil {
		return err
	}
	if !fresh {
		return nil // already waiting: only the time may have moved
	}
	var errs []error
	if s.o.Characters != nil {
		if err := s.o.Characters.Block(ctx, sub.ID); err != nil {
			errs = append(errs, err)
			_, _ = s.st.takeWait(ctx, sub.ID) // so the next reading tries to block again
		}
	}
	acct := i18n.M("usage.account_default")
	if sub.Profile != "" {
		acct = i18n.M("usage.account_named", "name", sub.Profile)
	}
	parts := []i18n.Msg{i18n.M("usage.quota_waiting", "name", orElse(sub.Name, sub.ID), "provider", string(sub.Provider), "account", acct)}
	if until.IsZero() {
		parts = append(parts, i18n.M("usage.quota_unknown"))
	} else {
		parts = append(parts, i18n.M("usage.quota_until", "at", s.clock(ctx, until)))
	}
	alts, err := s.o.Directory.Alternatives(ctx, sub)
	if err != nil {
		errs = append(errs, err)
	}
	if len(alts) > 0 {
		parts = append(parts, i18n.M("usage.quota_alternatives", "names", strings.Join(alts, ", ")))
	} else {
		parts = append(parts, i18n.M("usage.quota_no_alternative"))
	}
	text := i18n.Concat(parts...)
	if err := s.tellQuota(ctx, sub, text); err != nil {
		errs = append(errs, err)
	}
	return joinedOrNil(errs)
}

func joinedOrNil(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	return joinErrors(errs)
}

// tellQuota sends the event to whom decides: the table master; if it is the
// one that ran out, the Grand Master; if it is the Grand Master, the users.
func (s *Service) tellQuota(ctx context.Context, sub Subject, msg i18n.Msg) error {
	switch sub.Role {
	case characters.RoleGrandMaster:
		if s.o.Alerts != nil && sub.Workspace != "" {
			return s.o.Alerts.Notify(ctx, sub.Workspace, msg)
		}
		return nil
	case characters.RoleTableMaster:
		return s.notify(ctx, characters.ToGrandMaster, sub, i18n.Render(i18n.PtBR, msg))
	}
	return s.notify(ctx, characters.ToTableMaster, sub, i18n.Render(i18n.PtBR, msg))
}

func (s *Service) notify(ctx context.Context, to characters.RecipientKind, sub Subject, text string) error {
	if s.o.Notifier == nil {
		return nil
	}
	return s.o.Notifier.Notify(ctx, characters.Notice{Kind: characters.NoticeSystem, To: characters.Recipient{Kind: to}, Workspace: sub.Workspace, Table: sub.Table,
		Character: sub.ID, Name: sub.Name, Text: text})
}

// release takes the wait for the quota off a character. With unblock it also
// brings the character out of blocked and delivers what waited.
func (s *Service) release(ctx context.Context, id string, unblock bool) error {
	if unblock && s.o.Characters != nil {
		if has, err := s.st.waitOf(ctx, id); err != nil {
			return err
		} else if has {
			if err := s.o.Characters.Unblock(ctx, id); err != nil {
				return err // the wait stays, and the sweep tries again
			}
		}
	}
	_, err := s.st.takeWait(ctx, id)
	return err
}

// SourceUnavailable is a source of use that gave nothing for a character in
// activity (the statusLine does not run, the transcript was not found, the
// /usage failed): only the time and the turns are kept, and the alert ALL-23
// of the checklist opens.
func (s *Service) SourceUnavailable(ctx context.Context, character, source, detail string) error {
	s.log.Warn("usage: a source of use is not available", "id", character, "source", source, "detail", detail)
	if s.o.Checklist == nil {
		return nil
	}
	return s.o.Checklist.Raise(ctx, "ALL-23", character, source+": "+detail)
}

// clock writes a moment as the hour of the time zone of the Guild.
func (s *Service) clock(ctx context.Context, t time.Time) string {
	return t.In(s.location(ctx)).Format("15:04")
}
