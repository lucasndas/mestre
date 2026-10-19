package chain_test

import (
	"encoding/json"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/chain"
)

// Part of CA-APROV-FLUXO-01: the impression of a target does not depend on how
// it was written, and never gives two different targets the same one.
func TestTheImpressionOfATarget(t *testing.T) {
	t.Parallel()
	imp := func(s string) (string, error) { return chain.Impress(json.RawMessage(s)) }
	a, _ := imp(`{"repo":"a/b","pr":7,"head":"aaa"}`)
	b, _ := imp(` { "head": "aaa", "pr": 7, "repo": "a/b" } `)
	if a == "" || a != b {
		t.Errorf("the same target written two ways: %q %q", a, b)
	}
	x, _ := imp(`{"n":9007199254740993}`)
	y, _ := imp(`{"n":9007199254740992}`)
	if x == y {
		t.Error("two integers above 2^53 share an impression")
	}
	for name, bad := range map[string]string{
		"repeated key": `{"head":"A","head":"B"}`, "nested repeat": `{"a":[{"k":1,"k":2}]}`, "two values": `{"a":1} {"b":2}`,
		"bad UTF-8": "{\"a\":\"\xff\"}", "not JSON": `{`, "empty": ``,
	} {
		if _, err := imp(bad); err == nil {
			t.Errorf("%s has an impression", name)
		}
	}
}
