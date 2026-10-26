package sessionzero_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/orchestration/presets"
	"github.com/mestre-ia/mestre/internal/sessionzero"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
)

// qaRoster and charWriter bind the ports of github.Identities to the real
// sessionzero.Character, as the engine does.
type qaRoster map[string][]string

func (r qaRoster) AliveOfClass(_ context.Context, class string) ([]string, error) {
	return r[class], nil
}

type charWriter struct{ ch *sessionzero.Character }

func (w charWriter) UpdateIdentity(ctx context.Context, id string, i github.Identity) error {
	_, err := w.ch.UpdateIdentity(ctx, id, sessionzero.Identity{Name: i.Name, Email: i.Email})
	return err
}

// CA-GH-ID-02 and CA-GH-ID-04, the whole chain: the App QA (bot_user_id 123)
// becomes the Identity, the QA character that starts has it in the gitconfig
// and, when the App turns ready, the live QA character is rewritten and the
// Dev one is not.
func TestTheAppOfTheQAClassReachesTheGitconfigOfTheLiveCharacters(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	clk := fakeClock()
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	apps := &github.Registry{DB: db, Clock: clk}
	if _, err := apps.Request(ctx, github.App{Name: "lucas-mestre-qa", Slug: "lucas-mestre-qa", Role: github.RoleClass, Class: "QA"}); err != nil {
		t.Fatal(err)
	}
	r := newCharRig(t, nil)
	conf := func(id string) string { return read(t, filepath.Join(r.dir, ".mestre", "agents", id, "gitconfig")) }
	ids := github.Identities{Apps: apps, Roster: qaRoster{"QA": {"qa-1"}, "Dev": {"dev-1"}}, Writer: charWriter{r.ch}}

	// characters start while the App of the QA class is not ready
	r.prepare(r.req("dev-1", presets.AgentClaudeCode, sessionzero.RoleCharacter))
	q := r.req("qa-1", presets.AgentClaudeCode, sessionzero.RoleCharacter)
	q.Identity = sessionzero.Identity{Name: "lucas-mestre-mestre[bot]", Email: "1+lucas-mestre-mestre[bot]@users.noreply.github.com"}
	r.prepare(q)
	devBefore := conf("dev-1")

	// the App becomes ready
	if err := apps.Created(ctx, "lucas-mestre-qa", "lucas-mestre-qa", 3, "Iv1.x"); err != nil {
		t.Fatal(err)
	}
	if err := apps.BotID(ctx, "lucas-mestre-qa", 123); err != nil {
		t.Fatal(err)
	}
	if err := apps.Installed(ctx, "lucas-mestre-qa", 13, []string{"lucas/meu-app"}); err != nil {
		t.Fatal(err)
	}
	if err := ids.AppReady(ctx, "lucas-mestre-qa"); err != nil {
		t.Fatal(err)
	}
	wantName, wantEmail := `name = "lucas-mestre-qa[bot]"`, `email = "123+lucas-mestre-qa[bot]@users.noreply.github.com"`
	live := conf("qa-1")
	if !strings.Contains(live, wantName) || !strings.Contains(live, wantEmail) || strings.Contains(live, "mestre-mestre[bot]") {
		t.Errorf("the live QA gitconfig after the App is ready:\n%s", live)
	}
	if conf("dev-1") != devBefore {
		t.Errorf("the gitconfig of the Dev character changed:\n%s", conf("dev-1"))
	}

	// a QA character that starts now gets the identity of the App; the environment has the file and no author
	id, err := ids.For(ctx, github.Who{Role: "character", Class: "QA"})
	if err != nil {
		t.Fatal(err)
	}
	q2 := r.req("qa-2", presets.AgentClaudeCode, sessionzero.RoleCharacter)
	q2.Identity = sessionzero.Identity{Name: id.Name, Email: id.Email}
	plan := r.prepare(q2)
	started := conf("qa-2")
	if !strings.Contains(started, wantName) || !strings.Contains(started, wantEmail) {
		t.Errorf("the gitconfig of the QA character that starts:\n%s", started)
	}
	if got, ok := envOf(plan.Launch.Env, "GIT_CONFIG_GLOBAL"); !ok || got != filepath.Join(r.dir, ".mestre", "agents", "qa-2", "gitconfig") {
		t.Errorf("GIT_CONFIG_GLOBAL = %q, %v", got, ok)
	}
	for _, kv := range plan.Launch.Env {
		if k := strings.ToUpper(strings.SplitN(kv, "=", 2)[0]); strings.HasPrefix(k, "GIT_AUTHOR_") || strings.HasPrefix(k, "GIT_COMMITTER_") {
			t.Errorf("the environment has %s", kv)
		}
	}
}
