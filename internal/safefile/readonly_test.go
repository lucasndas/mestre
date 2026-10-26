package safefile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mestre-ia/mestre/internal/safefile"
)

// A file written without the write bit is read only on both systems, is replaced
// by the next write (the copies of the scrolls are rewritten at every change) and
// is taken away like any other.
func TestAReadOnlyFileIsReplacedAndRemoved(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "pasta", "copia.md")
	for i, text := range []string{"um", "dois"} {
		if err := safefile.Write(p, []byte(text), safefile.Options{Mode: 0o444}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		if b, err := os.ReadFile(p); err != nil || string(b) != text {
			t.Fatalf("read %d: %q, %v", i, b, err)
		}
		if f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0); err == nil {
			_ = f.Close()
			t.Fatalf("write %d: the file opens for writing", i)
		}
	}
	if err := safefile.Remove(p, nil); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("the file is still there: %v", err)
	}
}
