//go:build full

package github_test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/mestre-ia/mestre/internal/github"
)

// Part of CA-GH-ID-08 (MVP-5; the heavy composition with the lab closes it in M7, the doubles say the same here)
// A table with a Dev and a QA: what each one does on GitHub goes out with the App
// of its class, and the author of its commits is the bot of that App.
func TestWhatTheDevAndTheQAdoGoesOutWithTheAppOfEachClass(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.ready(github.App{Name: "lucas-mestre-mestre", Role: github.RoleMestre}, 2, 12, 101, false, "lucas/meu-app")
	w.ready(github.App{Name: "lucas-mestre-dev", Role: github.RoleClass, Class: "Dev"}, 3, 13, 201, false, "lucas/meu-app")
	w.ready(github.App{Name: "lucas-mestre-qa", Role: github.RoleClass, Class: "QA"}, 4, 14, 202, false, "lucas/meu-app")
	w.gh.AddRepo("lucas", "meu-app")
	ids := github.Identities{Apps: w.apps}

	comment := func(class, title string) github.Identity {
		t.Helper()
		id, err := ids.For(w.ctx, github.Who{Role: "character", Class: class})
		if err != nil {
			t.Fatal(err)
		}
		tok, err := w.tokens.Installation(w.ctx, github.Request{App: id.App, Repo: "lucas/meu-app", Permissions: github.RolePermissions(github.RoleClass, false)})
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequestWithContext(w.ctx, http.MethodPost, w.srv.URL+"/repos/lucas/meu-app/issues", bytes.NewBufferString(`{"title":"`+id.Mark(title)+`"}`))
		req.Header.Set("Authorization", "Bearer "+tok.Secret())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%s: status %d", class, resp.StatusCode)
		}
		return id
	}
	dev := comment("Dev", "login feito")
	qa := comment("QA", "falta teste")
	if dev.Name != "lucas-mestre-dev[bot]" || qa.Name != "lucas-mestre-qa[bot]" || dev.Email == qa.Email {
		t.Errorf("authors of the commits: %+v and %+v", dev, qa)
	}
	acts := w.gh.Actions()
	if len(acts) != 2 || acts[0].App != "lucas-mestre-dev" || acts[1].App != "lucas-mestre-qa" {
		t.Errorf("actions on GitHub: %+v; want one of each App", acts)
	}
}
