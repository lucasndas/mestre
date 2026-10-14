package classes_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

// Save checks the package itself: a Files built by hand, out of order, with
// a path that does not travel, twice the same file or over the limits, is
// refused before anything is written.
func TestSaveDoesNotTrustTheFilesOfTheRequest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	good := pkg(t, "qa", "", defaultPerms, nil)
	class, _ := good.Get("CLASS.md")
	perms, _ := good.Get("permissions.yaml")
	for name, files := range map[string]classes.Files{
		"dotdot":    {{Path: "CLASS.md", Data: class}, {Path: "permissions.yaml", Data: perms}, {Path: "../fora.sh", Data: []byte("x")}},
		"twice":     {{Path: "CLASS.md", Data: class}, {Path: "permissions.yaml", Data: perms}, {Path: "permissions.yaml", Data: perms}},
		"backslash": {{Path: "CLASS.md", Data: class}, {Path: `hooks\x.sh`, Data: []byte("x")}, {Path: "permissions.yaml", Data: perms}},
		"too large": {{Path: "CLASS.md", Data: class}, {Path: "big.txt", Data: make([]byte, classes.MaxFileBytes+1)}, {Path: "permissions.yaml", Data: perms}},
	} {
		_, err := f.lib.Save(ctx, classes.SaveRequest{Name: "qa", Scope: classes.ClassTable, Table: tableID, App: "novo", Files: files, By: tableMaster})
		if !errors.Is(err, classes.ErrPackage) {
			t.Errorf("%s: err = %v; want ErrPackage", name, err)
		}
	}
	// Out of order is only sorted: the package is the same.
	res := f.save(classes.SaveRequest{Name: "qa", Scope: classes.ClassTable, Table: tableID, App: "novo",
		Files: classes.Files{{Path: "permissions.yaml", Data: perms}, {Path: "CLASS.md", Data: class}}, By: tableMaster})
	if !res.Applied {
		t.Fatalf("the sorted package was not applied: %+v", res)
	}
}

// The name of the built-in class of the orchestrators is taken in every
// scope (docs/classes/classe-orquestrador.md).
func TestTheOrchestratorNameIsReserved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	for _, c := range []struct {
		scope classes.ClassScope
		table string
		by    classes.Author
	}{{classes.ClassTable, tableID, tableMaster}, {classes.ClassWorkspace, "", admin}, {classes.ClassGlobal, "", owner}} {
		_, err := f.lib.Save(ctx, classes.SaveRequest{Name: classes.ReservedOrchestrator, Scope: c.scope, Table: c.table, App: "novo",
			Files: pkg(t, classes.ReservedOrchestrator, "", defaultPerms, nil), By: c.by})
		if !errors.Is(err, classes.ErrNameTaken) || !strings.Contains(err.Error(), "orquestrador") {
			t.Errorf("%s: err = %v; want name_taken", c.scope, err)
		}
	}
}
