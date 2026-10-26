package guildtpl

import (
	"testing"

	guilddb "github.com/mestre-ia/mestre/internal/store/guild"
)

func TestTheTemplateIsBuiltOnceAndThenReadFromTheSharedFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	builds := 0
	build := func() ([]byte, error) { builds++; return []byte("template"), nil }
	for range 3 {
		b, err := shared("guild", guilddb.Migrations, build)
		if err != nil || string(b) != "template" {
			t.Fatalf("shared = %q, %v", b, err)
		}
	}
	if builds != 1 {
		t.Fatalf("built %d times, want 1", builds)
	}
	if cachePath("guild", guilddb.Migrations) == cachePath("machine", guilddb.Migrations) {
		t.Fatal("two kinds share a file")
	}
}
