package ghcli_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/epicdeps"
)

// fakeDeps is the dependency between epics of the tests.
type fakeDeps struct {
	mu     sync.Mutex
	made   [][2]int
	closed []int
	// notPlanned are the closings told as not planned
	notPlanned []int
	released   [][2]int
	// releaseErr is what Release answers (nil: it released)
	releaseErr error
	err        error
	// failOn makes the dependency on that epic fail
	failOn int
}

func (f *fakeDeps) Depend(_ context.Context, by characters.Character, epic, on int) (epicdeps.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if by.Role != characters.RoleGrandMaster {
		return epicdeps.Result{}, epicdeps.ErrNotGrandMaster
	}
	if f.err != nil || (f.failOn != 0 && f.failOn == on) {
		if f.err == nil {
			return epicdeps.Result{}, errors.New("the engine failed")
		}
		return epicdeps.Result{}, f.err
	}
	f.made = append(f.made, [2]int{epic, on})
	return epicdeps.Result{State: epicdeps.Pending, Waiting: true, New: true}, nil
}

func (f *fakeDeps) Release(_ context.Context, _ characters.Character, epic, on int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.releaseErr != nil {
		return f.releaseErr
	}
	f.released = append(f.released, [2]int{epic, on})
	return nil
}

func (f *fakeDeps) EpicClosed(_ context.Context, _ string, epic int, notPlanned bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if notPlanned {
		f.notPlanned = append(f.notPlanned, epic)
		return nil
	}
	f.closed = append(f.closed, epic)
	return nil
}

func epicRig(t *testing.T) (*rig, *fakeDeps) {
	t.Helper()
	r := newRig(t)
	d := &fakeDeps{}
	r.h.Deps = d
	return r, d
}

// CA-DEPMESA-05 (the command: only the Grão-Mestre; the dependency is the engine's
// and a ring or an epic that is not one stops the command before GitHub is
// touched) and the epics of the Grão-Mestre of docs/cli/grao-mestre.md
func TestOnlyTheGrandMasterWorksOnEpicsAndTheMestreDaMesaIsRefused(t *testing.T) {
	t.Parallel()
	r, deps := epicRig(t)
	for _, args := range [][]string{{"create"}, {"update", "1"}, {"list"}} {
		got := r.run("tm", "gh epic", map[string]any{"title": "x", "depends-on": []any{"2"}}, args...)
		if got.OK || got.Exit != 3 || got.Code != "level_denied" {
			t.Errorf("gh epic %v by a Mestre da mesa: %+v, want exit 3 level_denied", args, got)
		}
	}
	if len(deps.made) != 0 {
		t.Fatalf("a dependency was declared by a Mestre da mesa: %v", deps.made)
	}
}

func TestAnEpicIsCreatedWithTheLabelAndListedAndUpdated(t *testing.T) {
	t.Parallel()
	r, deps := epicRig(t)
	got := r.run("gm", "gh epic", map[string]any{"title": "Checkout", "body": "pagar", "label": []any{"area:loja"}}, "create")
	if !got.OK {
		t.Fatalf("create: %+v", got)
	}
	n := int(got.Data["number"].(float64))
	if _, body, _, labels, ok := r.gh.Issue(repoFull, n); !ok || !slices.Contains(labels, github.EpicLabel) || !slices.Contains(labels, "area:loja") || body == "" {
		t.Fatalf("the epic #%d has labels %v, body %q", n, labels, body)
	}
	r.gh.AddIssue(repoFull, "uma issue comum")
	list := r.run("gm", "gh epic", nil, "list")
	if !list.OK {
		t.Fatalf("list: %+v", list)
	}
	epics := list.Data["epics"].([]any)
	if len(epics) != 2 { // the one of the rig and the new one
		t.Fatalf("list = %v, want the 2 epics and not the plain issue", epics)
	}
	got = r.run("gm", "gh epic", map[string]any{"title": "Checkout v2", "state": "closed"}, "update", itoa(n))
	if !got.OK {
		t.Fatalf("update: %+v", got)
	}
	if title, _, state, _, _ := r.gh.Issue(repoFull, n); title != "Checkout v2" || state != "closed" {
		t.Fatalf("the epic is %q (%s), want the new title and closed", title, state)
	}
	if !slices.Equal(deps.closed, []int{n}) {
		t.Fatalf("the dependencies were told of the closing of %v, want [%d]", deps.closed, n)
	}
}

func TestADependencyIsDeclaredByTheEngineAndARingOrAStrangerStopsBeforeGitHub(t *testing.T) {
	t.Parallel()
	r, deps := epicRig(t)
	e12 := int(r.run("gm", "gh epic", map[string]any{"title": "Login"}, "create").Data["number"].(float64))
	e15 := int(r.run("gm", "gh epic", map[string]any{"title": "Checkout"}, "create").Data["number"].(float64))
	plain := r.gh.AddIssue(repoFull, "não é épico")

	got := r.run("gm", "gh epic", map[string]any{"depends-on": []any{itoa(e12)}, "title": "Checkout 2"}, "update", itoa(e15))
	if !got.OK || !slices.Equal(deps.made, [][2]int{{e15, e12}}) {
		t.Fatalf("update --depends-on: %+v, made %v", got, deps.made)
	}
	if title, _, _, _, _ := r.gh.Issue(repoFull, e15); title != "Checkout 2" {
		t.Fatalf("the title was not changed: %q", title)
	}
	// a ring
	deps.err = &epicdeps.CycleError{Text: "#12 → #15 → #12"}
	got = r.run("gm", "gh epic", map[string]any{"depends-on": []any{itoa(e15)}, "title": "Nao muda"}, "update", itoa(e12))
	if got.OK || got.Code != "dependency_cycle" || got.Exit != 7 {
		t.Fatalf("a ring: %+v, want exit 7 dependency_cycle", got)
	}
	if title, _, _, _, _ := r.gh.Issue(repoFull, e12); title == "Nao muda" {
		t.Fatal("GitHub was changed by a command that was refused")
	}
	deps.err = nil
	// an epic that is not one, on either side
	for _, c := range []struct {
		n  int
		on []any
	}{{plain, []any{itoa(e12)}}, {e15, []any{itoa(plain)}}, {e15, []any{"9999"}}} {
		before := len(deps.made)
		got = r.run("gm", "gh epic", map[string]any{"depends-on": c.on}, "update", itoa(c.n))
		if got.OK || got.Code != "not_found" || len(deps.made) != before {
			t.Errorf("update %d --depends-on %v: %+v, made %v; want not_found and nothing kept", c.n, c.on, got, deps.made)
		}
	}
	// without the dependencies in this run the flag is refused, not ignored
	r.h.Deps = nil
	got = r.run("gm", "gh epic", map[string]any{"depends-on": []any{itoa(e12)}}, "update", itoa(e15))
	if got.OK || got.Code != "github_unavailable" && got.Code != "unavailable" {
		t.Fatalf("no dependencies in the run: %+v", got)
	}
}

func TestACommandThatFailsAfterTheDependenciesDropsTheOnesItMade(t *testing.T) {
	t.Parallel()
	r, deps := epicRig(t)
	e12 := int(r.run("gm", "gh epic", map[string]any{"title": "Login"}, "create").Data["number"].(float64))
	e13 := int(r.run("gm", "gh epic", map[string]any{"title": "Perfil"}, "create").Data["number"].(float64))
	e15 := int(r.run("gm", "gh epic", map[string]any{"title": "Checkout"}, "create").Data["number"].(float64))
	// the second dependency fails: the first, made by this command, is dropped
	deps.failOn = e13
	got := r.run("gm", "gh epic", map[string]any{"depends-on": []any{itoa(e12), itoa(e13)}, "title": "Nao muda"}, "update", itoa(e15))
	if got.OK {
		t.Fatalf("update with a dependency that failed: %+v", got)
	}
	if !slices.Equal(deps.released, [][2]int{{e15, e12}}) {
		t.Fatalf("released = %v, want the dependency this command made", deps.released)
	}
	if title, _, _, _, _ := r.gh.Issue(repoFull, e15); title == "Nao muda" {
		t.Fatal("GitHub was changed after a dependency failed")
	}
	// a nothing to change is a usage error, not an "updated"
	deps.failOn = 0
	if got := r.run("gm", "gh epic", nil, "update", itoa(e15)); got.OK || got.Code != "usage" {
		t.Fatalf("update with no flag: %+v", got)
	}
}

func TestAnEpicThatIsClosedAlreadyIsNotClosedAgainAndTheTablesAreToldHowItWasClosed(t *testing.T) {
	t.Parallel()
	r, deps := epicRig(t)
	e20 := int(r.run("gm", "gh epic", map[string]any{"title": "Velho"}, "create").Data["number"].(float64))
	r.gh.SetIssueState(repoFull, e20, "closed")
	if got := r.run("gm", "gh epic", map[string]any{"state": "closed"}, "update", itoa(e20)); !got.OK {
		t.Fatalf("closing an epic closed already: %+v", got)
	}
	if !slices.Equal(deps.closed, []int{e20}) {
		t.Fatalf("closed = %v, want the tables told once, as completed", deps.closed)
	}
	// without the dependencies of the run the epic is not closed at all
	r.h.Deps = nil
	e21 := int(r.run("gm", "gh epic", map[string]any{"title": "Aberto"}, "create").Data["number"].(float64))
	if got := r.run("gm", "gh epic", map[string]any{"state": "closed"}, "update", itoa(e21)); got.OK {
		t.Fatalf("closing with no dependencies in the run: %+v", got)
	}
	if _, _, state, _, _ := r.gh.Issue(repoFull, e21); state != "open" {
		t.Fatalf("the epic was closed on GitHub (%s) with nobody to tell the tables", state)
	}
}

func TestAPullRequestWithTheLabelOfTheEpicsIsNotAnEpic(t *testing.T) {
	t.Parallel()
	r, _ := epicRig(t)
	pr := r.gh.AddPR(repoFull, "feat: x", "branch-x", repoFull)
	r.gh.AddIssueLabel(repoFull, pr, github.EpicLabel)
	if got := r.run("gm", "gh epic", map[string]any{"title": "x"}, "update", itoa(pr)); got.OK || got.Code != "not_found" {
		t.Fatalf("update of a pull request: %+v", got)
	}
	for _, e := range r.run("gm", "gh epic", nil, "list").Data["epics"].([]any) {
		if int(e.(map[string]any)["number"].(float64)) == pr {
			t.Fatal("the pull request is listed as an epic")
		}
	}
}

// Part of CA-DEPMESA-05: `gh epic update --release <m>` drops the wait of the epic for
// another one (the Grão-Mestre decided to go on without it); what GitHub is asked to
// change goes first, an epic that does not wait for that one is not_found, and it needs
// the dependencies of the run.
func TestReleaseDropsTheWaitOfAnEpicForAnotherAndRefusesWhatDoesNotWait(t *testing.T) {
	t.Parallel()
	r, deps := epicRig(t)
	e12 := int(r.run("gm", "gh epic", map[string]any{"title": "Login"}, "create").Data["number"].(float64))
	e15 := int(r.run("gm", "gh epic", map[string]any{"title": "Checkout"}, "create").Data["number"].(float64))
	got := r.run("gm", "gh epic", map[string]any{"release": []any{"#" + itoa(e12)}, "title": "Checkout 2"}, "update", itoa(e15))
	if !got.OK || !slices.Equal(deps.released, [][2]int{{e15, e12}}) {
		t.Fatalf("update --release: %+v, released %v", got, deps.released)
	}
	if title, _, _, _, _ := r.gh.Issue(repoFull, e15); title != "Checkout 2" {
		t.Errorf("the title was not changed: %q", title)
	}
	// it does not wait for that one
	deps.releaseErr = epicdeps.ErrNoDependency
	if got = r.run("gm", "gh epic", map[string]any{"release": []any{itoa(e12)}}, "update", itoa(e15)); got.OK || got.Code != "not_found" {
		t.Errorf("release of a wait that does not exist: %+v", got)
	}
	// to wait and to be released in one command makes no sense
	deps.releaseErr = nil
	if got = r.run("gm", "gh epic", map[string]any{"release": []any{itoa(e12)}, "depends-on": []any{itoa(e12)}}, "update", itoa(e15)); got.OK {
		t.Errorf("depends-on and release of the same epic: %+v", got)
	}
	if got = r.run("gm", "gh epic", map[string]any{"release": []any{"x"}}, "update", itoa(e15)); got.OK {
		t.Errorf("a release that is not a number: %+v", got)
	}
	// without the dependencies of the run nothing is changed
	r.h.Deps = nil
	if got = r.run("gm", "gh epic", map[string]any{"release": []any{itoa(e12)}}, "update", itoa(e15)); got.OK {
		t.Errorf("release with no dependencies in the run: %+v", got)
	}
}

// Part of CA-DEPMESA-05: a release of a wait that is gone (the command was repeated) is
// not an error when the command changes something else, the same epic twice is one
// release, and the tables that wait for a closed epic are told all the same.
func TestAReleaseThatWasAlreadyDoneDoesNotStopTheRestOfTheCommand(t *testing.T) {
	t.Parallel()
	r, deps := epicRig(t)
	e12 := int(r.run("gm", "gh epic", map[string]any{"title": "Login"}, "create").Data["number"].(float64))
	e15 := int(r.run("gm", "gh epic", map[string]any{"title": "Checkout"}, "create").Data["number"].(float64))
	deps.releaseErr = epicdeps.ErrNoDependency
	got := r.run("gm", "gh epic", map[string]any{"release": []any{itoa(e12), itoa(e12)}, "state": "closed"}, "update", itoa(e15))
	if !got.OK || !slices.Equal(deps.closed, []int{e15}) {
		t.Fatalf("closing with a release already done: %+v, closed %v", got, deps.closed)
	}
	if nw, _ := got.Data["not_waiting"].([]any); len(nw) != 1 {
		t.Errorf("not_waiting = %v; want the epic once", got.Data["not_waiting"])
	}
}
