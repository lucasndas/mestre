package basesync_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

// fakeVerifier gives the results it was told to, one for each run (green when
// they end), and can hold a run until released.
type fakeVerifier struct {
	mu        sync.Mutex
	confirmed bool
	results   []quests.VerifyResult
	runs      int
	hold      chan struct{}
	started   chan struct{}
}

func (f *fakeVerifier) Confirmed(context.Context, string) (bool, error) { return f.confirmed, nil }

func (f *fakeVerifier) Verify(context.Context, quests.VerifyRequest) (quests.VerifyResult, error) {
	f.mu.Lock()
	f.runs++
	hold, started := f.hold, f.started
	f.started = nil
	res := quests.VerifyResult{OK: true}
	if len(f.results) > 0 {
		res, f.results = f.results[0], f.results[1:]
	}
	f.mu.Unlock()
	if started != nil {
		close(started)
	}
	if hold != nil {
		<-hold
	}
	return res, nil
}

func (f *fakeVerifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs
}

func red(out string, places ...string) quests.VerifyResult {
	return quests.VerifyResult{Command: "go test ./...", Output: out, Places: places}
}

// integrate puts the tip of the base in the branch of the table, as the merge
// of the Dev does, and commits the resolution.
func (w *world) integrate() {
	w.t.Helper()
	m := w.mirror()
	tip, err := m.ResolveRef(w.ctx, "refs/remotes/origin/"+w.table.BaseBranch)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := m.UpdateRef(w.ctx, "refs/heads/"+w.table.Branch, tip); err != nil {
		w.t.Fatal(err)
	}
	w.commit("merge: base", map[string]string{"merged.go": "package app\n"})
}

// syncWorld is a table whose base moved: the Dev has the step sync.
func syncWorld(t *testing.T, v *fakeVerifier) (*world, characters.Character) {
	t.Helper()
	w := newWorld(t, v)
	dev := w.devWorks()
	w.moveBase("login.go")
	if sq := w.syncQuests(); len(sq) != 1 || sq[0].State != quests.Running {
		t.Fatalf("missions of sync = %+v; want one running", sq)
	}
	return w, dev
}

// doneOfSync is the done of the Dev on the step sync, after it merged.
func (w *world) doneOfSync(dev characters.Character) quests.DoneResult {
	w.t.Helper()
	w.integrate()
	r := w.call(dev.ID, "done", nil, "base integrada")
	if !r.OK {
		w.t.Fatalf("done of the sync: %+v", r.Error)
	}
	var res quests.DoneResult
	if err := json.Unmarshal(r.Data, &res); err != nil {
		w.t.Fatal(err)
	}
	w.machines.Report(w.ctx, dev.ID, characters.Idle)
	return res
}

func (w *world) syncState() quests.Quest {
	w.t.Helper()
	sq := w.syncQuests()
	if len(sq) != 1 {
		w.t.Fatalf("missions of sync = %d", len(sq))
	}
	return sq[0]
}

func hasEvent(items []string, marker string) bool {
	for _, x := range items {
		if strings.Contains(x, marker) {
			return true
		}
	}
	return false
}

// CA-SYNC-03: the limit of returns pauses the mission, the Master of the table
// gets the return_limit and the table keeps working; the same failure again
// pauses before.
func TestTheLimitOfReturnsPausesTheSyncAndTheSameFailureAgainPausesBefore(t *testing.T) {
	t.Parallel()
	t.Run("limit", func(t *testing.T) {
		t.Parallel()
		v := &fakeVerifier{confirmed: true, results: []quests.VerifyResult{
			red("src/a.go:1: x", "src/a.go:1"), red("src/b.go:2: y", "src/b.go:2"), red("src/c.go:3: z", "src/c.go:3"), red("src/d.go:4: w", "src/d.go:4")}}
		w, dev := syncWorld(t, v)
		for i := 0; i < 3; i++ {
			w.doneOfSync(dev)
			w.orc.Quests.WaitVerifications()
			if got := w.syncState().State; got != quests.Running {
				t.Fatalf("the sync is %s after %d failures; want it back with the Dev", got, i+1)
			}
			if steps, err := w.orc.Quests.Store().Steps(w.ctx, w.syncState().ID); err != nil || len(steps) != 1 || steps[0].Returns != i+1 {
				t.Fatalf("steps after the failure %d = %+v, %v; want %d returns", i+1, steps, err, i+1)
			}
		}
		w.doneOfSync(dev)
		w.orc.Quests.WaitVerifications()
		q := w.syncState()
		if q.MaxReturns != 3 {
			t.Errorf("max_returns = %d; want the default 3", q.MaxReturns)
		}
		if q.State != quests.Paused || q.PausedReason != "limite de voltas" {
			t.Fatalf("the sync is %s (%q); want paused by the limit of returns", q.State, q.PausedReason)
		}
		if !hasEvent(w.itemsOf(w.master.ID, inbox.KindSystem), "[return_limit]") {
			t.Error("the Master of the table did not get the return_limit")
		}
		if got := w.tableNow().State; got != tables.StateWorking {
			t.Errorf("the table is %s; want working", got)
		}
	})
	t.Run("same failure", func(t *testing.T) {
		t.Parallel()
		same := red("src/a.go:7: x", "src/a.go:7")
		v := &fakeVerifier{confirmed: true, results: []quests.VerifyResult{same, same, same}}
		w, dev := syncWorld(t, v)
		for i := 0; i < 3; i++ {
			w.doneOfSync(dev)
			w.orc.Quests.WaitVerifications()
			if i < 2 {
				// the first two failures only give the step back to the Dev
				if got := w.syncState().State; got != quests.Running {
					t.Fatalf("the sync is %s after the failure %d; want it back with the Dev (it pauses at the third)", got, i+1)
				}
				if steps, err := w.orc.Quests.Store().Steps(w.ctx, w.syncState().ID); err != nil || len(steps) != 1 || steps[0].Returns != i+1 {
					t.Fatalf("steps after the failure %d = %+v, %v; want %d returns", i+1, steps, err, i+1)
				}
			}
		}
		if v.count() != 3 {
			t.Errorf("verifications = %d; want exactly 3 (it pauses at the third, not later)", v.count())
		}
		q := w.syncState()
		if q.State != quests.Paused || q.PausedReason != "mesma falha repetida" {
			t.Fatalf("the sync is %s (%q); want paused by the same failure", q.State, q.PausedReason)
		}
		if !hasEvent(w.itemsOf(w.master.ID, inbox.KindSystem), "[return_limit]") {
			t.Error("the Master of the table did not get the return_limit")
		}
	})
}

// CA-SYNC-05: the base moves again while the verification runs: the gate runs
// again before the end, and the step goes on; with no command confirmed the
// sync goes back to the Master of the table with the question.
func TestTheBaseMovingAgainRedoesTheGateAndNoCommandAsksTheMaster(t *testing.T) {
	t.Parallel()
	t.Run("base moves again", func(t *testing.T) {
		t.Parallel()
		v := &fakeVerifier{confirmed: true, hold: make(chan struct{}), started: make(chan struct{})}
		started := v.started
		w, dev := syncWorld(t, v)
		w.doneOfSync(dev)
		<-started
		w.moveBase("later.go") // a fetch while the verification runs
		close(v.hold)
		w.orc.Quests.WaitVerifications()
		if got := v.count(); got != 1 {
			t.Errorf("verifications = %d after the first round; want 1 (it ran and did not conclude)", got)
		}
		if got := w.syncState().State; got != quests.Running {
			t.Fatalf("the sync is %s; the base moved again, so it must not end", got)
		}
		if told := strings.Join(w.itemsOf(dev.ID, inbox.KindSystem), "\n"); !strings.Contains(told, "andou de novo") {
			t.Errorf("the Dev was not told that the base moved again:\n%s", told)
		}
		if got := len(w.syncQuests()); got != 1 {
			t.Errorf("missions of sync = %d; the signal during the step must be ignored", got)
		}
		w.doneOfSync(dev) // integrates the new tip and gives the done again
		w.orc.Quests.WaitVerifications()
		if got := v.count(); got != 2 {
			t.Errorf("verifications = %d; want 2 (the gate ran again before the end)", got)
		}
		if got := w.syncState().State; got != quests.Completed {
			t.Errorf("the sync is %s after the gate passed; want completed", got)
		}
	})
	t.Run("no command", func(t *testing.T) {
		t.Parallel()
		v := &fakeVerifier{confirmed: false}
		w, dev := syncWorld(t, v)
		res := w.doneOfSync(dev)
		if res.Verification != quests.VerificationNoCommand || res.QuestState != string(quests.Paused) {
			t.Fatalf("result = %+v; want the sync paused with no_command", res)
		}
		if v.count() != 0 {
			t.Error("it verified with no command")
		}
		q := w.syncState()
		if q.State != quests.Paused || q.PausedReason != quests.ReasonNoVerifyCommand {
			t.Errorf("the sync is %s (%q)", q.State, q.PausedReason)
		}
		if !hasEvent(w.itemsOf(w.master.ID, inbox.KindSystem), "quais comandos") {
			t.Error("the Master of the table was not asked which commands to use")
		}
		w.doneOfSync(dev) // a done again before the resume does not ask again
		asks := 0
		for _, x := range w.itemsOf(w.master.ID, inbox.KindSystem) {
			if strings.Contains(x, "quais comandos") {
				asks++
			}
		}
		if asks != 1 {
			t.Errorf("the Master was asked %d times; want once", asks)
		}
		v.mu.Lock()
		v.confirmed = true
		v.mu.Unlock()
		if _, err := w.orc.Quests.Resume(w.ctx, q.ID); err != nil {
			t.Fatal(err)
		}
		w.doneOfSync(dev)
		w.orc.Quests.WaitVerifications()
		if got := w.syncState().State; got != quests.Completed {
			t.Errorf("the sync is %s after the commands were confirmed; want completed", got)
		}
	})
}

// A done of the sync with the base not integrated is refused before any
// verification runs (7 conflict).
func TestADoneOfTheSyncWithoutTheBaseIsRefusedBeforeTheVerification(t *testing.T) {
	t.Parallel()
	v := &fakeVerifier{confirmed: true}
	w, dev := syncWorld(t, v)
	r := w.call(dev.ID, "done", nil, "sem integrar")
	if r.OK || r.Error == nil || r.Error.Code != "conflict" || r.ExitCode() != 7 {
		t.Fatalf("done = %+v; want exit 7 conflict", r)
	}
	w.orc.Quests.WaitVerifications()
	if v.count() != 0 {
		t.Error("a verification ran over a branch without the base")
	}
	if got := w.syncState().State; got != quests.Running {
		t.Errorf("the sync is %s", got)
	}
}
