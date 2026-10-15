package chain_test

import (
	"context"
	"slices"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
)

// fakeDecrees gives the real builder the exceptions the fake grants made.
type fakeDecrees struct{ f *fakeClasses }

func (d fakeDecrees) Decrees(_ context.Context, c characters.Character) ([]classes.Decree, error) {
	d.f.mu.Lock()
	defer d.f.mu.Unlock()
	return slices.Clone(d.f.decrees[c.ID]), nil
}

// approveClass puts an approved class in the workspace of the world, with the
// package the library keeps (worldOpts.realClasses).
func (w *world) approveClass(name, perms string) {
	w.t.Helper()
	files, err := classes.NewFiles([]classes.File{
		{Path: "CLASS.md", Data: []byte("---\nname: " + name + "\ndescription: Classe de teste\napp: novo\n---\nFaça o que a mesa pede.\n")},
		{Path: "permissions.yaml", Data: []byte(perms)},
	})
	if err != nil {
		w.t.Fatal(err)
	}
	rec := classes.ClassRecord{Key: classes.ClassKey{Scope: classes.ClassWorkspace, Name: name}, Hash: files.Hash(), Files: files, ApprovedAt: w.clk.Now(),
		By: classes.Author{Level: classes.LevelAdmin, ID: "u1"}}
	if err := w.lib.Records.Approve(w.ctx, w.slug, rec, 0); err != nil {
		w.t.Fatal(err)
	}
}
