package app

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

// CA-USO-14: a Tower reads the transcript of its Codex character on its own
// machine, with no sheet to name (it has no registry), and tells the Citadel in
// usage.reading; the Citadel takes the reading as if it had read it. Each
// reading goes on from where the last stopped, a missing profile is told once,
// and nothing is read while the Tower has no Citadel.
func TestATowerReadsItsCharactersAndTheCitadelTakesTheReading(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	profile := t.TempDir()
	path := filepath.Join(profile, "sessions", "2026", "10", "14", "rollout-2026-10-14T12-00-00-019a0000-0000-7000-8000-000000000001.jsonl")
	writeCodexRollout(t, path, 0, 3)

	citadel := &usageSpy{}
	hu := newHookUsage(citadel, newUsageReader(&usageReader{svc: citadel, dir: subjects{"cx": {ID: "cx", Provider: providers.Codex}}, clk: clock.Real{}}), nil)
	var sent []protocol.UsageReading
	connected := false
	sink := towerUsageSink{tell: func(ctx context.Context, p protocol.UsageReading) error {
		if err := p.Validate(); err != nil {
			t.Errorf("the Tower sent a message that does not validate: %v", err)
		}
		sent = append(sent, p)
		return hu.tower(ctx, p)
	}}
	living := []characters.Character{
		{ID: "cx", Machine: "t1", State: characters.Busy},
		{ID: "cx2", Machine: "t1", State: characters.Busy},
	}
	r := newUsageReader(&usageReader{svc: sink, machine: "t1", clk: clock.Real{}, log: slog.New(slog.DiscardHandler), noSheet: true,
		pause:    func() bool { return !connected },
		dir:      subjects{"cx": {ID: "cx", Provider: providers.Codex}, "cx2": {ID: "cx2", Provider: providers.Codex}},
		alive:    func(context.Context) ([]characters.Character, error) { return living, nil },
		adapters: providers.NewRegistry(providers.Options{}),
		src: UsageSources{Profile: func(_ context.Context, c characters.Character) (UsageProfile, bool) {
			return UsageProfile{Dir: profile}, c.ID == "cx"
		}}})
	r.Note(providers.Event{AgentID: "cx", Event: providers.EventTurnEnd, SessionID: "019a0000-0000-7000-8000-000000000001", TranscriptPath: path})
	r.Note(providers.Event{AgentID: "cx2", Event: providers.EventTurnEnd, SessionID: "s2", TranscriptPath: path})

	if err := r.Pass(ctx); err != nil || len(sent) != 0 {
		t.Fatalf("a pass with no Citadel: %v, %d messages; want none", err, len(sent))
	}
	connected = true
	if err := r.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if len(citadel.readings) != 1 || citadel.readings[0].InputTokens != 12000 {
		t.Fatalf("the Citadel took %+v, want the tokens of the first turn", citadel.readings)
	}
	if len(citadel.unavailable) != 1 || citadel.unavailable[0] != "cx2/codex" {
		t.Errorf("unavailable = %v, want the character with no profile", citadel.unavailable)
	}
	if err := r.Pass(ctx); err != nil || len(sent) != 2 {
		t.Fatalf("nothing new: %v, %d messages in all; want no new one (the missing source is told once)", err, len(sent))
	}
	writeCodexRollout(t, path, 3, 6)
	if err := r.Pass(ctx); err != nil || len(citadel.readings) != 2 {
		t.Fatalf("the next lines: %v, %d readings", err, len(citadel.readings))
	}
	if got := citadel.readings[1]; got.InputTokens != 8000 || got.OutputTokens != 100 {
		t.Errorf("second reading = %d in / %d out, want 8000 / 100 (the tokens of lines 3-6 only, not the first batch again)", got.InputTokens, got.OutputTokens)
	}
}

// CA-USO-14
func TestTheCitadelTakesAUsageReadingOnlyFromTheTowerOfTheCharacter(t *testing.T) {
	t.Parallel()
	spy := &usageSpy{}
	e := &Engine{}
	r := newUsageReader(&usageReader{svc: spy, dir: subjects{"dev-1": {ID: "dev-1", Provider: providers.Codex}}, clk: clock.Real{}})
	e.period.Store(&periodHooks{usage: newHookUsage(spy, r, nil),
		agentMachine: func(_ context.Context, id string) (string, bool) { return "torre-1", id == "dev-1" || id == "dev-2" }})
	send := func(from string, p protocol.UsageReading) error {
		_, err := e.handleUsageReading(context.Background(), &protocol.Message{Payload: &p, Peer: protocol.Peer{ID: from}})
		return err
	}
	ok := protocol.UsageReading{Character: "dev-1", Source: "codex", Session: "s", Usage: []byte(`{"input_tokens":5}`)}
	if err := send("torre-1", ok); err != nil || len(spy.readings) != 1 || spy.readings[0].InputTokens != 5 {
		t.Fatalf("the Tower where it runs: %v, %+v", err, spy.readings)
	}
	if err := send("torre-1", protocol.UsageReading{Character: "dev-1", Source: "codex", Unavailable: "no\nprofile"}); err != nil || len(spy.unavailable) != 1 {
		t.Fatalf("an unavailable source: %v, %v", err, spy.unavailable)
	}
	for name, c := range map[string]struct {
		from string
		p    protocol.UsageReading
		code string
	}{
		"another Tower":    {"torre-2", ok, protocol.CodeForbidden},
		"unknown":          {"torre-1", protocol.UsageReading{Character: "ghost", Source: "codex", Usage: ok.Usage}, protocol.CodeForbidden},
		"not known to use": {"torre-1", protocol.UsageReading{Character: "dev-2", Source: "codex", Usage: ok.Usage}, protocol.CodeInvalidPayload},
		"unreadable":       {"torre-1", protocol.UsageReading{Character: "dev-1", Source: "codex", Usage: []byte(`[1]`)}, protocol.CodeInvalidPayload},
	} {
		var perr *protocol.Error
		if err := send(c.from, c.p); !errors.As(err, &perr) || perr.Code != c.code {
			t.Errorf("%s: %v; want %s", name, err, c.code)
		}
	}
	if len(spy.readings) != 1 || len(spy.unavailable) != 1 {
		t.Errorf("taken = %d readings, %d unavailable; the refused ones are not taken", len(spy.readings), len(spy.unavailable))
	}
}

func TestAUsageReadingSaysOneThing(t *testing.T) {
	t.Parallel()
	for name, p := range map[string]protocol.UsageReading{
		"neither": {Character: "a", Source: "codex"},
		"both":    {Character: "a", Source: "codex", Usage: []byte(`{}`), Unavailable: "x"},
	} {
		if err := p.Validate(); err == nil {
			t.Errorf("%s: validates", name)
		}
	}
}

// The day of use turns in the time zone of the Guild when the host gives none.
func TestTheDayOfUseTurnsInTheTimeZoneOfTheGuild(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, err := guilddb.Open(ctx, filepath.Join(t.TempDir(), "guild.db"), store.Options{StmtCache: guildtpl.StmtCache, Clock: clock.Real{}, Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	zone := usageZone(nil, db)
	if zone == nil || zone(ctx) != nil {
		t.Fatalf("with no Guild row the zone must be UTC (nil)")
	}
	set := func(name string) {
		t.Helper()
		if _, err := db.Writer().ExecContext(ctx, `INSERT INTO guild (id, guild_id, ca_cert, epoch, epoch_certificate, timezone, recovery_public_key, created_at) VALUES (1, 'g', x'00', 1, x'00', ?, x'00', 0)
			ON CONFLICT(id) DO UPDATE SET timezone = excluded.timezone`, name); err != nil {
			t.Fatal(err)
		}
	}
	set("America/Sao_Paulo")
	if z := zone(ctx); z == nil || z.String() != "America/Sao_Paulo" {
		t.Errorf("zone = %v, want the one of the Guild", z)
	}
	for _, name := range []string{"Local", "Nowhere/None", ""} {
		set(name)
		if z := zone(ctx); z != nil {
			t.Errorf("zone for %q = %v, want UTC (nil)", name, z)
		}
	}
	fixed := time.FixedZone("X", 3600)
	if z := usageZone(func(context.Context) *time.Location { return fixed }, db)(ctx); z != fixed {
		t.Error("a given zone must win")
	}
	if usageZone(nil, nil) != nil {
		t.Error("no database, no zone")
	}
}
