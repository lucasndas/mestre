package ptyd_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/terminal"
	"github.com/mestre-ia/mestre/internal/terminal/ptyd"
	"github.com/mestre-ia/mestre/internal/testkit"
	"github.com/mestre-ia/mestre/internal/testkit/fakeagent"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// startScreen runs the fake agent of a provider until it stops printing
// and returns what it printed, as raw bytes with their escape sequences.
// The fake agent prints the start screens of the provider it imitates.
func startScreen(t *testing.T, program string, trusted bool) string {
	t.Helper()
	root := t.TempDir()
	work, home, cfg := filepath.Join(root, "work"), filepath.Join(root, "home"), filepath.Join(root, "cfg")
	files := map[string]string{"work/.git/HEAD": "ref: refs/heads/main\n", "home/.keep": ""}
	if trusted {
		files["cfg/.credentials.json"] = "{}"
		files["cfg/.claude.json"] = fmt.Sprintf(`{"numStartups": 3, "projects": {%q: {"hasTrustDialogAccepted": true}}}`, filepath.ToSlash(work))
	}
	testkit.WriteFiles(t, root, files)
	var out string
	synctest.Test(t, func(t *testing.T) {
		l := &fakeagent.MemLauncher{Clock: clock.Real{}, Reporter: t}
		p, err := l.Start(t.Context(), terminal.Spec{
			Path: program, Dir: work,
			Env: []string{"HOME=" + home, "MESTRE_AGENT_ID=a1", "CLAUDE_CONFIG_DIR=" + cfg},
		})
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		out = p.(*fakeagent.MemProcess).Output()
		_ = p.Kill()
	})
	return out
}

// The start screens of the fake agent, including the ones the real round
// does not show (the trust and the login); the real CLIs' are in
// realscreens_test.go (CA-TERM-02).
func TestEmulatorShowsTheStartScreenOfEachProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, program string
		trusted       bool
		// promptRow is the row of the input line, or -1 when the start
		// screen is a question (trust, login) instead.
		wantPrompt bool
	}{
		{"claude, folder trusted", "claude", true, true},
		{"claude, folder not trusted", "claude", false, false},
		{"codex", "codex", false, false},
		{"antigravity", "agy", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw := startScreen(t, tt.program, tt.trusted)
			if raw == "" {
				t.Fatal("the agent printed nothing")
			}
			// Wide enough that no line wraps: the agent prints its work folder, which
			// is long on Windows.
			em := ptyd.NewVTEmulator(400, 40, 100, nil)
			defer em.Close()

			em.Write([]byte(raw))
			screen := em.Screen()

			if strings.ContainsAny(screen, "\x1b\x07") {
				t.Fatalf("control sequence on the screen:\n%q", screen)
			}
			// The screen holds the text the agent printed, line by line.
			var want []string
			for _, l := range strings.Split(logging.StripANSI(raw), "\r\n") {
				want = append(want, strings.TrimRight(l, " "))
			}
			if got, w := screen, strings.TrimRight(strings.Join(want, "\n"), "\n"); got != w {
				t.Fatalf("screen:\n%q\nwant the printed text:\n%q", got, w)
			}
			if tt.wantPrompt {
				// The prompt marker is the last line: "\r\n> " after k line breaks.
				row := strings.Count(raw, "\r\n")
				lines := strings.Split(screen, "\n")
				if row >= len(lines) || lines[row] != ">" {
					t.Fatalf("prompt not on row %d of:\n%q", row, screen)
				}
				if col, r := em.Cursor(); r != row || col != 2 {
					t.Fatalf("cursor at %d,%d, want 2,%d", col, r, row)
				}
			}
		})
	}
}
