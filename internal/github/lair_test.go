package github_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/github"
)

const guildID = "01M4ZPXYG0PNPDZ5JN7697ZSCH"

func newLair(w *world) *github.Lair {
	return &github.Lair{Apps: w.apps, BaseURL: w.srv.URL}
}

// CA-GH-ID-10
func TestTheRepositoryOfTheLockIsMadePrivateWithAutoInitAndRecorded(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.ready(github.App{Name: "lucas-mestre-grao-mestre", Role: github.RoleGrandMaster}, 1, 11, 100, true, "lucas/meu-app")
	tok, err := w.tokens.Installation(w.ctx, github.Request{App: "lucas-mestre-grao-mestre", Permissions: map[string]string{"administration": "write"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := w.apps.LairRepo(w.ctx); ok {
		t.Fatal("a repository of the lock before it was made")
	}
	full, err := newLair(w).Ensure(w.ctx, "lucas", guildID, tok.Secret())
	want := "lucas/mestre-guilda-" + strings.ToLower(guildID[len(guildID)-8:])
	if err != nil || full != want {
		t.Fatalf("Ensure = %q, %v; want %q", full, err, want)
	}
	if got, ok, _ := w.apps.LairRepo(w.ctx); !ok || got != want {
		t.Errorf("recorded %q, %v", got, ok)
	}
	// the second call finds it already made and is not an error
	if again, err := newLair(w).Ensure(w.ctx, "lucas", guildID, tok.Secret()); err != nil || again != want {
		t.Errorf("the second Ensure = %q, %v", again, err)
	}
}

func TestWhenGitHubRefusesTheCreationTheOwnerGetsTheLinkAndNoLockIsRecorded(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.ready(github.App{Name: "lucas-mestre-grao-mestre", Role: github.RoleGrandMaster}, 1, 11, 100, true, "lucas/meu-app")
	tok, _ := w.tokens.Installation(w.ctx, github.Request{App: "lucas-mestre-grao-mestre"})
	w.gh.FailUserRepos(http.StatusForbidden)
	_, err := newLair(w).Ensure(w.ctx, "lucas", guildID, tok.Secret())
	var by *github.ErrCreateByHand
	if !errors.As(err, &by) || !strings.Contains(by.Link, "mestre-guilda-") || !strings.Contains(by.Link, "private") {
		t.Fatalf("Ensure = %v, want the link to create it in the browser", err)
	}
	if _, ok, _ := w.apps.LairRepo(w.ctx); ok {
		t.Error("a repository of the lock was recorded without the access being confirmed")
	}
	// an App without the permission of creating gets the same answer
	w2 := newWorld(t)
	w2.ready(github.App{Name: "lucas-mestre-grao-mestre", Role: github.RoleGrandMaster}, 1, 11, 100, false, "lucas/meu-app")
	tok2, _ := w2.tokens.Installation(w2.ctx, github.Request{App: "lucas-mestre-grao-mestre"})
	if _, err := newLair(w2).Ensure(w2.ctx, "lucas", guildID, tok2.Secret()); !errors.As(err, &by) {
		t.Errorf("an App that may not create = %v", err)
	}
}
