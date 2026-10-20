package presets_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/presets"
)

// CA-CONF-07 (the sheet half): a character with the skills of the Mestre
// calls the CLI; one without them does not.
func TestASheetWithTheSkillsOfTheMestreNeedsTheCLI(t *testing.T) {
	t.Parallel()
	off := false
	for _, tt := range []struct {
		name   string
		skills *bool
		want   bool
	}{{"default", nil, true}, {"skills off", &off, false}} {
		req, err := presets.Requirements(presets.Base{Name: "codex"}, presets.Sheet{Name: "s", Agent: "codex", MestreSkills: tt.skills}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if req.CLI != tt.want {
			t.Errorf("%s: CLI = %v, want %v", tt.name, req.CLI, tt.want)
		}
	}
}
