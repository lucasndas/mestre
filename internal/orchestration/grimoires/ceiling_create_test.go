package grimoires_test

import (
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/grimoires"
)

// The ceiling of the workspace holds for every character that is made, also the
// ones a restore makes with a class the destination already has: the class was
// fine when it was approved, the ceiling came down after.
func TestARestoreWithAClassOfTheDestinationAboveItsCeilingCreatesNoCharacter(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	const perms = "version: 1\ncommands: {allow: [\"docker *\"], default: ask}\n"
	w.workspaceClass(w.a, "ops", perms)
	w.workspaceClass(w.b, "ops", perms)
	dev := w.recruit(w.a, "Dev Claude", "Dev", "ops")
	w.save(w.a, "Ops", grimoires.Selection{Characters: picks(dev.ID)})
	w.gate.set(w.b.slug, "ops", true) // the ceiling of B came down after the class was approved

	rep, err := w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.b, "Ops", nil))
	if err == nil && len(rep.Failed) == 0 {
		t.Fatalf("the restore went through: %+v", rep)
	}
	if w.has(w.b, "Dev") {
		t.Error("a character above the ceiling of the workspace was created by the restore")
	}
	// and with the ceiling lifted it is made
	w.gate.set(w.b.slug, "ops", false)
	if _, err := w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.b, "Ops", nil)); err != nil || !w.has(w.b, "Dev") {
		t.Errorf("with the ceiling lifted: %v, has Dev %v", err, w.has(w.b, "Dev"))
	}
}
