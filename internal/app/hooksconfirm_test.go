package app

import (
	"encoding/json"
	"testing"
)

// The Citadel starts a character whose class has hooks on a Tower only when the answer of
// character.start says the Tower applies the same package of hooks: no field (an old Tower) or
// another hash is refused, so the character never runs without them.
func TestACharacterWithHooksIsStartedOnATowerOnlyIfItConfirmsTheSamePackage(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		ans  string
		ok   bool
	}{
		{"the same package", `{"class_hooks":"h1"}`, true},
		{"an old Tower: no field", `{}`, false},
		{"an old Tower: no result", ``, false},
		{"another package", `{"class_hooks":"h2"}`, false},
	} {
		if why := hooksNotConfirmed(json.RawMessage(c.ans), "h1"); (why == "") != c.ok {
			t.Errorf("%s: %q, want confirmed = %v", c.name, why, c.ok)
		}
	}
}
