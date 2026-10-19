package github_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/github"
)

// CA-GH-ID-07
func TestTheTokenIsAskedForOnlyTheRepositoryOfTheAction(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.ready(github.App{Name: "lucas-mestre-mestre", Role: github.RoleMestre}, 2, 12, 101, false, "lucas/x", "lucas/meu-app")
	perms := github.RolePermissions(github.RoleMestre, false)
	tok, err := w.tokens.Installation(w.ctx, github.Request{App: "lucas-mestre-mestre", Repo: "lucas/x", Permissions: perms})
	if err != nil {
		t.Fatal(err)
	}
	reqs := w.gh.TokenRequests()
	if len(reqs) != 1 || len(reqs[0].Repositories) != 1 || reqs[0].Repositories[0] != "x" {
		t.Fatalf("the request listed %+v, want only x", reqs)
	}
	if reqs[0].Permissions["contents"] != "write" || reqs[0].Permissions["administration"] != "" || reqs[0].Permissions["workflows"] != "" {
		t.Errorf("permissions asked: %v", reqs[0].Permissions)
	}
	// the token reaches x and not the other repository of the installation
	if fmt.Sprint(tok) != "github.Token(redacted)" || strings.Contains(fmt.Sprintf("%v %+v %#v", tok, tok, tok), tok.Secret()) {
		t.Errorf("the token prints itself: %v", tok)
	}
}

func TestATokenStillGoodIsReusedAndOneNearItsEndIsRenewed(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.ready(github.App{Name: "lucas-mestre-mestre", Role: github.RoleMestre}, 2, 12, 101, false, "lucas/x")
	req := github.Request{App: "lucas-mestre-mestre", Repo: "lucas/x", Permissions: github.RolePermissions(github.RoleMestre, false)}
	a, err := w.tokens.Installation(w.ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	w.clk.Advance(54 * time.Minute) // 6 minutes left
	b, _ := w.tokens.Installation(w.ctx, req)
	if a.Secret() != b.Secret() || len(w.gh.TokenRequests()) != 1 {
		t.Fatalf("the token was asked for again too soon: %d requests", len(w.gh.TokenRequests()))
	}
	w.clk.Advance(2 * time.Minute) // 4 minutes left: renew 5 minutes before
	c, err := w.tokens.Installation(w.ctx, req)
	if err != nil || c.Secret() == a.Secret() || len(w.gh.TokenRequests()) != 2 {
		t.Fatalf("the token near its end was not renewed: %v, %d requests", err, len(w.gh.TokenRequests()))
	}
	// another repository or other permissions are other tokens
	if _, err := w.tokens.Installation(w.ctx, github.Request{App: "lucas-mestre-mestre", Repo: "lucas/x", Permissions: map[string]string{"issues": "write"}}); err != nil {
		t.Fatal(err)
	}
	if len(w.gh.TokenRequests()) != 3 {
		t.Errorf("%d requests, want a new one for other permissions", len(w.gh.TokenRequests()))
	}
}

func TestAnAppNotInstalledInTheRepositoryFailsAndARefusedTokenPutsTheAppInError(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.ready(github.App{Name: "lucas-mestre-mestre", Role: github.RoleMestre}, 2, 12, 101, false, "lucas/x")
	_, err := w.tokens.Installation(w.ctx, github.Request{App: "lucas-mestre-mestre", Repo: "lucas/outro", Permissions: nil})
	if !errors.Is(err, github.ErrNotInstalled) || !strings.Contains(err.Error(), "lucas-mestre-mestre") || !strings.Contains(err.Error(), "lucas/outro") {
		t.Errorf("not installed = %v", err)
	}
	// the key in the vault is not the one of the App: GitHub refuses the JWT
	other := newWorld(t)
	other.ready(github.App{Name: "lucas-mestre-qa", Role: github.RoleClass, Class: "QA"}, 3, 13, 123, false, "lucas/x")
	other.vault.keys[github.KeySecret("lucas-mestre-qa")] = pemOf(otherKey(t))
	_, err = other.tokens.Installation(other.ctx, github.Request{App: "lucas-mestre-qa", Repo: "lucas/x"})
	if !errors.Is(err, github.ErrTokenRefused) {
		t.Fatalf("a wrong key = %v", err)
	}
	if a, _ := other.apps.Get(other.ctx, "lucas-mestre-qa"); a.State != classes.AppWithError {
		t.Errorf("the App is %s, want com_erro", a.State)
	}
	if strings.Contains(err.Error(), "PRIVATE KEY") {
		t.Error("the error carries the key")
	}
}
