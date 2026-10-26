package github_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/github"
)

// CA-GH-ID-01
func TestTheNameOfTheAppOfAClassHasAtMost34CharactersAndNormalizes(t *testing.T) {
	t.Parallel()
	prefix := github.DefaultPrefix("Lucas")
	if prefix != "lucas-mestre" {
		t.Fatalf("prefix = %q", prefix)
	}
	name := github.ClassName(prefix, "QA de integração com APIs externas", nil)
	if len(name) > github.MaxNameLength || !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(name) || !strings.HasPrefix(name, "lucas-mestre-") {
		t.Errorf("name %q (%d)", name, len(name))
	}
	if strings.HasSuffix(name, "-") {
		t.Errorf("the cut left a dash at the end: %q", name)
	}
	if got := github.ClassName(prefix, "QA", nil); got != "lucas-mestre-qa" {
		t.Errorf("QA = %q", got)
	}
	taken := map[string]bool{"lucas-mestre-qa": true, "lucas-mestre-qa-2": true}
	if got := github.ClassName(prefix, "QA", func(n string) bool { return taken[n] }); got != "lucas-mestre-qa-3" {
		t.Errorf("a name in use = %q, want -3", got)
	}
	long := github.ClassName(prefix, strings.Repeat("a", 60), func(n string) bool { return n == strings.Repeat("a", 0) })
	if len(long) > github.MaxNameLength {
		t.Errorf("a long class made %q", long)
	}
	if github.GrandMasterName(prefix) != "lucas-mestre-grao-mestre" || github.MestreName(prefix) != "lucas-mestre-mestre" {
		t.Error("the names of the fixed Apps")
	}
}

type roster map[string][]string

func (r roster) AliveOfClass(_ context.Context, class string) ([]string, error) { return r[class], nil }

type writer struct{ got map[string]github.Identity }

func (w *writer) UpdateIdentity(_ context.Context, id string, i github.Identity) error {
	w.got[id] = i
	return nil
}

// CA-GH-ID-02, the side of the identity: the bot of the App is the author, with the
// e-mail `<bot_user_id>+<slug>[bot]@users.noreply.github.com`. The file and the
// environment of the character are in internal/sessionzero.
func TestTheIdentityOfACharacterIsTheBotOfTheAppOfItsClass(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.ready(github.App{Name: "lucas-mestre-grao-mestre", Role: github.RoleGrandMaster}, 1, 11, 100, true, "lucas/meu-app")
	w.ready(github.App{Name: "lucas-mestre-mestre", Role: github.RoleMestre}, 2, 12, 101, false, "lucas/meu-app")
	w.ready(github.App{Name: "lucas-mestre-qa", Role: github.RoleClass, Class: "QA"}, 3, 13, 123, false, "lucas/meu-app")
	ids := github.Identities{Apps: w.apps}
	got, err := ids.For(w.ctx, github.Who{Role: "character", Class: "QA"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "lucas-mestre-qa[bot]" || got.Email != "123+lucas-mestre-qa[bot]@users.noreply.github.com" || got.Temporary || got.App != "lucas-mestre-qa" {
		t.Errorf("identity = %+v", got)
	}
	if gm, _ := ids.For(w.ctx, github.Who{Role: "grand_master"}); gm.App != "lucas-mestre-grao-mestre" || gm.Name != "lucas-mestre-grao-mestre[bot]" {
		t.Errorf("Grand Master = %+v", gm)
	}
	if tm, _ := ids.For(w.ctx, github.Who{Role: "table_master"}); tm.App != "lucas-mestre-mestre" {
		t.Errorf("table master = %+v", tm)
	}
	if plain, _ := ids.For(w.ctx, github.Who{Role: "character"}); plain.App != "lucas-mestre-mestre" || plain.Temporary {
		t.Errorf("a character without class = %+v", plain)
	}
}

// CA-GH-ID-03
func TestWhileTheAppOfTheClassIsBeingMadeTheActionsGoOutWithTheAppMestreAndMarkTheClass(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.ready(github.App{Name: "lucas-mestre-mestre", Role: github.RoleMestre}, 2, 12, 101, false, "lucas/meu-app")
	if _, err := w.apps.Request(w.ctx, github.App{Name: "lucas-mestre-qa", Role: github.RoleClass, Class: "QA"}); err != nil {
		t.Fatal(err)
	}
	id, err := github.Identities{Apps: w.apps}.For(w.ctx, github.Who{Role: "character", Class: "QA"})
	if err != nil {
		t.Fatal(err)
	}
	if !id.Temporary || id.App != "lucas-mestre-mestre" || id.Name != "lucas-mestre-mestre[bot]" {
		t.Fatalf("identity = %+v", id)
	}
	if got := id.Mark("Faltou teste do erro."); got != "**[QA]** Faltou teste do erro." {
		t.Errorf("the comment = %q", got)
	}
	if tr := id.Trailers(); len(tr) != 1 || tr[0] != "Mestre-Classe: QA" {
		t.Errorf("trailers = %v", tr)
	}
	// the token of the comment is the one of the App "Mestre"
	tok, err := w.tokens.Installation(w.ctx, github.Request{App: id.App, Repo: "lucas/meu-app", Permissions: github.RolePermissions(github.RoleMestre, false)})
	if err != nil || tok.Secret() == "" {
		t.Fatalf("token = %v, %v", tok, err)
	}
	if reqs := w.gh.TokenRequests(); len(reqs) != 1 || reqs[0].App != "lucas-mestre-mestre" {
		t.Errorf("token requests = %+v", reqs)
	}
	// an App that is ready needs no mark
	if (github.Identity{App: "x"}).Mark("texto") != "texto" {
		t.Error("a ready App marks the text")
	}
	// the App "Mestre" not being ready is an error, not an identity borrowed from nobody
	w2 := newWorld(t)
	if _, err := (github.Identities{Apps: w2.apps}).For(w2.ctx, github.Who{Role: "table_master"}); err == nil {
		t.Error("an identity was given with no App")
	}
}

// CA-GH-ID-04
func TestWhenTheAppOfTheClassIsReadyTheLiveCharactersOfTheClassChangeIdentity(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if _, err := w.apps.Request(w.ctx, github.App{Name: "lucas-mestre-qa", Role: github.RoleClass, Class: "QA"}); err != nil {
		t.Fatal(err)
	}
	wr := &writer{got: map[string]github.Identity{}}
	ids := github.Identities{Apps: w.apps, Roster: roster{"QA": {"qa-1", "qa-2"}, "Dev": {"dev-1"}}, Writer: wr}
	if err := ids.AppReady(w.ctx, "lucas-mestre-qa"); err != nil || len(wr.got) != 0 {
		t.Fatalf("before it is ready: %v, rewrote %v", err, wr.got)
	}
	if err := w.apps.Created(w.ctx, "lucas-mestre-qa", "lucas-mestre-qa", 3, "Iv1.x"); err != nil {
		t.Fatal(err)
	}
	if err := w.apps.BotID(w.ctx, "lucas-mestre-qa", 123); err != nil {
		t.Fatal(err)
	}
	if err := w.apps.Installed(w.ctx, "lucas-mestre-qa", 13, []string{"lucas/meu-app"}); err != nil {
		t.Fatal(err)
	}
	if err := ids.AppReady(w.ctx, "lucas-mestre-qa"); err != nil {
		t.Fatal(err)
	}
	if len(wr.got) != 2 || wr.got["dev-1"].Name != "" || wr.got["qa-1"].Name != "lucas-mestre-qa[bot]" || wr.got["qa-2"].Email != "123+lucas-mestre-qa[bot]@users.noreply.github.com" {
		t.Errorf("identities rewritten: %+v", wr.got)
	}
}

// CA-GH-ID-06
func TestAtOneHundredAppsTheCreationOfANewOneIsRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if err := w.apps.SetOthers(w.ctx, 95); err != nil {
		t.Fatal(err)
	}
	if near, _ := w.apps.NearLimit(w.ctx); !near {
		t.Error("95 Apps do not warn the Owner")
	}
	for i := range 4 { // 95 + 4 = 99
		if _, err := w.apps.Request(w.ctx, github.App{Name: github.ClassName("lucas-mestre", string(rune('a'+i)), nil), Role: github.RoleClass, Class: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.apps.Request(w.ctx, github.App{Name: "lucas-mestre-e", Role: github.RoleClass, Class: "e"}); err != nil {
		t.Fatalf("the 100th App: %v", err)
	}
	if _, err := w.apps.Request(w.ctx, github.App{Name: "lucas-mestre-f", Role: github.RoleClass, Class: "f"}); err == nil || err.Error() == "" {
		t.Fatalf("the 101st App was accepted")
	} else if !strings.Contains(err.Error(), "100") {
		t.Errorf("error = %v", err)
	}
	if _, err := w.apps.Request(w.ctx, github.App{Name: "lucas-mestre-a", Role: github.RoleClass, Class: "z"}); err == nil {
		t.Error("a name in use was accepted")
	}
}

func TestTheRegistryIsTheDirectoryOfTheAppsOfTheClasses(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	var _ classes.AppDirectory = w.apps
	if _, ok, _ := w.apps.Requested(w.ctx, "QA"); ok {
		t.Error("an App nobody asked for")
	}
	w.ready(github.App{Name: "lucas-mestre-qa", Role: github.RoleClass, Class: "QA"}, 3, 13, 123, false, "lucas/meu-app")
	if s, ok, _ := w.apps.Requested(w.ctx, "QA"); !ok || s != classes.AppReady {
		t.Errorf("Requested = %v, %v", s, ok)
	}
	if s, ok, _ := w.apps.App(w.ctx, "lucas-mestre-qa"); !ok || s != classes.AppReady {
		t.Errorf("App = %v, %v", s, ok)
	}
	if _, ok, _ := w.apps.App(w.ctx, "nobody"); ok {
		t.Error("an App that does not exist")
	}
}

func TestAnAppThatWasNotCreatedCannotBeInstalledAndAReinstallReplacesTheRepositories(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if _, err := w.apps.Request(w.ctx, github.App{Name: "lucas-mestre-qa", Role: github.RoleClass, Class: "QA"}); err != nil {
		t.Fatal(err)
	}
	if err := w.apps.Installed(w.ctx, "lucas-mestre-qa", 13, []string{"lucas/a"}); err == nil {
		t.Fatal("an App not created was installed")
	}
	if err := w.apps.Created(w.ctx, "lucas-mestre-qa", "lucas-mestre-qa", 3, "Iv1.x"); err != nil {
		t.Fatal(err)
	}
	if err := w.apps.Installed(w.ctx, "lucas-mestre-qa", 13, []string{"lucas/a", "lucas/b"}); err != nil {
		t.Fatal(err)
	}
	if err := w.apps.Installed(w.ctx, "lucas-mestre-qa", 13, []string{"lucas/b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.apps.Installation(w.ctx, "lucas-mestre-qa", "lucas/a"); !errors.Is(err, github.ErrNotInstalled) {
		t.Errorf("the repository the new installation does not cover = %v", err)
	}
}
