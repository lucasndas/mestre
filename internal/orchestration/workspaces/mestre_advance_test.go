package workspaces_test

import (
	"strings"
	"testing"
)

// The files of .mestre/ are the engine's own: untracked ones do not make the
// main clone "dirty", so the clone keeps advancing when the project versions
// .mestre/ (owner decision of stage 2). Part of CA-WS-06 (the full suite has
// the real git case, where the copies are removed).
func TestUntrackedMestreFilesDoNotHoldTheMainCloneBack(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.active()
	r.principal(w.Slug).SetFile(".mestre/regras/instrucoes/grao-mestre.md", "mine")
	r.principal(w.Slug).SetFile(".mestre/modelos/VERSAO", "1\n")
	tip := r.push("feat: ahead", map[string]string{"a.txt": "a"})

	if err := r.svc.Fetch(r.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	if got := r.head(w.Slug); got != tip {
		t.Fatalf("head = %s, advance %+v; want the clone advanced to %s", got, r.get(w.Slug).Advance, tip)
	}
}

// A file of .mestre/ that the project versions with other content than the
// engine's copy is not overwritten: the advance waits and says why.
func TestMestreFileThatDiffersFromTheVersionedOneHoldsTheAdvance(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.active()
	before := r.head(w.Slug)
	r.principal(w.Slug).SetFile(".mestre/regras/teto.yaml", "mine")
	r.push("feat: version .mestre", map[string]string{".mestre/regras/teto.yaml": "theirs"})

	if err := r.svc.Fetch(r.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	a := r.get(w.Slug).Advance
	if r.head(w.Slug) != before || !strings.Contains(a.Reason, ".mestre/") || a.BlockedSince != t0 {
		t.Fatalf("head %s (was %s), advance %+v; want it held back for the .mestre/ file", r.head(w.Slug), before, a)
	}
}

// A change outside .mestre/ still holds the advance, with or without files of
// .mestre/ next to it, and counts only those.
func TestOnlyChangesOutsideMestreCountAsLocalChanges(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.active()
	r.principal(w.Slug).SetFile(".mestre/modelos/VERSAO", "1\n")
	r.principal(w.Slug).SetFile("notes.md", "wip")
	r.push("feat: ahead", map[string]string{"a.txt": "a"})

	if err := r.svc.Fetch(r.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	if a := r.get(w.Slug).Advance; !strings.Contains(a.Reason, "1 arquivo(s) com alterações locais") {
		t.Fatalf("advance %+v; want one local change counted", a)
	}
}
