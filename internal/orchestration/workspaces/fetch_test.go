package workspaces_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
)

var errOffline = errors.New("fatal: unable to access 'https://github.com/lucas/Meu_App.git/': Could not resolve host: github.com")

// CA-WS-03
func TestMirrorIsFetchedEvery15MinutesAndAnOutageOf1HourAlertsOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.active() // the mirror stage fetched at t0
	base := r.fetchCount()

	// Fifteen minutes without events: the fetch is scheduled, not before.
	r.runUntil(r.svc, t0.Add(15*time.Minute-time.Second))
	if got := r.fetchCount(); got != base {
		t.Fatalf("fetches after 14m59s = %d; want %d", got, base)
	}
	if next, ok, err := r.svc.Tick(r.ctx); err != nil || !ok || next != t0.Add(15*time.Minute) {
		t.Fatalf("next thing due = %v, %v, %v; want the fetch at t0+15m", next, ok, err)
	}
	r.runUntil(r.svc, t0.Add(15*time.Minute))
	if got := r.fetchCount(); got != base+1 {
		t.Fatalf("fetches at 15m = %d; want %d", got, base+1)
	}

	// The fetch fails from the next one on, for an hour.
	r.setFetchErr(errOffline)
	outage := t0.Add(30 * time.Minute)
	r.runUntil(r.svc, outage)
	if got := r.get(w.Slug).Fetch.FailingSince; got != outage {
		t.Fatalf("failing since %v; want %v", got, outage)
	}
	r.runUntil(r.svc, outage.Add(time.Hour-time.Second))
	if got := r.alerts(); len(got) != 0 {
		t.Fatalf("alerts before the hour = %+v", got)
	}
	r.runUntil(r.svc, outage.Add(time.Hour))
	got := r.alerts()
	if len(got) != 1 || got[0].Kind != workspaces.AlertFetchFailing || got[0].Workspace != w.Slug ||
		!strings.Contains(got[0].Detail, "Could not resolve host") || !got[0].Since.Equal(outage) {
		t.Fatalf("alerts at the hour = %+v; want one about the failing fetch since %v", got, outage)
	}
	// And only once, however long it goes on.
	r.runUntil(r.svc, outage.Add(6*time.Hour))
	if got := r.alerts(); len(got) != 1 {
		t.Fatalf("alerts after 6 h = %d; want the single one", len(got))
	}
}

func TestFetchFailuresBackOffFrom30SecondsTo15Minutes(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.active()
	r.setFetchErr(errOffline)
	r.runUntil(r.svc, t0.Add(15*time.Minute+3*time.Hour))
	at := r.attempts()[1:] // from the first failure, after the fetch of the creation
	var gaps []time.Duration
	for i := 1; i < len(at) && i <= 8; i++ {
		gaps = append(gaps, at[i].Sub(at[i-1]))
	}
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	if !slices.Equal(gaps, want) {
		t.Fatalf("gaps between the attempts of an outage = %v; want %v", gaps, want)
	}
}

func TestRecoveryEndsTheOutageAndANewOutageAlertsAgain(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.active()
	r.setFetchErr(errOffline)
	r.runUntil(r.svc, t0.Add(15*time.Minute+time.Hour+time.Minute)) // first failure at 15m, the alert at 1h15m
	if len(r.alerts()) != 1 {
		t.Fatalf("alerts = %d; want 1", len(r.alerts()))
	}
	r.setFetchErr(nil)
	r.runUntil(r.svc, r.clk.Now().Add(16*time.Minute)) // the next attempt gets through
	got := r.get(w.Slug).Fetch
	if !got.FailingSince.IsZero() || got.Alerted || got.Error != "" || got.LastAt.IsZero() {
		t.Fatalf("after the recovery: %+v", got)
	}
	r.setFetchErr(errOffline)
	r.runUntil(r.svc, r.clk.Now().Add(16*time.Minute+time.Hour+time.Minute))
	if len(r.alerts()) != 2 {
		t.Fatalf("alerts after a second outage = %d; want 2", len(r.alerts()))
	}
}

func TestAlertThatFailsToSendIsTriedAgainAndThenSentOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.active()
	r.setFetchErr(errOffline)
	r.mu.Lock()
	r.alertErr = errors.New("telegram is down")
	r.mu.Unlock()
	r.runUntil(r.svc, t0.Add(15*time.Minute+time.Hour+10*time.Minute))
	if len(r.alerts()) != 0 {
		t.Fatalf("alerts = %+v while the channel is down", r.alerts())
	}
	r.mu.Lock()
	r.alertErr = nil
	r.mu.Unlock()
	r.runUntil(r.svc, r.clk.Now().Add(2*time.Minute))
	r.runUntil(r.svc, r.clk.Now().Add(2*time.Hour))
	if len(r.alerts()) != 1 {
		t.Fatalf("alerts = %d; want exactly one once the channel is back", len(r.alerts()))
	}
}

func TestOutageAndItsAlertSurviveARestartOfTheCitadel(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.active()
	r.setFetchErr(errOffline)
	r.runUntil(r.svc, t0.Add(15*time.Minute+time.Hour+time.Minute))
	since := r.get(w.Slug).Fetch.FailingSince
	if len(r.alerts()) != 1 || since.IsZero() {
		t.Fatalf("alerts = %d, failing since %v", len(r.alerts()), since)
	}
	// A new process over the same database: it fetches at once (the failing
	// mirror is due), keeps the outage's start and does not alert again.
	svc := r.newService(r.db)
	r.runUntil(svc, r.clk.Now().Add(3*time.Hour))
	if got := r.get(w.Slug).Fetch.FailingSince; got != since {
		t.Fatalf("failing since after the restart = %v; want %v", got, since)
	}
	if len(r.alerts()) != 1 {
		t.Fatalf("alerts after the restart = %d; want still 1", len(r.alerts()))
	}
}

func TestPokeFetchesAtOnceAndFetchResetsTheClock(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.active()
	base := r.fetchCount()
	tip := r.push("feat: from the events", map[string]string{"a.txt": "a"})
	r.clk.Advance(5 * time.Minute)
	r.svc.Poke(w.Slug)
	if _, _, err := r.svc.Tick(r.ctx); err != nil {
		t.Fatal(err)
	}
	if r.fetchCount() != base+1 || r.head(w.Slug) != tip {
		t.Fatalf("after the poke: %d fetches, head %s; want %d and the new tip %s", r.fetchCount()-base, r.head(w.Slug), 1, tip)
	}
	// The next regular fetch is 15 minutes after that one, not after the first.
	r.runUntil(r.svc, r.clk.Now().Add(15*time.Minute-time.Second))
	if r.fetchCount() != base+1 {
		t.Fatalf("a regular fetch came %d too early", r.fetchCount()-base-1)
	}
	r.runUntil(r.svc, r.clk.Now().Add(time.Second))
	if r.fetchCount() != base+2 {
		t.Fatalf("fetches = %d; want the regular one 15 minutes after the poke", r.fetchCount()-base)
	}
	if err := r.svc.Fetch(r.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Fetch(r.ctx, "nope"); !errors.Is(err, workspaces.ErrNotFound) {
		t.Fatalf("Fetch of a missing workspace = %v", err)
	}
}

func TestFetchReturnsTheFailureToWhoAsked(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.active()
	r.setFetchErr(errOffline)
	if err := r.svc.Fetch(r.ctx, w.Slug); !errors.Is(err, errOffline) {
		t.Fatalf("Fetch = %v; want the failure", err)
	}
	if f := r.get(w.Slug).Fetch; f.FailingSince != t0 || !strings.Contains(f.Error, "Could not resolve host") {
		t.Fatalf("fetch status = %+v", f)
	}
}

func TestGateHoldsBackEveryFetch(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.active()
	base := r.fetchCount()
	var mu sync.Mutex
	acting := false
	svc, err := workspaces.New(workspaces.Options{
		DB: r.db, Git: &spyGit{Git: r.git, r: r}, Layout: r.svc.Layout(), Clock: r.clk, GitHub: (*ghFake)(r),
		Alerts: (*alertFake)(r), GrandMaster: (*gmFake)(r),
		Gate: func() error {
			mu.Lock()
			defer mu.Unlock()
			if !acting {
				return errors.New("the Citadel is not acting")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.clk.Advance(time.Hour)
	if _, ok, err := svc.Tick(r.ctx); err != nil || !ok || r.fetchCount() != base {
		t.Fatalf("Tick while the Citadel does not act: ok %v err %v, %d fetches", ok, err, r.fetchCount()-base)
	}
	if err := svc.Fetch(r.ctx, "meu-app"); err == nil {
		t.Fatal("Fetch went through the gate")
	}
	mu.Lock()
	acting = true
	mu.Unlock()
	if _, _, err := svc.Tick(r.ctx); err != nil || r.fetchCount() != base+1 {
		t.Fatalf("Tick once it acts: %v, %d fetches", err, r.fetchCount()-base)
	}
}

// Run sleeps on the clock until the next fetch and does it.
func TestRunFetchesWhenTheIntervalPasses(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.active()
	base := r.fetchCount()
	ctx, cancel := context.WithCancel(r.ctx)
	done := make(chan error, 1)
	go func() { done <- r.svc.Run(ctx) }()
	r.clk.BlockUntil(1) // Run waits for the 15 minutes
	if r.fetchCount() != base {
		t.Fatalf("Run fetched before its time")
	}
	r.clk.Advance(15 * time.Minute)
	<-r.fetched
	for r.fetchCount() == base { // the signal can be the one of the creation
		<-r.fetched
	}
	r.clk.BlockUntil(1)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// CA-WS-06
func TestMainCloneAdvancesWithFFOnlyOnlyWhenTheGrandMasterIsIdle(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.active()
	before := r.head(w.Slug)
	r.setGM(workspaces.GMBusy)
	tip := r.push("feat: ahead", map[string]string{"a.txt": "a"})

	if err := r.svc.Fetch(r.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	mirror, err := r.git.Open(r.ctx, r.svc.Layout().Mirror(w.Slug))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := mirror.ResolveRef(r.ctx, "refs/remotes/origin/main"); err != nil || got != tip {
		t.Fatalf("origin/main in the mirror = %s, %v; the fetch should bring %s", got, err, tip)
	}
	if got := r.head(w.Slug); got != before || len(r.mergeRefs()) != 0 {
		t.Fatalf("with the Grand Master busy: head %s (was %s), merges %v; want the main clone untouched", got, before, r.mergeRefs())
	}

	// The Grand Master becomes idle; the next fetch (15 minutes later) ends
	// and the engine asks for the fast-forward onto origin/main.
	r.setGM(workspaces.GMIdle)
	r.runUntil(r.svc, r.clk.Now().Add(15*time.Minute))
	if got := r.mergeRefs(); !slices.Equal(got, []string{"refs/remotes/origin/main"}) {
		t.Fatalf("merges asked = %v; want one ff-only merge of origin/main", got)
	}
	if got := r.head(w.Slug); got != tip {
		t.Fatalf("head = %s; want the tip %s", got, tip)
	}
	if a := r.get(w.Slug).Advance; !a.BlockedSince.IsZero() || a.Reason != "" {
		t.Fatalf("advance status after catching up = %+v", a)
	}
}

func TestMainCloneAdvancesWhenTheGrandMasterIsWaitingOrThereIsNone(t *testing.T) {
	t.Parallel()
	for _, state := range []workspaces.GrandMasterState{workspaces.GMWaiting, workspaces.GMStopped, workspaces.GMNone} {
		r := newRig(t)
		w := r.active()
		r.setGM(state)
		tip := r.push("feat: ahead", map[string]string{"a.txt": "a"})
		if err := r.svc.Fetch(r.ctx, w.Slug); err != nil {
			t.Fatal(err)
		}
		if got := r.head(w.Slug); got != tip {
			t.Errorf("Grand Master %q: head = %s; want %s", state, got, tip)
		}
	}
}

func TestMainCloneStaysWhenTheGrandMasterIsNotStill(t *testing.T) {
	t.Parallel()
	for _, state := range []workspaces.GrandMasterState{"starting", "blocked", "interrupted"} {
		r := newRig(t)
		w := r.active()
		before := r.head(w.Slug)
		r.setGM(state)
		r.push("feat: ahead", map[string]string{"a.txt": "a"})
		if err := r.svc.Fetch(r.ctx, w.Slug); err != nil {
			t.Fatal(err)
		}
		if got := r.head(w.Slug); got != before {
			t.Errorf("Grand Master %q: the main clone moved to %s", state, got)
		}
	}
}

func TestMainCloneWithLocalChangesOrOwnCommitsIsNotAdvanced(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.active()
	before := r.head(w.Slug)
	tip := r.push("feat: ahead", map[string]string{"a.txt": "a"})

	r.principal(w.Slug).SetFile("notes.md", "work in progress")
	if err := r.svc.Fetch(r.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	a := r.get(w.Slug).Advance
	if r.head(w.Slug) != before || !strings.Contains(a.Reason, "alterações locais") || a.BlockedSince != t0 {
		t.Fatalf("with a dirty clone: head %s, advance %+v; want it held back for the changes", r.head(w.Slug), a)
	}
	r.principal(w.Slug).RemoveFile("notes.md")
	r.clk.Advance(time.Minute)
	if err := r.svc.Fetch(r.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	if r.head(w.Slug) != tip {
		t.Fatalf("clean again: head %s; want %s", r.head(w.Slug), tip)
	}

	// A commit made in the main clone that origin does not have: the merge
	// is not a fast-forward and nothing is forced.
	own := r.principal(w.Slug).Data().Commit("main", "chore: local", map[string]string{"local.txt": "l"})
	r.push("feat: ahead again", map[string]string{"b.txt": "b"})
	if err := r.svc.Fetch(r.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	if a := r.get(w.Slug).Advance; r.head(w.Slug) != own || !strings.Contains(a.Reason, "não é um avanço simples") {
		t.Fatalf("diverged clone: head %s, advance %+v; want it held back, not forced", r.head(w.Slug), a)
	}
}

func TestGrandMasterIsToldAfterAnHourOfTheAdvanceHeldBackOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.active()
	r.setGM(workspaces.GMBusy)
	r.push("feat: ahead", map[string]string{"a.txt": "a"})
	if err := r.svc.Fetch(r.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	held := r.get(w.Slug).Advance.BlockedSince
	if held != t0 {
		t.Fatalf("held since %v; want %v", held, t0)
	}
	r.runUntil(r.svc, held.Add(time.Hour-time.Second))
	if got := r.items(); len(got) != 0 {
		t.Fatalf("items system before the hour = %q", got)
	}
	r.runUntil(r.svc, held.Add(time.Hour))
	got := r.items()
	if len(got) != 1 || !strings.Contains(got[0], "Grão-Mestre está trabalhando (busy)") || !strings.Contains(got[0], "origin/main") {
		t.Fatalf("items system at the hour = %q; want one with the reason", got)
	}
	r.runUntil(r.svc, held.Add(5*time.Hour))
	if len(r.items()) != 1 {
		t.Fatalf("items system after 5 h = %d; want the single one", len(r.items()))
	}
	// Once it advances, the hold and its alert are forgotten.
	r.setGM(workspaces.GMIdle)
	r.runUntil(r.svc, r.clk.Now().Add(15*time.Minute))
	if a := r.get(w.Slug).Advance; !a.BlockedSince.IsZero() || a.Alerted {
		t.Fatalf("advance status after catching up = %+v", a)
	}
}

func TestMainCloneThatNoLongerPointsAtItsRecordIsNotUsedAndAlertsOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.active()
	before := r.head(w.Slug)
	r.mu.Lock()
	r.moved = true
	r.mu.Unlock()
	r.push("feat: ahead", map[string]string{"a.txt": "a"})
	for range 3 {
		if err := r.svc.Fetch(r.ctx, w.Slug); err != nil {
			t.Fatal(err)
		}
	}
	got := r.alerts()
	if len(got) != 1 || got[0].Kind != workspaces.AlertWorktreeMoved || got[0].Workspace != w.Slug {
		t.Fatalf("alerts = %+v; want one about the main clone that moved", got)
	}
	if r.head(w.Slug) != before || len(r.mergeRefs()) != 0 {
		t.Fatalf("a main clone that moved was used: head %s, merges %v", r.head(w.Slug), r.mergeRefs())
	}
	if a := r.get(w.Slug).Advance; !strings.Contains(a.Reason, "não aponta mais") {
		t.Fatalf("advance status = %+v", a)
	}
}
