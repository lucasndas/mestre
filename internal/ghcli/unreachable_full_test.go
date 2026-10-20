//go:build full

package ghcli_test

import (
	"context"
	"slices"
	"testing"
)

// The answer of `mestre pr update` warns when a Tower with a worktree of the table is
// not connected: the branch was sent from the Citadel without it (docs/orquestracao/mesas.md).
func TestPrUpdateWarnsOfTheTowersThatAreNotConnected(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	prOfTheTable(t, r)
	var asked []string
	towers := []string{}
	r.h.Unreachable = func(_ context.Context, key string) []string {
		asked = append(asked, key)
		return towers
	}
	if got := r.run("qa", "pr update", map[string]any{"title": "Um"}); !got.OK || got.Data["warning"] != nil || got.Data["unreachable_towers"] != nil {
		t.Fatalf("with every Tower connected: %+v", got)
	}
	towers = []string{"torre-1"}
	got := r.run("qa", "pr update", map[string]any{"title": "Dois"})
	list, _ := got.Data["unreachable_towers"].([]any)
	if !got.OK || len(list) != 1 || list[0] != "torre-1" || got.Data["warning"] == nil {
		t.Fatalf("with a Tower away: %+v", got)
	}
	if want := []string{"meu-app/42-login", "meu-app/42-login"}; !slices.Equal(asked, want) {
		t.Fatalf("asked for the tables %q, want %q", asked, want)
	}
}
