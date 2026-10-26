package fakegit_test

import (
	"errors"
	"testing"

	"github.com/mestre-ia/mestre/internal/testkit/fakegit"
)

func TestCommitsWorktreesAndExclude(t *testing.T) {
	t.Parallel()
	r := fakegit.New()
	r.Commit("main", "init", map[string]string{"README.md": "hi"})
	if err := r.Branch("mesa/12-login", "main"); err != nil {
		t.Fatal(err)
	}
	h := r.Commit("mesa/12-login", "login", map[string]string{"login.go": "package x", "README.md": ""})
	if got, err := r.ReadFile(string(h), "login.go"); err != nil || got != "package x" {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}
	if _, err := r.ReadFile("mesa/12-login", "README.md"); !errors.Is(err, fakegit.ErrNoSuchFile) {
		t.Fatalf("deleted file still there: %v", err)
	}
	if err := r.AddWorktree("/w/12", "mesa/12-login"); err != nil {
		t.Fatal(err)
	}
	if err := r.AddWorktree("/w/other", "mesa/12-login"); !errors.Is(err, fakegit.ErrBranchInUse) {
		t.Fatalf("second checkout: %v", err)
	}
	r.AddExclude(".mestre/")
	r.AddExclude(".mestre/")
	if ex := r.Exclude(); len(ex) != 1 {
		t.Fatalf("exclude = %v", ex)
	}
}

func TestBundleFetchFastForwardOnly(t *testing.T) {
	t.Parallel()
	citadel, tower := fakegit.New(), fakegit.New()
	citadel.Commit("mesa", "a", map[string]string{"a": "1"})
	b, err := citadel.Bundle("mesa")
	if err != nil {
		t.Fatal(err)
	}
	if err := tower.Fetch(b); err != nil {
		t.Fatal(err)
	}
	tower.Commit("mesa", "b", map[string]string{"b": "2"})
	back, _ := tower.Bundle("mesa")
	if err := citadel.Fetch(back); err != nil {
		t.Fatalf("fast-forward refused: %v", err)
	}
	citadel.Commit("mesa", "c", map[string]string{"c": "3"})
	tower.Commit("mesa", "d", map[string]string{"d": "4"})
	diverged, _ := tower.Bundle("mesa")
	if err := citadel.Fetch(diverged); !errors.Is(err, fakegit.ErrNotFastForward) {
		t.Fatalf("diverged fetch: %v", err)
	}
}
