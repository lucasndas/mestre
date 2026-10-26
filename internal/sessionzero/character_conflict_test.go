package sessionzero_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/sessionzero"
)

// CA-CLI-SKL-05
func TestASharedSkillWithoutTheMarkerIsNotTouchedAndTheConflictIsReported(t *testing.T) {
	t.Parallel()
	e := newTableEnv(t, nil, nil)
	skill := filepath.Join(e.dir, ".agents", "skills", "mestre-comunicacao", "SKILL.md")
	const mine = "---\nname: mestre-comunicacao\ndescription: mine\n---\nMy own text.\n"
	write(t, skill, mine)
	rep := e.prepare()
	if got := read(t, skill); got != mine {
		t.Errorf("the skill without the marker was rewritten:\n%s", got)
	}
	if !slices.ContainsFunc(rep.Problems, func(p sessionzero.Problem) bool {
		return p.Path == ".agents/skills/mestre-comunicacao/SKILL.md" && strings.Contains(p.Reason, "conflito")
	}) {
		t.Errorf("the conflict is not in the report: %v", rep.Problems)
	}
	if !slices.Contains(e.notices.kinds(), "provider_files") {
		t.Errorf("no alert of the conflict: %v", e.notices.kinds())
	}
}
