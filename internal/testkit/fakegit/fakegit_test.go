package fakegit_test

import (
	"errors"
	"testing"

	"github.com/lucasndas/mestre/internal/testkit/fakegit"
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
	core, node := fakegit.New(), fakegit.New()
	core.Commit("mesa", "a", map[string]string{"a": "1"})
	b, err := core.Bundle("mesa")
	if err != nil {
		t.Fatal(err)
	}
	if err := node.Fetch(b); err != nil {
		t.Fatal(err)
	}
	node.Commit("mesa", "b", map[string]string{"b": "2"})
	back, _ := node.Bundle("mesa")
	if err := core.Fetch(back); err != nil {
		t.Fatalf("fast-forward refused: %v", err)
	}
	core.Commit("mesa", "c", map[string]string{"c": "3"})
	node.Commit("mesa", "d", map[string]string{"d": "4"})
	diverged, _ := node.Bundle("mesa")
	if err := core.Fetch(diverged); !errors.Is(err, fakegit.ErrNotFastForward) {
		t.Fatalf("diverged fetch: %v", err)
	}
}
