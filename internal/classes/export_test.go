package classes_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

func TestSlugIsTheNameTheLibraryKeeps(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"QA API": "qa-api", "  Dev ": "dev", "qa": "qa", "Revisão de PR": "revis-o-de-pr", "a--b": "a-b", "": "", "---": ""} {
		if got := classes.Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

// The export overwrites the earlier one (what the package no longer has goes),
// and the folder is one the agents write: a link planted in it is never
// followed.
func TestExportPackageOverwritesAndNeverFollowsALink(t *testing.T) {
	t.Parallel()
	tmp, outside := t.TempDir(), t.TempDir()
	first := pkg(t, "qa", "novo", defaultPerms, map[string]string{"hooks/old.sh": "echo old\n", "notes/a.md": "a"})
	dir, err := classes.ExportPackage(tmp, "qa", first)
	if err != nil || dir != filepath.Join(tmp, "classes", "qa") {
		t.Fatalf("export = %q, %v", dir, err)
	}
	second := pkg(t, "qa", "novo", defaultPerms, nil)
	if _, err := classes.ExportPackage(tmp, "qa", second); err != nil {
		t.Fatal(err)
	}
	got, err := classes.ReadDir(dir)
	if err != nil || got.Hash() != second.Hash() {
		t.Fatalf("exported = %v, %v; want the second package only", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes")); err == nil {
		t.Error("the empty folder of a removed file is still there")
	}

	// a link planted where a file goes
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "CLASS.md")); err != nil {
		t.Skipf("no links here: %v", err)
	}
	if _, err := classes.ExportPackage(tmp, "qa", second); err != nil {
		t.Logf("export over a link: %v", err)
	}
	if b, _ := os.ReadFile(secret); string(b) != "keep" {
		t.Errorf("the file the link points at was written: %q", b)
	}
	// a link in place of the folder
	if err := os.RemoveAll(filepath.Join(tmp, "classes")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(tmp, "classes")); err != nil {
		t.Fatal(err)
	}
	if _, err := classes.ExportPackage(tmp, "qa", second); err == nil {
		t.Error("the export went through a link")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 1 {
		t.Errorf("something was written outside: %v", entries)
	}
	if _, err := classes.ExportPackage("relative", "qa", second); err == nil {
		t.Error("the export of a relative path")
	}
}

// Delete removes the approved version, the proposals and the folder, and only
// for who can change the class in its scope.
func TestDeleteRemovesTheClassAndOnlyForWhoCanChangeIt(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	k := classes.ClassKey{Scope: classes.ClassTable, Table: tableID, Name: "qa"}
	f.save(classes.SaveRequest{Name: "qa", Scope: classes.ClassTable, Table: tableID, App: "novo", By: tableMaster, Files: pkg(t, "qa", "", defaultPerms, nil)})
	other := classes.Author{Level: classes.LevelTableMaster, ID: "m2", Table: "43-outra"}
	if err := f.lib.Delete(context.Background(), k, other); !errors.Is(err, classes.ErrNotAuthorized) {
		t.Fatalf("delete by the master of another table = %v", err)
	}
	if err := f.lib.Delete(context.Background(), k, classes.Author{Level: classes.LevelGrandMaster, ID: "g"}); !errors.Is(err, classes.ErrNotAuthorized) {
		t.Fatalf("delete by the Grand Master = %v", err)
	}
	if err := f.lib.Delete(context.Background(), k, tableMaster); err != nil {
		t.Fatal(err)
	}
	if _, err := f.lib.Record(context.Background(), "qa", tableID); !errors.Is(err, classes.ErrNotVisible) {
		t.Errorf("record after the delete = %v", err)
	}
	dir, _ := f.roots.Dir(k)
	if _, err := os.Stat(dir); err == nil {
		t.Error("the folder is still there")
	}
	if err := f.lib.Delete(context.Background(), k, tableMaster); !errors.Is(err, classes.ErrNotVisible) {
		t.Errorf("delete of a class that is gone = %v", err)
	}
}
