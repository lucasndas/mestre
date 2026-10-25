package ghcli_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/ghcli"
	"github.com/lucasndas/mestre/internal/github"
)

type creatorLog struct {
	why   string
	began []string
}

func (c *creatorLog) Available(context.Context) string { return c.why }

func (c *creatorLog) Begin(_ context.Context, app, by string) (ghcli.AppLink, error) {
	c.began = append(c.began, app+" by "+by)
	return ghcli.AppLink{ID: "req-1", ExpiresAt: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}, nil
}

func appsRig(t *testing.T) (*rig, *github.Registry, *creatorLog) {
	t.Helper()
	r := newRig(t)
	reg := &github.Registry{DB: r.db, Clock: r.clk}
	cr := &creatorLog{}
	(&ghcli.Apps{Characters: r.chars, Store: reg, Creator: cr}).Register(r.fwd)
	if _, err := reg.Request(r.ctx, github.App{Name: "lucas-mestre-grao-mestre", Role: github.RoleGrandMaster}); err != nil {
		t.Fatal(err)
	}
	return r, reg, cr
}

// `app list|status|create`.
func TestAppCreatePreparesTheAppOfAClassAndSendsTheLinkToTheOwnerOnly(t *testing.T) {
	t.Parallel()
	r, reg, cr := appsRig(t)
	got := r.run("gm", "app create", map[string]any{"class": "QA"})
	if got.OK || got.Exit != 9 || got.Data["app"] != "lucas-mestre-qa" || got.Data["request_id"] != "req-1" {
		t.Fatalf("create = %+v, want exit 9 with the App and the request", got)
	}
	if _, ok := got.Data["link"]; ok {
		t.Fatal("the link came back to the Grand Master")
	}
	if len(cr.began) != 1 || !strings.HasPrefix(cr.began[0], "lucas-mestre-qa by ") {
		t.Fatalf("began = %v", cr.began)
	}
	if app, err := reg.Get(r.ctx, "lucas-mestre-qa"); err != nil || app.Class != "QA" || app.State != "aguardando_criacao" {
		t.Fatalf("the App = %+v, %v", app, err)
	}
	// the same class again: it already has one
	if again := r.run("gm", "app create", map[string]any{"class": "QA"}); again.OK || again.Code != "app_exists" || again.Exit != 7 {
		t.Fatalf("the same class: %+v, want exit 7 app_exists", again)
	}
	// a name that is too long, a class with no name
	if long := r.run("gm", "app create", map[string]any{"class": "Dev", "name": strings.Repeat("x", 40)}); long.OK || long.Code != "name_too_long" || long.Exit != 2 {
		t.Fatalf("a long name: %+v, want exit 2 name_too_long", long)
	}
	if none := r.run("gm", "app create", nil); none.OK || none.Code != "usage" {
		t.Fatalf("no class: %+v", none)
	}
	// the account with 100 Apps
	if err := reg.SetOthers(r.ctx, 99); err != nil {
		t.Fatal(err)
	}
	if full := r.run("gm", "app create", map[string]any{"class": "Revisor"}); full.OK || full.Code != "app_limit" || full.Exit != 8 {
		t.Fatalf("100 Apps: %+v, want exit 8 app_limit", full)
	}
	if len(cr.began) != 1 {
		t.Fatalf("a link was made for a refused creation: %v", cr.began)
	}
	// only the Grand Master
	if tm := r.run("tm", "app create", map[string]any{"class": "Dev"}); tm.OK || tm.Code != "level_denied" {
		t.Fatalf("a table master: %+v", tm)
	}
}

// CA-CLI-GM-06
// The command as written, `app create --class QA`, on an account that holds
// exactly 100 Apps (the 99 of others and the Grand Master's own).
func TestAppCreateOfQAOnAFullAccountIsExit8AppLimit(t *testing.T) {
	t.Parallel()
	r, reg, cr := appsRig(t)
	if err := reg.SetOthers(r.ctx, 99); err != nil {
		t.Fatal(err)
	}
	if full := r.run("gm", "app create", map[string]any{"class": "QA"}); full.OK || full.Code != "app_limit" || full.Exit != 8 {
		t.Fatalf("100 Apps: %+v, want exit 8 app_limit", full)
	}
	if len(cr.began) != 0 {
		t.Errorf("a link was started for a refused creation: %v", cr.began)
	}
	if _, err := reg.Get(r.ctx, "lucas-mestre-qa"); err == nil {
		t.Error("an App was registered by a refused creation")
	}
}

func TestAppCreateSaysWhyItCannotAndPreparesNothing(t *testing.T) {
	t.Parallel()
	r, reg, cr := appsRig(t)
	cr.why = "needs the panel"
	if got := r.run("gm", "app create", map[string]any{"class": "QA"}); got.OK || !strings.Contains(got.Error, "panel") {
		t.Fatalf("create = %+v, want the reason", got)
	}
	if _, err := reg.Get(r.ctx, "lucas-mestre-qa"); err == nil {
		t.Fatal("an App was registered by a creation that could not go on")
	}
}

func TestAppListAndStatusShowTheAppsTheirReposAndTheCount(t *testing.T) {
	t.Parallel()
	r, _, _ := appsRig(t)
	list := r.run("gm", "app list", nil)
	apps, _ := list.Data["apps"].([]any)
	if !list.OK || len(apps) != 1 || list.Data["count"] != float64(1) || list.Data["limit"] != float64(100) {
		t.Fatalf("list = %+v", list)
	}
	st := r.run("gm", "app status", nil, "lucas-mestre-grao-mestre")
	if !st.OK || st.Data["state_label"] != "em criação" || st.Data["key_in_vault"] != false {
		t.Fatalf("status = %+v", st)
	}
	if none := r.run("gm", "app status", nil, "nada"); none.OK || none.Code != "not_found" || none.Exit != 4 {
		t.Fatalf("an App that does not exist: %+v", none)
	}
	if tm := r.run("tm", "app list", nil); tm.OK || tm.Code != "level_denied" {
		t.Fatalf("a table master: %+v", tm)
	}
}
