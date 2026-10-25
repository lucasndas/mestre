package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/usage"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/pkg/clock"
)

type usageSpy struct {
	events      []providers.Event
	readings    []providers.Usage
	unavailable []string
}

func (s *usageSpy) OnEvent(_ context.Context, ev providers.Event) { s.events = append(s.events, ev) }
func (s *usageSpy) Reading(_ context.Context, _ usage.Subject, u providers.Usage, _ usage.Reading) error {
	s.readings = append(s.readings, u)
	return nil
}
func (s *usageSpy) SourceUnavailable(_ context.Context, id, source, _ string) error {
	s.unavailable = append(s.unavailable, id+"/"+source)
	return nil
}

type subjects map[string]usage.Subject

func (s subjects) Character(_ context.Context, id string) (usage.Subject, bool, error) {
	sub, ok := s[id]
	return sub, ok, nil
}

func writeCodexRollout(t *testing.T, path string, from, to int) {
	t.Helper()
	rec, err := testkit.LoadRecording(testkit.Recordings(), "codex", "", "rollout")
	if err != nil {
		t.Fatal(err)
	}
	var lines []json.RawMessage
	if err := json.Unmarshal(rec.Body, &lines); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines[from:to] {
		var c bytes.Buffer
		if err := json.Compact(&c, l); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(c.Bytes(), '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

// CA-USO-13: the use of the providers is taken in production. The
// events of the characters reach the limits of use, a Codex character is read
// from its transcript periodically (each reading from where the last stopped),
// and a character whose source cannot be read is reported, never guessed.
func TestTheSourcesOfUseAreReadPeriodicallyAndAMissingOneIsReported(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	profile := t.TempDir()
	path := filepath.Join(profile, "sessions", "2026", "10", "14", "rollout-2026-10-14T12-00-00-019a0000-0000-7000-8000-000000000001.jsonl")
	writeCodexRollout(t, path, 0, 3)

	spy := &usageSpy{}
	chars := []characters.Character{
		{ID: "cx", Machine: "m1", Sheet: "Dev Codex", State: characters.Busy},
		{ID: "cx2", Machine: "m1", Sheet: "Outro Codex", State: characters.Busy},
		{ID: "away", Machine: "m2", Sheet: "Dev Codex", State: characters.Busy},
		{ID: "cc", Machine: "m1", Sheet: "Dev Claude", State: characters.Busy},
	}
	r := newUsageReader(&usageReader{svc: spy, machine: "m1", clk: clock.Real{}, log: slog.New(slog.DiscardHandler),
		dir: subjects{"cx": {ID: "cx", Provider: providers.Codex}, "cx2": {ID: "cx2", Provider: providers.Codex},
			"away": {ID: "away", Provider: providers.Codex}, "cc": {ID: "cc", Provider: providers.ClaudeCode}},
		alive:    func(context.Context) ([]characters.Character, error) { return chars, nil },
		adapters: providers.NewRegistry(providers.Options{}),
		src: UsageSources{Profile: func(_ context.Context, c characters.Character) (UsageProfile, bool) {
			return UsageProfile{Dir: profile}, c.ID == "cx"
		}}})
	r.Note(providers.Event{AgentID: "cx", Event: providers.EventTurnEnd, SessionID: "019a0000-0000-7000-8000-000000000001", TranscriptPath: path})

	r.Note(providers.Event{AgentID: "cx2", Event: providers.EventTurnEnd, SessionID: "s2", TranscriptPath: path})
	if err := r.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(spy.readings) != 1 || spy.readings[0].InputTokens != 12000 {
		t.Fatalf("readings = %+v, want the tokens of the first turn", spy.readings)
	}
	if len(spy.unavailable) != 1 || spy.unavailable[0] != "cx2/codex" {
		t.Errorf("unavailable = %v, want only the character with no profile (not the other machine's, not Claude's)", spy.unavailable)
	}
	// nothing new in the transcript: no second reading
	if err := r.Pass(ctx); err != nil || len(spy.readings) != 1 {
		t.Fatalf("a reading with nothing new: %v, %d readings", err, len(spy.readings))
	}
	// the next lines are read from where the last stopped
	writeCodexRollout(t, path, 3, 6)
	if err := r.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(spy.readings) != 2 {
		t.Fatalf("readings = %d, want the new turn read", len(spy.readings))
	}
	// only the lines 3-6 were read: the delta, not the whole file again (the first reading was 12000)
	if got := spy.readings[1]; got.InputTokens != 8000 || got.OutputTokens != 100 {
		t.Errorf("second reading = %d in / %d out, want 8000 / 100 (the tokens of lines 3-6 only)", got.InputTokens, got.OutputTokens)
	}
	// and the cursor stays at the end of the file
	var cur struct {
		Offset int64 `json:"offset"`
	}
	if st, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal([]byte(spy.readings[1].Cursor), &cur); err != nil || cur.Offset != st.Size() {
		t.Errorf("cursor = %q (%v), want the offset at the end of the file (%d)", spy.readings[1].Cursor, err, st.Size())
	}

	if len(spy.unavailable) != 1 {
		t.Errorf("unavailable = %v, a missing source is told once", spy.unavailable)
	}
	// a new session (another transcript) starts over instead of reusing the old cursor
	path2 := filepath.Join(profile, "sessions", "2026", "10", "15", "rollout-2026-10-15T12-00-00-019a0000-0000-7000-8000-000000000002.jsonl")
	writeCodexRollout(t, path2, 0, 3)
	r.Note(providers.Event{AgentID: "cx", Event: providers.EventTurnEnd, SessionID: "019a0000-0000-7000-8000-000000000002", TranscriptPath: path2})
	if err := r.Pass(ctx); err != nil || len(spy.readings) != 3 || spy.readings[2].InputTokens != 12000 {
		t.Fatalf("the new session: %v, readings %+v", err, spy.readings)
	}
	// the events of the period reach the limits of use and keep the transcript
	hu := newHookUsage(spy, r, nil)
	hu.onEvent(providers.Event{AgentID: "cc", Event: providers.EventTurnStart})
	hu.flush()
	if len(spy.events) != 1 {
		t.Errorf("events = %d, want the event taken", len(spy.events))
	}
}

// CA-USO-13: an agent.event of a Tower reaches the limits of use
// of the period when the registry puts the character on that Tower, and is
// refused (forbidden, nothing taken) when it puts it on another.
func TestAnAgentEventOfATowerReachesTheUseOfThePeriod(t *testing.T) {
	t.Parallel()
	spy := &usageSpy{}
	e := &Engine{}
	r := newUsageReader(&usageReader{svc: spy, clk: clock.Real{}})
	e.period.Store(&periodHooks{usage: newHookUsage(spy, r, nil),
		agentMachine: func(_ context.Context, id string) (string, bool) { return "torre-1", id == "dev-1" }})
	send := func(from, agent string) error {
		payload, err := agentEventPayload(providers.Event{AgentID: agent, Event: providers.EventTurnEnd}, hook.Answer{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.handleAgentEvent(context.Background(), &protocol.Message{Payload: &payload, Peer: protocol.Peer{ID: from}})
		return err
	}
	err := send("torre-1", "dev-1")
	e.period.Load().usage.flush()
	if err != nil || len(spy.events) != 1 {
		t.Fatalf("the event of the Tower where it runs: %v, %d taken", err, len(spy.events))
	}
	for _, c := range []struct{ from, agent string }{{"torre-2", "dev-1"}, {"torre-1", "ghost"}} {
		var perr *protocol.Error
		if err := send(c.from, c.agent); !errors.As(err, &perr) || perr.Code != protocol.CodeForbidden {
			t.Errorf("%s says an event of %q: %v; want forbidden", c.from, c.agent, err)
		}
	}
	if len(spy.events) != 1 {
		t.Errorf("events taken = %d, want only the first", len(spy.events))
	}
}
