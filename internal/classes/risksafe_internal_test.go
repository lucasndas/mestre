package classes

import "testing"

// The list of known-safe programs holds no program that is a risk signal
// by its name: a signal program never passes as baixo.
func TestTheSafeListHoldsNoSignalProgram(t *testing.T) {
	t.Parallel()
	for n := range safeList {
		if g, ok := signalPrograms[n]; ok {
			t.Errorf("%s is in the safe list and a signal of %s", n, g)
		}
		if multiCall(n) || elevation[n] || agentCLIs[n] || wrapperPrograms[n] && n != "command" {
			t.Errorf("%s is in the safe list and runs other programs", n)
		}
	}
}
