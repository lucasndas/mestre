package cli_test

import (
	"regexp"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/cli"
)

// The example of `ask` in the help must not suggest a time limit above the
// largest one an ask has (9 minutes): the engine would cut it silently.
func TestTheExampleOfAskDoesNotAskForMoreThanTheLargestTimeLimit(t *testing.T) {
	t.Parallel()
	spec, ok := cli.Default().Get("ask")
	if !ok {
		t.Fatal("no ask")
	}
	m := regexp.MustCompile(`--timeout (\d+[smh])`).FindStringSubmatch(spec.Example)
	if m == nil {
		return
	}
	d, err := time.ParseDuration(m[1])
	if err != nil || d > 9*time.Minute {
		t.Errorf("the example %q asks for %s; the largest time limit of an ask is 9m", spec.Example, d)
	}
}
