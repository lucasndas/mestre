package github_test

import (
	"net/http"
	"testing"

	"github.com/mestre-ia/mestre/internal/github"
)

// The lock of the production is made over the repository the Guild recorded and
// the token of the App "Grão-Mestre" for that one repository, with the contents
// of it and nothing else: before the repository is recorded there is no lock.
func TestTheLockOfTheEpochIsMadeOverTheRecordedRepositoryWithTheTokenOfTheGrandMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	lock, err := github.NewLairLock(w.ctx, w.tokens, "01GUILD", w.srv.URL, http.DefaultClient)
	if err != nil || lock != nil {
		t.Fatalf("no repository recorded: lock %v, err %v", lock, err)
	}
	w.ready(github.App{Name: "lucas-mestre-grao-mestre", Role: github.RoleGrandMaster}, 1, 11, 100, true, lairRepo)
	w.gh.AddRepo("lucas", "mestre-guilda-abc12345")
	w.gh.AddRef(lairRepo, "refs/mestre/epoca-3", "machine a, epoch 3")
	if err := w.apps.SetLair(w.ctx, lairRepo); err != nil {
		t.Fatal(err)
	}
	if lock, err = github.NewLairLock(w.ctx, w.tokens, "01GUILD", w.srv.URL, http.DefaultClient); err != nil || lock == nil {
		t.Fatalf("lock %v, err %v", lock, err)
	}
	n, err := lock.Check(w.ctx)
	if err != nil || n != 3 {
		t.Fatalf("Check = %d, %v, want the epoch 3", n, err)
	}
	reqs := w.gh.TokenRequests()
	if len(reqs) != 1 || len(reqs[0].Repositories) != 1 || reqs[0].Repositories[0] != "mestre-guilda-abc12345" {
		t.Fatalf("the token was asked for %+v, want only the repository of the lock", reqs)
	}
	if p := reqs[0].Permissions; p["contents"] != "write" || p["administration"] != "" || p["issues"] != "" || p["pull_requests"] != "" {
		t.Errorf("permissions asked: %v", p)
	}
	won, err := lock.Claim(w.ctx, 4, "machine-b")
	if err != nil || !won {
		t.Fatalf("Claim(4) = %v, %v", won, err)
	}
	if n, err := lock.Check(w.ctx); err != nil || n != 4 {
		t.Errorf("Check after the claim = %d, %v", n, err)
	}
}

// Without the App "Grão-Mestre" ready the lock is not reachable: GitHub fora,
// never a higher epoch, never a silent success.
func TestTheLockOfTheEpochFailsClosedWithoutTheGrandMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if err := w.apps.SetLair(w.ctx, lairRepo); err != nil {
		t.Fatal(err)
	}
	lock, err := github.NewLairLock(w.ctx, w.tokens, "01GUILD", w.srv.URL, http.DefaultClient)
	if err != nil || lock == nil {
		t.Fatalf("lock %v, err %v", lock, err)
	}
	if n, err := lock.Check(w.ctx); err == nil || n != 0 {
		t.Errorf("Check with no App = %d, %v", n, err)
	}
	if won, err := lock.Claim(w.ctx, 1, "machine-b"); err == nil || won {
		t.Errorf("Claim with no App = %v, %v", won, err)
	}
}
