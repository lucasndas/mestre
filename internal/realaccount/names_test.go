package realaccount_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/realaccount"
)

// CA-CONTAS-01
func TestTheRealAccountTestsRefuseAMachineNotDeclaredDisposable(t *testing.T) {
	t.Parallel()
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == realaccount.EnvDisposable {
				return v
			}
			return ""
		}
	}
	for _, v := range []string{"", "0", "true", "yes", "disposable", "1 "} {
		err := realaccount.CheckDisposable(env(v))
		if err == nil || !strings.Contains(err.Error(), realaccount.EnvDisposable) {
			t.Errorf("%s=%q: %v, want a refusal that names the variable", realaccount.EnvDisposable, v, err)
		}
	}
	if err := realaccount.CheckDisposable(env("1")); err != nil {
		t.Errorf("%s=1: %v, want it accepted", realaccount.EnvDisposable, err)
	}
}
