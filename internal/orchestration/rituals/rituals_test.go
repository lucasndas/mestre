package rituals_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/orchestration/rituals"
)

// CA-RIT-01: with a fake clock and a ritual `--daily 02:00`, when the clock
// passes 02:00 one firing enters the queue of the target, and only once.
func TestADailyRitualEntersTheQueueOfTheTargetOnceWhenTheClockPasses(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev Claude", "Dev")
	w.state(dev, characters.Busy) // a busy target: the firing stays in the queue
	r := w.make("Testes da noite", "Dev", daily(2, 0))
	if want := time.Date(2026, 10, 16, 2, 0, 0, 0, time.UTC); !r.NextRun.Equal(want) {
		t.Fatalf("next run = %s; want %s", r.NextRun, want)
	}

	w.goTo(time.Date(2026, 10, 16, 1, 59, 0, 0, time.UTC))
	w.tick()
	if fs := w.firings(r); len(fs) != 0 {
		t.Fatalf("before 02:00: %s", states(fs))
	}
	w.goTo(time.Date(2026, 10, 16, 2, 0, 30, 0, time.UTC))
	w.tick()
	w.tick()
	w.goTo(time.Date(2026, 10, 16, 2, 5, 0, 0, time.UTC))
	w.tick()
	fs := w.firings(r)
	if len(fs) != 1 || fs[0].State != rituals.Queued || !fs[0].ScheduledFor.Equal(time.Date(2026, 10, 16, 2, 0, 0, 0, time.UTC)) {
		t.Fatalf("firings after 02:00 = %s; want one queued for 02:00", states(fs))
	}
	if got := w.items(dev, inbox.KindRitual); len(got) != 0 {
		t.Errorf("a busy target got %d items; the firing waits", len(got))
	}
	got, err := w.orc.Rituals.Store().Ritual(w.ctx, r.ID)
	if err != nil || !got.NextRun.Equal(time.Date(2026, 10, 17, 2, 0, 0, 0, time.UTC)) {
		t.Errorf("next run = %s (%v); want the next day at 02:00", got.NextRun, err)
	}
}

// CA-RIT-02: with the target busy, when the ritual fires and the target is free
// in 10 minutes, the firing is delivered; when it stays busy for more than 30
// minutes, the firing is expired and the owner gets the item with the reason.
func TestABusyTargetQueuesTheFiringAndTheTimeoutGivesItBackToTheOwner(t *testing.T) {
	t.Parallel()
	t.Run("free in 10 minutes", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		dev := w.recruit("Dev Claude", "Dev")
		w.state(dev, characters.Busy)
		r := w.make("Testes", "Dev", every(time.Hour))
		w.goTo(w.at(10, 20))
		w.tick()
		if fs := w.firings(r); len(fs) != 1 || fs[0].State != rituals.Queued {
			t.Fatalf("firings = %s; want one queued", states(fs))
		}
		w.goTo(w.at(10, 30))
		w.state(dev, characters.Idle)
		w.tick()
		fs := w.firings(r)
		if len(fs) != 1 || fs[0].State != rituals.Delivered {
			t.Fatalf("firings = %s; want one delivered", states(fs))
		}
		items := w.items(dev, inbox.KindRitual)
		if len(items) != 1 || items[0].Text != "Rode os testes" {
			t.Fatalf("items of the target = %+v", items)
		}
		if typed := w.machines.Delivered(w.char("Dev")); len(typed) != 1 || !strings.Contains(typed[0], "Rode os testes") {
			t.Errorf("typed in the terminal = %q", typed)
		}
		if got := w.items(w.master, inbox.KindRitual); len(got) != 0 {
			t.Errorf("the owner got %d items for a firing that was delivered", len(got))
		}
	})
	t.Run("busy for more than 30 minutes", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		dev := w.recruit("Dev Claude", "Dev")
		w.state(dev, characters.Busy)
		r := w.make("Testes", "Dev", every(time.Hour))
		w.goTo(w.at(10, 20))
		w.tick()
		w.goTo(w.at(10, 49))
		w.tick()
		if fs := w.firings(r); fs[0].State != rituals.Queued {
			t.Fatalf("at 29 minutes: %s; want it still queued", states(fs))
		}
		w.goTo(w.at(10, 51))
		w.tick()
		fs := w.firings(r)
		if len(fs) != 1 || fs[0].State != rituals.Expired {
			t.Fatalf("at 31 minutes: %s; want expired", states(fs))
		}
		items := w.items(w.master, inbox.KindRitual)
		if len(items) != 1 {
			t.Fatalf("the owner got %d items; want 1", len(items))
		}
		for _, want := range []string{"Dev", "ocupado", "10:20", "30m"} {
			if !strings.Contains(items[0].Text, want) {
				t.Errorf("the item to the owner = %q; want %q in it", items[0].Text, want)
			}
		}
		if got := w.items(dev, inbox.KindRitual); len(got) != 0 {
			t.Errorf("the target got %d items from a firing that expired", len(got))
		}
		// A free target later does not receive the old firing.
		w.state(dev, characters.Idle)
		w.tick()
		if got := w.items(dev, inbox.KindRitual); len(got) != 0 {
			t.Errorf("the target got the firing that was given back")
		}
	})
}

// CA-RIT-03: with `--pre-run` that prints "3 falhas" and the content "Analise:
// {{output}}", when delivered the target receives "Analise: 3 falhas"; when the
// pre-run exits with 1, the firing is failed and given back.
func TestThePreRunOutputReplacesThePlaceholderAndAFailureGivesTheFiringBack(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev Claude", "Dev")
	r := w.make("Analise", "Dev", every(time.Hour), func(c *rituals.CreateRequest) {
		c.Prompt, c.PreRun = "Analise: {{output}}", "npm test 2>&1 | tail -1"
	})
	w.pre.set(rituals.PreRunResult{Output: "3 falhas\n"})
	w.goTo(w.at(10, 20))
	w.tick()
	fs := w.firings(r)
	if len(fs) != 1 || fs[0].State != rituals.Delivered {
		t.Fatalf("firings = %s; want delivered", states(fs))
	}
	items := w.items(dev, inbox.KindRitual)
	if len(items) != 1 || items[0].Text != "Analise: 3 falhas" {
		t.Fatalf("the target received %+v; want %q", items, "Analise: 3 falhas")
	}
	calls := w.pre.ran()
	got := w.char("Dev")
	if len(calls) != 1 || calls[0].Command != "npm test 2>&1 | tail -1" || calls[0].Machine != got.Machine || calls[0].Dir != got.Dir ||
		calls[0].Timeout != 60*time.Second || calls[0].MaxOutput != 16<<10 {
		t.Errorf("the pre-run ran as %+v; want the command on the machine and in the folder of the target, 60 s, 16 KiB", calls)
	}
	w.perm.mu.Lock()
	asked, whose := w.perm.asked, w.perm.target
	w.perm.mu.Unlock()
	if len(asked) != 1 || asked[0] != "npm test 2>&1 | tail -1" || whose[0] != "Dev" {
		t.Errorf("permissions decided %q for %q; want the command for the target", asked, whose)
	}

	// The pre-run fails with code 1.
	w.state(dev, characters.Idle)
	w.pre.set(rituals.PreRunResult{ExitCode: 1, Stderr: "boom: npm not found\n"})
	w.goTo(w.at(11, 20))
	w.tick()
	fs = w.firings(r)
	if len(fs) != 2 || fs[1].State != rituals.Failed {
		t.Fatalf("firings = %s; want the second failed", states(fs))
	}
	if n := len(w.items(dev, inbox.KindRitual)); n != 1 {
		t.Errorf("the target has %d items; a failed pre-run delivers nothing", n)
	}
	back := w.items(w.master, inbox.KindRitual)
	if len(back) != 1 || !strings.Contains(back[0].Text, "código 1") || !strings.Contains(back[0].Text, "boom: npm not found") || !strings.Contains(back[0].Text, "Analise") {
		t.Errorf("the owner got %+v; want the failure with the code and the tail of the error output", back)
	}
}

// CA-RIT-04: with `--every 1h` at :20 and the engine down from 10:10 to 10:40,
// when it is back the 10:20 firing happens once (20 minutes late, less than 30);
// with the engine down from 10:10 to 13:00, no missed firing happens, all are
// skipped and the owner is told.
func TestAfterTheEngineWasDownOnlyTheLatestSlotFiresAndOnlyIfNotTooLate(t *testing.T) {
	t.Parallel()
	t.Run("20 minutes late", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		dev := w.recruit("Dev Claude", "Dev")
		r := w.make("Hora", "Dev", every(time.Hour))
		w.goTo(w.at(10, 40)) // the engine was down from 10:10 and is back
		w.tick()
		w.tick()
		fs := w.firings(r)
		if len(fs) != 1 || fs[0].State != rituals.Delivered || !fs[0].ScheduledFor.Equal(w.at(10, 20)) {
			t.Fatalf("firings = %s; want one delivered for 10:20", states(fs))
		}
		if n := len(w.items(dev, inbox.KindRitual)); n != 1 {
			t.Errorf("the target got %d items; want 1", n)
		}
		got, _ := w.orc.Rituals.Store().Ritual(w.ctx, r.ID)
		if !got.NextRun.Equal(w.at(11, 20)) {
			t.Errorf("next run = %s; want 11:20", got.NextRun)
		}
	})
	t.Run("3 hours late", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		dev := w.recruit("Dev Claude", "Dev")
		r := w.make("Hora", "Dev", every(time.Hour))
		w.goTo(w.at(13, 0))
		w.tick()
		fs := w.firings(r)
		if len(fs) != 3 {
			t.Fatalf("firings = %s; want the three missed slots (10:20, 11:20, 12:20)", states(fs))
		}
		for _, f := range fs {
			if f.State != rituals.Skipped {
				t.Errorf("firing %s is %s; want skipped", f.ScheduledFor.Format("15:04"), f.State)
			}
		}
		if n := len(w.items(dev, inbox.KindRitual)); n != 0 {
			t.Errorf("the target got %d items; nothing fires", n)
		}
		told := w.items(w.master, inbox.KindRitual)
		if len(told) != 1 || !strings.Contains(told[0].Text, "perdeu 3 disparo") || !strings.Contains(told[0].Text, "12:20") {
			t.Errorf("the owner got %+v; want the notice of the three missed firings", told)
		}
		got, _ := w.orc.Rituals.Store().Ritual(w.ctx, r.ID)
		if !got.NextRun.Equal(w.at(13, 20)) {
			t.Errorf("next run = %s; want 13:20", got.NextRun)
		}
	})
}

// CA-RIT-05: with a firing still in the queue, when the next time comes the new
// one is skipped.
func TestAFiringStillInTheQueueSkipsTheNextOne(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev Claude", "Dev")
	w.state(dev, characters.Busy)
	r := w.make("Hora", "Dev", every(time.Hour), func(c *rituals.CreateRequest) { c.Timeout = time.Hour })
	w.goTo(w.at(10, 20))
	w.tick()
	w.goTo(w.at(11, 10))
	w.tick()
	if fs := w.firings(r); len(fs) != 1 || fs[0].State != rituals.Queued {
		t.Fatalf("before the next time: %s; want the first still in the queue", states(fs))
	}
	w.goTo(w.at(11, 20))
	w.tick()
	fs := w.firings(r)
	if len(fs) != 2 || fs[0].State != rituals.Expired || fs[1].State != rituals.Skipped {
		t.Fatalf("firings = %s; want the second skipped", states(fs))
	}
	if !strings.Contains(fs[1].Reason, "fila") {
		t.Errorf("reason of the skipped = %q", fs[1].Reason)
	}
	// Free again: the skipped firing is not made up for.
	w.state(dev, characters.Idle)
	w.tick()
	if n := len(w.items(dev, inbox.KindRitual)); n > 1 {
		t.Errorf("the target got %d items; the skipped one is not delivered", n)
	}
}

// The hour of a ritual is the one of the time zone of the Guild when the
// workspace has none of its own: 02:00 in São Paulo is 05:00 UTC
// (docs/orquestracao/rituais.md).
// CA-RIT-10
func TestTheHourOfARitualFollowsTheTimeZoneOfTheGuild(t *testing.T) {
	t.Parallel()
	if _, err := time.LoadLocation("America/Sao_Paulo"); err != nil {
		t.Skip("no tzdata")
	}
	w := newWorld(t, worldOpts{guildZone: "America/Sao_Paulo"})
	w.recruit("Dev Claude", "Dev")
	r := w.make("Testes da noite", "Dev", daily(2, 0))
	if want := time.Date(2026, 10, 16, 5, 0, 0, 0, time.UTC); !r.NextRun.Equal(want) {
		t.Fatalf("next run = %s; want %s (02:00 in São Paulo)", r.NextRun, want)
	}
}
