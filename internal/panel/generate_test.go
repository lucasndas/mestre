package panel_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/gzuidhof/tygo/config"
	"github.com/gzuidhof/tygo/tygo"
)

// The TypeScript types of the API are generated from the Go types (go generate in
// internal/panel) and committed in web/src/gen/. This fails when the committed
// file is not what tygo makes of the Go types today, like the glossary does.
func TestGeneratedTypesAreNotStale(t *testing.T) {
	t.Parallel()
	cfg := config.ReadFromFilepath("tygo.yaml")
	committed := filepath.Join("..", "..", "web", "src", "gen", "panel.ts")
	want, err := os.ReadFile(committed)
	if err != nil {
		t.Fatalf("the generated file is missing (run go generate in internal/panel): %v", err)
	}
	out := filepath.Join(t.TempDir(), "panel.ts")
	cfg.Packages[0].OutputPath = out
	if err := tygo.New(&cfg).Generate(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(normalize(got), normalize(want)) {
		t.Fatal("web/src/gen/panel.ts is stale: run `go generate ./internal/panel` and commit it")
	}
}

func normalize(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
