package gitx_test

import (
	"testing"

	"github.com/mestre-ia/mestre/internal/gitx"
)

func TestParseTrackedStatus(t *testing.T) {
	t.Parallel()
	h := "1234567890123456789012345678901234567890"
	out := "# branch.oid " + h + "\x00# branch.head epico/42-login\x00" +
		"1 A. N... 000000 100644 100644 " + h + " " + h + " tests/a b.ts\x00" +
		"1 .M N... 100644 100644 100644 " + h + " " + h + " src/m.ts\x00" +
		"u UU N... 100644 100644 100644 100644 " + h + " " + h + " " + h + " src/conflict.ts\x00" +
		"? new.txt\x00"
	ts, err := gitx.ParseTrackedStatus([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if ts.Branch != "epico/42-login" || ts.Detached {
		t.Fatalf("branch = %q detached %v", ts.Branch, ts.Detached)
	}
	want := []gitx.StatusEntry{
		{Index: 'A', Worktree: ' ', Path: "tests/a b.ts"},
		{Index: ' ', Worktree: 'M', Path: "src/m.ts"},
		{Index: 'U', Worktree: 'U', Path: "src/conflict.ts"},
	}
	if len(ts.Entries) != len(want) {
		t.Fatalf("entries = %+v, want %+v", ts.Entries, want)
	}
	for i := range want {
		if ts.Entries[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, ts.Entries[i], want[i])
		}
	}
}

func TestParseTrackedStatusDetachedAndMalformed(t *testing.T) {
	t.Parallel()
	ts, err := gitx.ParseTrackedStatus([]byte("# branch.oid abc\x00# branch.head (detached)\x00"))
	if err != nil || !ts.Detached || ts.Branch != "" {
		t.Fatalf("detached = %+v, %v", ts, err)
	}
	// No branch.head: never read as a branch, the decision fails closed.
	if _, err := gitx.ParseTrackedStatus([]byte("1 A. N... 000000 100644 100644 a b c.ts\x00")); err == nil {
		t.Fatal("a status without branch.head was accepted")
	}
	// A truncated entry is an error, not a clean worktree.
	if _, err := gitx.ParseTrackedStatus([]byte("# branch.head x\x001 A.\x00")); err == nil {
		t.Fatal("a truncated entry was accepted")
	}
}
