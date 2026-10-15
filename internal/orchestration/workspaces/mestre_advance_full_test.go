//go:build full

package workspaces_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/testkit"
)

// The project versions .mestre/ while the main clone holds the engine's own
// untracked copies of the same files: git refuses to overwrite an untracked
// file even with the same content, so the engine removes the identical copies
// before the fast-forward (owner decision of stage 2). Part of CA-WS-06.
func TestRealMainCloneAdvancesOverIdenticalUntrackedMestreCopies(t *testing.T) {
	t.Parallel()
	rr := newRealRig(t)
	w := rr.create()
	if err := rr.svc.Prepare(rr.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	dir := rr.svc.Layout().Principal(w.Slug)
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".mestre/modelos/VERSAO", "1\n")
	write(".mestre/regras/teto.yaml", "mine\n")
	write(".mestre/run/state", "execution file, never versioned\n")
	tip := rr.commit("feat: version .mestre", map[string]string{
		".mestre/modelos/VERSAO": "1\n", ".mestre/regras/teto.yaml": "theirs\n",
	})

	// One copy differs: nothing is overwritten and the advance waits.
	before := rr.head(w)
	if err := rr.svc.Fetch(rr.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	got, _ := rr.svc.Get(rr.ctx, w.Slug)
	if rr.head(w) != before || !strings.Contains(got.Advance.Reason, ".mestre/") {
		t.Fatalf("with a different copy: head %s (was %s), advance %+v", rr.head(w), before, got.Advance)
	}
	if body, _ := os.ReadFile(filepath.Join(dir, ".mestre", "regras", "teto.yaml")); string(body) != "mine\n" {
		t.Fatalf("the different copy was overwritten: %q", body)
	}

	// The copy becomes identical to the versioned one: it is removed and the
	// clone advances.
	write(".mestre/regras/teto.yaml", "theirs\n")
	rr.clk.Advance(time.Minute)
	if err := rr.svc.Fetch(rr.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	if rr.head(w) != string(tip) {
		t.Fatalf("head %s; want the tip %s", rr.head(w), tip)
	}
	if body, err := os.ReadFile(filepath.Join(dir, ".mestre", "modelos", "VERSAO")); err != nil || string(body) != "1\n" {
		t.Fatalf("VERSAO after the advance = %q, %v", body, err)
	}
	if body, err := os.ReadFile(filepath.Join(dir, ".mestre", "run", "state")); err != nil || !strings.Contains(string(body), "execution") {
		t.Fatalf("the execution file was touched: %q, %v", body, err)
	}
	if out := testkit.ProjectGit(t, dir, "status", "--porcelain", "--", ".mestre/modelos", ".mestre/regras"); strings.TrimSpace(out) != "" {
		t.Fatalf("the versioned files are not clean after the advance: %q", out)
	}
}
