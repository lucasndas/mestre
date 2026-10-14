package conformance_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/conformance"
	"github.com/lucasndas/mestre/internal/providers"
)

// transcript is a recorded answer of testdata/transcripts: the answers of
// the real round of 2026-10-14, and variants without the codes or with the
// test word of the project.
func transcript(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "transcripts", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The codes of the real runs the transcripts come from.
var (
	claudeCodes = conformance.Codes{Mestre: "M-R4P88A", Project: "P-J329WZ", Skill: "S-CEVX8E"}
	codexCodes  = conformance.Codes{Mestre: "M-BS6SLZ", Project: "P-36NSCR"}
)

// CA-CONF-05
func TestTheVerifierOfStepsTwoAndThreeClassifiesTheRecordedTranscripts(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		file     string
		codes    conformance.Codes
		provider providers.ID
		pass     bool
	}{
		{"claude-2.1.299-step2.txt", claudeCodes, providers.ClaudeCode, true},
		{"codex-0.162.1-step2.txt", codexCodes, providers.Codex, true},
		{"without-mestre-code.txt", claudeCodes, providers.ClaudeCode, false},
		{"azul-instead-of-verde.txt", claudeCodes, providers.ClaudeCode, false},
		{"azul-and-verde-without-the-line.txt", claudeCodes, providers.ClaudeCode, false},
		{"without-the-shell-tool.txt", claudeCodes, providers.ClaudeCode, false},
		// The summary of the thought of Antigravity quotes the question above
		// the answer: the answer's line counts.
		{"antigravity-1.3.5-step10.txt", conformance.Codes{Mestre: "M-J4RRVJ", Project: "P-5F2A9R"}, providers.Antigravity, true},
		// The answer of Codex does not hold the codes of the Claude run.
		{"codex-0.162.1-step2.txt", claudeCodes, providers.Codex, false},
	} {
		got := conformance.CheckInstructions(transcript(t, c.file), c.codes, c.provider)
		if got.Pass != c.pass {
			t.Errorf("step 2, %s (%s): %s, want pass=%t", c.file, c.provider, got, c.pass)
		}
	}
	for _, c := range []struct {
		file string
		pass bool
	}{{"claude-2.1.299-step3.txt", true}, {"step3-without-the-code.txt", false}, {"claude-2.1.299-step2.txt", false}} {
		if got := conformance.CheckSkill(transcript(t, c.file), claudeCodes); got.Pass != c.pass {
			t.Errorf("step 3, %s: %s, want pass=%t", c.file, got, c.pass)
		}
	}
}
