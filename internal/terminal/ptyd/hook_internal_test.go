package ptyd

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/terminal/ptywire"
)

// Part of CA-TERM-18: the output of a hook comes back cut at 64 KiB, and a hook
// that writes more is not cut short.
func TestTheOutputOfAHookIsCutAt64KiBAndTheWriterIsNotStopped(t *testing.T) {
	t.Parallel()
	b := &cappedBuffer{max: ptywire.MaxHookOutput}
	chunk := []byte(strings.Repeat("x", 10<<10))
	for range 10 { // 100 KiB
		if n, err := b.Write(chunk); n != len(chunk) || err != nil {
			t.Fatalf("Write = %d, %v; want the whole write", n, err)
		}
	}
	if got := b.String(); len(got) != ptywire.MaxHookOutput || !b.cut {
		t.Fatalf("kept %d bytes, cut %v; want 64 KiB and cut", len(got), b.cut)
	}
	small := &cappedBuffer{max: ptywire.MaxHookOutput}
	_, _ = small.Write([]byte("ok"))
	if small.cut || small.String() != "ok" {
		t.Fatalf("a short output was cut: %q %v", small.String(), small.cut)
	}
}

// Part of CA-TERM-18: the folder of a hook is the one of the agent or inside it.
func TestTheFolderOfAHookIsTheOneOfTheAgentOrInsideIt(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	for dir, ok := range map[string]bool{"": true, base: true, base + "/sub": true, base + "/sub/../x": true, base + "/..": false, base + "/../other": false, "relative": false} {
		got, err := hookDir(base, dir)
		if (err == nil) != ok {
			t.Errorf("hookDir(%q) = %q, %v; want ok %v", dir, got, err, ok)
		}
	}
	if _, err := hookDir("", "/x"); err == nil {
		t.Error("a hook ran in a folder when the agent has none")
	}
}
