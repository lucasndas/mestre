package gitx_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/mestre-ia/mestre/internal/gitx"
)

// Two spellings of one folder (case, 8.3 short names) share one lock: the
// operations that change a repository stay serialized however it was opened.
func TestLockKeyIsCanonicalOnWindows(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "Espelho Longo Do Workspace.git")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	long := gitx.LockKey(dir)
	if got := gitx.LockKey(strings.ToUpper(dir)); got != long {
		t.Errorf("upper case: %q, want %q", got, long)
	}
	if got := gitx.LockKey(dir + `\`); got != long {
		t.Errorf("trailing separator: %q, want %q", got, long)
	}
	u, err := windows.UTF16FromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetShortPathName(&u[0], &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		t.Skipf("no 8.3 names on this volume: %v", err)
	}
	short := windows.UTF16ToString(buf[:n])
	if short == dir {
		t.Skip("the volume did not create an 8.3 name")
	}
	if got := gitx.LockKey(short); got != long {
		t.Errorf("8.3 name %q: %q, want %q", short, got, long)
	}
}
