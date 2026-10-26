package terminal_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/terminal"
)

// CA-TESTES-05
func TestExecLauncherRefusesInFastSuite(t *testing.T) {
	t.Parallel()
	env := map[string]string{terminal.EnvTestSuite: "fast"}
	l := terminal.NewExecLauncher(func(k string) string { return env[k] })

	p, err := l.Start(context.Background(), terminal.Spec{Path: "/bin/true"})

	if p != nil || !errors.Is(err, terminal.ErrExternalProcessInFastSuite) {
		t.Fatalf("Start = %v, %v; want ErrExternalProcessInFastSuite", p, err)
	}
	if want := "external process in fast suite: use the fake PTY"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not say %q", err, want)
	}
}

func TestGuardSuiteAllowsOtherSuites(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "full", "heavy"} {
		if err := terminal.GuardSuite(s); err != nil {
			t.Fatalf("GuardSuite(%q) = %v", s, err)
		}
	}
}
