package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CA-TREINO-02
func TestVariationsOverlayInOrderAndUnknownFails(t *testing.T) {
	t.Parallel()
	root := WriteFiles(t, t.TempDir(), map[string]string{
		"m/README.md":                       "model",
		"m/a.go":                            "package a",
		"variacoes/com-agents-md/AGENTS.md": "agents",
		"variacoes/extra/README.md":         "extra",
		"variacoes/extra/.github/x.yml":     "on: push",
	})
	dst := filepath.Join(t.TempDir(), "copy")
	files, err := copyProject(root, "m", []string{"extra"}, dst)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"README.md": "extra", "a.go": "package a", "AGENTS.md": "agents", ".github/x.yml": "on: push"}
	for p, c := range want {
		if files[p] != c {
			t.Fatalf("%s = %q, want %q (files %v)", p, files[p], c, files)
		}
		if b, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(p))); err != nil || string(b) != c {
			t.Fatalf("on disk %s = %q, %v", p, b, err)
		}
	}
	if len(files) != len(want) {
		t.Fatalf("files = %v", files)
	}

	if _, err := copyProject(root, "m", []string{"sem-nada"}, t.TempDir()); err == nil ||
		!strings.Contains(err.Error(), `unknown variation "sem-nada"`) || !strings.Contains(err.Error(), "com-agents-md") {
		t.Fatalf("unknown variation: %v", err)
	}
	if _, err := copyProject(root, "../m", nil, t.TempDir()); err == nil || !strings.Contains(err.Error(), "unknown training project") {
		t.Fatalf("unknown project: %v", err)
	}
	WriteFiles(t, root, map[string]string{"m/.git/HEAD": "ref: refs/heads/main"})
	if _, err := copyProject(root, "m", nil, t.TempDir()); err == nil || !strings.Contains(err.Error(), "nested git repository") {
		t.Fatalf("nested repository: %v", err)
	}
}
