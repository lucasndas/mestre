package ptyd_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/terminal/ptyd"
	"github.com/mestre-ia/mestre/internal/testkit"
)

// The start screens of the real CLIs, captured as raw terminal output by the
// first conformance round (docs/provedores/conformidade.md, 2026-10-14):
// Claude Code, Codex and Antigravity draw them with the alternate screen,
// cursor moves, colors and wide characters.
var realStartScreens = []struct {
	origin, version string
	id              providers.ID
	// prompt is the input line of the CLI on its start screen and row the
	// line (from 0) where the recording puts it.
	prompt string
	row    int
}{
	{"claude-code", "2.1.299", providers.ClaudeCode, "❯", 46},
	{"codex", "0.162.1", providers.Codex, "›", 46},
	{"antigravity", "1.3.5", providers.Antigravity, ">", 8},
}

// CA-TERM-02
func TestEmulatorRendersTheRealStartScreenOfEachProvider(t *testing.T) {
	t.Parallel()
	reg := providers.NewRegistry(providers.Options{})
	for _, c := range realStartScreens {
		t.Run(c.origin, func(t *testing.T) {
			t.Parallel()
			rec, err := testkit.LoadRecording(testkit.Recordings(), c.origin, c.version, "screen-start")
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Cols, Rows int
				Raw        string
			}
			if err := json.Unmarshal(rec.Body, &body); err != nil || body.Cols == 0 || body.Raw == "" {
				t.Fatalf("recording %s/%s: %v", c.origin, c.version, err)
			}
			em := ptyd.NewVTEmulator(body.Cols, body.Rows, 100, nil)
			defer em.Close()
			em.Write([]byte(body.Raw))
			screen := em.Screen()

			if strings.ContainsAny(screen, "\x1b\x07") {
				t.Fatalf("control sequence on the screen:\n%q", screen)
			}
			// The input line is on the screen, below the banner, and the
			// adapter reads the screen as ready.
			lines := strings.Split(screen, "\n")
			row := -1
			for i, l := range lines {
				if strings.HasPrefix(strings.TrimSpace(l), c.prompt) {
					row = i
				}
			}
			if row != c.row {
				t.Fatalf("the prompt %q is on line %d of the screen, want line %d:\n%s", c.prompt, row, c.row, screen)
			}
			a, err := reg.For(c.id)
			if err != nil {
				t.Fatal(err)
			}
			if st := a.Screens(providers.ScreenInput{Text: screen, Stable: true}).State; st != providers.StateReady {
				t.Fatalf("the adapter reads the start screen as %s:\n%s", st, screen)
			}
		})
	}
}
