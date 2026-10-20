package providers_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// An empty OS is this machine's: the hook line of Antigravity is in the form
// of the shell of this system, not always the one of sh.
func TestAntigravityHookLineOfTheMachineWhenTheOSIsEmpty(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "wt")
	binary := filepath.Join(t.TempDir(), "mestre")
	a := providers.NewAntigravity(providers.Options{})
	files, err := a.HookFiles(providers.HookFilesRequest{Root: root, Binary: binary})
	if err != nil || len(files) != 1 {
		t.Fatalf("%v, %v", files, err)
	}
	doc, err := files[0].Merge(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `[ -n "$MESTRE_AGENT_ID" ]`
	if runtime.GOOS == "windows" {
		want = "if not defined MESTRE_AGENT_ID (echo {}) else if exist "
	}
	if !strings.Contains(string(doc), strings.ReplaceAll(want, `"`, `\"`)) {
		t.Fatalf("the hook file does not have the line of %s (%s):\n%s", runtime.GOOS, want, doc)
	}
	// agy breaks the quotes of a line of cmd: the Windows line has none.
	if runtime.GOOS == "windows" && strings.Contains(string(doc), `\"`) {
		t.Fatalf("the Windows hook line has quotes:\n%s", doc)
	}
}
