package hook_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// The allow of the dispatcher carries the decided tool to the formatter, so
// Antigravity repeats the allowed command in permissionOverrides and its own
// permission screen does not ask again
// (docs/provedores/antigravity.md#verificar-na-implementação).
func TestAnAllowedCommandIsRepeatedInPermissionOverridesForAntigravity(t *testing.T) {
	t.Parallel()
	files, mc := newFiles(t), machine()
	files.snapshot(mc, rules(t, qaYAML))
	r := call{provider: providers.Antigravity, event: "PreToolUse", stdin: payload(providers.Antigravity, "npm test"),
		dial: down, files: files, machine: mc}.run(t.Context())
	if v, why := decision(t, providers.Antigravity, r); v != "allow" {
		t.Fatalf("decision %s (%s)", v, why)
	}
	var m struct {
		PermissionOverrides []string `json:"permissionOverrides"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &m); err != nil || !slices.Equal(m.PermissionOverrides, []string{"command(npm test)"}) {
		t.Fatalf("stdout %s: %v", r.stdout, err)
	}
	// What the rules deny carries no grant.
	r = call{provider: providers.Antigravity, event: "PreToolUse", stdin: payload(providers.Antigravity, "curl example.com"),
		dial: down, files: files, machine: mc}.run(t.Context())
	if v, _ := decision(t, providers.Antigravity, r); v != "deny" {
		t.Fatalf("curl: %s", r.stdout)
	}
	var d map[string]any
	_ = json.Unmarshal([]byte(r.stdout), &d)
	if _, has := d["permissionOverrides"]; has {
		t.Fatalf("a denial with a grant: %s", r.stdout)
	}
}
