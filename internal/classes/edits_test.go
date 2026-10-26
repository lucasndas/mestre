package classes_test

import (
	"fmt"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
)

// CA-HOOK-09: what a forged informative event can do to the tracker is deny.
func TestAForgedEventOfACharacterNeverFreesAFileAnotherHolds(t *testing.T) {
	t.Parallel()
	tr := classes.NewEditTrackerFor("linux")
	tr.Record("42-login", "a", "Dev A", []string{"/w/x.go"})
	// b records the same file, and ends its turn: a keeps its mark
	tr.Record("42-login", "b", "Dev B", []string{"/w/x.go"})
	if who, ok := tr.OtherEditor("42-login", "b", "/w/x.go"); !ok || who != "Dev A" {
		t.Fatalf("b is stopped by %q, %v; want Dev A", who, ok)
	}
	tr.EndTurn("42-login", "b")
	if who, ok := tr.OtherEditor("42-login", "c", "/w/x.go"); !ok || who != "Dev A" {
		t.Fatalf("after the forged turn_end of b: %q, %v; want the mark of Dev A", who, ok)
	}
	if _, ok := tr.OtherEditor("42-login", "a", "/w/x.go"); ok {
		t.Fatal("a is stopped by its own mark")
	}
	tr.EndTurn("42-login", "a")
	if _, ok := tr.OtherEditor("42-login", "c", "/w/x.go"); ok {
		t.Fatal("the file stays held after the turn of a")
	}
	// the files are different on a system that tells them apart
	tr.Record("42-login", "a", "Dev A", []string{"/w/X.go"})
	if _, ok := tr.OtherEditor("42-login", "c", "/w/x.go"); ok {
		t.Fatal("x.go and X.go are one file on linux")
	}
}

func TestTheTrackerReadsTheSpellingsWindowsTakesForOneFile(t *testing.T) {
	t.Parallel()
	tr := classes.NewEditTrackerFor("windows")
	for _, written := range []string{`C:\wt\x.go::$DATA`, `\\?\C:\wt\x.go`, `\\localhost\C$\wt\x.go`, `C:\WT\X.GO.`} {
		tr.Record("42-login", "a", "Dev A", []string{written})
		for _, asked := range []string{`C:\wt\x.go`, `c:/wt/x.go`} {
			if who, ok := tr.OtherEditor("42-login", "b", asked); !ok || who != "Dev A" {
				t.Errorf("recorded %q, asked %q: %q, %v; want the mark of Dev A", written, asked, who, ok)
			}
		}
		tr.EndTurn("42-login", "a")
	}
}

func TestTheTrackerHoldsAtMostAFewFilesPerCharacterAndForgetsAGoneOne(t *testing.T) {
	t.Parallel()
	tr := classes.NewEditTrackerFor("linux")
	var paths []string
	for i := range classes.MaxEditPaths + 50 {
		paths = append(paths, fmt.Sprintf("/w/f%d.go", i))
	}
	tr.Record("42-login", "a", "Dev A", paths)
	if _, ok := tr.OtherEditor("42-login", "b", fmt.Sprintf("/w/f%d.go", classes.MaxEditPaths-1)); !ok {
		t.Fatal("a file within the bound was not held")
	}
	if _, ok := tr.OtherEditor("42-login", "b", fmt.Sprintf("/w/f%d.go", classes.MaxEditPaths+10)); ok {
		t.Fatal("a file past the bound was held")
	}
	// another table, same agent id: the bound is the character's, not the table's
	tr.Record("43-other", "a", "Dev A", []string{"/w/y.go"})
	if _, ok := tr.OtherEditor("43-other", "b", "/w/y.go"); ok {
		t.Fatal("a character past the bound holds one more file in another table")
	}
	// a character that is gone sends no turn_end
	tr.Forget("a")
	if _, ok := tr.OtherEditor("42-login", "b", "/w/f0.go"); ok {
		t.Fatal("a file of a forgotten character is still held")
	}
	tr.Record("43-other", "a", "Dev A", []string{"/w/y.go"})
	if _, ok := tr.OtherEditor("43-other", "b", "/w/y.go"); !ok {
		t.Fatal("a forgotten character cannot hold files again")
	}
}
