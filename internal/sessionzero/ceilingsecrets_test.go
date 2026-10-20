package sessionzero_test

import (
	"regexp"
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

// Part of CA-TETO-07: the files.deny of the default ceiling are the globs of
// permissoes_e_segredos of the default approval policy, less the two that there
// only ask (docs/classes/teto-do-workspace.md#arquivo).
func TestTheDefaultCeilingDeniesTheSecretGlobsOfTheDefaultPolicyButTheTwoThatOnlyAsk(t *testing.T) {
	t.Parallel()
	m := regexp.MustCompile(`permissoes_e_segredos: \[(.*)\]`).FindStringSubmatch(sessionzero.DefaultPolicyYAML)
	if m == nil {
		t.Fatal("the default policy has no permissoes_e_segredos")
	}
	var want []string
	for _, g := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(m[1], -1) {
		if g[1] != "**/secrets/**" && g[1] != "**/*permission*" {
			want = append(want, g[1])
		}
	}
	c := classes.DefaultCeiling()
	if got := *c.Permissions.Files.Deny; !slices.Equal(got, want) {
		t.Errorf("files.deny of the default ceiling = %q; want %q", got, want)
	}
}
