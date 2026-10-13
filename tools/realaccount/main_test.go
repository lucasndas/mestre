package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/realaccount"
	"github.com/lucasndas/mestre/pkg/clock"
)

func env(disposable string) func(string) string {
	return func(k string) string {
		if k == realaccount.EnvDisposable {
			return disposable
		}
		return ""
	}
}

var start = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

// CA-CONTAS-01
func TestTheToolTouchesNothingOnAMachineNotDeclaredDisposable(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"", "0", "yes"} {
		var out bytes.Buffer
		called := false
		code := run([]string{"run"}, &out, env(v), clock.NewFake(start), func(context.Context, io.Writer, options) error {
			called = true
			return nil
		})
		if code != 2 || called || !strings.Contains(out.String(), realaccount.EnvDisposable) {
			t.Errorf("%s=%q: code %d, prepared the machine = %v, output %q; want 2, nothing prepared and the variable named", realaccount.EnvDisposable, v, code, called, out.String())
		}
	}
	var out bytes.Buffer
	if code := run([]string{"prepare"}, &out, env("1"), clock.NewFake(start), nil); code != 2 {
		t.Errorf("an unknown command: code %d, want 2", code)
	}
}

// CA-CONTAS-02
func TestTheRunFailsOverItsBudgetOrWhenTheTestsFail(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		args    []string
		took    time.Duration
		err     error
		want    int
		message string
	}{
		"within the budget": {[]string{"run"}, 8 * time.Minute, nil, 0, "PASS realaccount in 8m0s (budget 10m0s)"},
		"over the budget":   {[]string{"run"}, 11 * time.Minute, nil, 1, "over the budget of 10m0s"},
		"own budget":        {[]string{"run", "-budget", "2m"}, 3 * time.Minute, nil, 1, "over the budget of 2m0s"},
		"tests fail":        {[]string{"run"}, time.Minute, errors.New("the tests failed"), 1, "FAIL realaccount in 1m0s: the tests failed"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			clk := clock.NewFake(start)
			var out bytes.Buffer
			code := run(c.args, &out, env("1"), clk, func(context.Context, io.Writer, options) error {
				clk.Advance(c.took)
				return c.err
			})
			if code != c.want || !strings.Contains(out.String(), c.message) {
				t.Errorf("code %d, output %q; want %d and %q", code, out.String(), c.want, c.message)
			}
		})
	}
}
