package app

import (
	"context"
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/orchestration/usage"
	"github.com/lucasndas/mestre/internal/providers"
)

// sheetsOnly is the presets with only the list of sheets.
type sheetsOnly struct {
	characters.Presets
	sheets []presets.Sheet
}

func (s sheetsOnly) Sheets(context.Context) ([]presets.Sheet, error) { return s.sheets, nil }

// CA-USO-04: the alternatives offered when a provider cuts a character are the
// sheets of the same class on another provider or account, never other classes.
func TestTheAlternativesOfACutCharacterAreOfTheSameClass(t *testing.T) {
	t.Parallel()
	d := usageDirectory{presets: sheetsOnly{sheets: []presets.Sheet{
		{Name: "Revisor", Class: "Revisor", Agent: "codex"},
		{Name: "Revisor Claude", Class: "Revisor", Agent: "claude-code"},
		{Name: "Revisor (conta trabalho)", Class: "Revisor", Agent: "codex", Account: "trabalho"},
		{Name: "Dev Claude", Class: "Dev", Agent: "claude-code"},
	}}}
	got, err := d.Alternatives(context.Background(), usage.Subject{Sheet: "Revisor", Class: "Revisor", Provider: providers.Codex})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Revisor Claude", "Revisor (conta trabalho)"}; !slices.Equal(got, want) {
		t.Errorf("alternatives = %v; want %v (the same class only, not the sheet itself, not Dev)", got, want)
	}
}
