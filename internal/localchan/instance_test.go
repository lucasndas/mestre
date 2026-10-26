package localchan_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/localchan"
)

// maxAccountName is the longest name of a local account on Windows (the
// strictest of the systems; useradd on Linux takes 32).
const maxAccountName = 20

// The account of the agents is named after the instance, so the name of an
// instance must give an account that fits the system and that a system
// which ignores the case cannot confuse with the account of another instance
// (docs/arquitetura/componentes.md#instâncias).
// CA-LOCAL-16
func TestTheNameOfAnInstanceGivesAnAccountThatFitsAndHasNoCaseTwin(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"principal", "a", "ci_2", "seg-1", "abcdef", "0"} {
		if !localchan.ValidInstance(name) {
			t.Errorf("ValidInstance(%q) = false", name)
		}
		if got := localchan.AgentAccount(name); len(got) > maxAccountName || got != strings.ToLower(got) {
			t.Errorf("AgentAccount(%q) = %q: longer than %d or not lower case", name, got, maxAccountName)
		}
	}
	for _, name := range []string{"", "abcdefg", strings.Repeat("a", 32), "X", "Ci", "-a", "_a", "a b", "../x", "a.b", "Principal"} {
		if localchan.ValidInstance(name) {
			t.Errorf("ValidInstance(%q) = true: the account would not fit or would have a case twin", name)
		}
		if _, err := localchan.SocketPath(name); err == nil {
			t.Errorf("SocketPath(%q) accepted the name", name)
		}
	}
}
