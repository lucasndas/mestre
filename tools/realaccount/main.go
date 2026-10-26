// Command realaccount runs the real-account tests of internal/realaccount on a
// disposable machine (docs/qualidade/contas-reais.md):
//
//	MESTRE_REAL_ACCOUNT=1 go run ./tools/realaccount run [-budget 10m] [-timeout 25m] [-keep]
//
// It builds the test binary and the mestre-pty, creates the accounts of the
// installation (the engine's and the agents', or the agents' alone on
// Windows), installs the mestre-launcher as the packaged unit (Linux) or as a
// LocalSystem service (Windows), runs the tests as the engine, in the unit or
// service the launcher accepts, and removes everything. It refuses to do any of
// it unless the machine was declared disposable.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/mestre-ia/mestre/internal/realaccount"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// options are what a run needs, besides the machine.
type options struct {
	// budget is the time the whole run is expected to take, including the
	// build and the preparation: a longer run fails (the CI would otherwise
	// drift into minutes nobody asked for).
	budget time.Duration
	// timeout bounds the tests themselves.
	timeout time.Duration
	// keep leaves the accounts, units and files for a look afterwards.
	keep bool
}

// runner prepares the machine, runs the tests and cleans up; platformRun is
// the real one.
type runner func(ctx context.Context, out io.Writer, o options) error

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Getenv, clock.Real{}, platformRun))
}

func run(args []string, out io.Writer, getenv func(string) string, clk clock.Clock, do runner) int {
	if len(args) == 0 || args[0] != "run" {
		fmt.Fprintln(out, "usage: MESTRE_REAL_ACCOUNT=1 go run ./tools/realaccount run [-budget d] [-timeout d] [-keep]")
		return 2
	}
	var o options
	fl := flag.NewFlagSet("realaccount run", flag.ContinueOnError)
	fl.SetOutput(out)
	fl.DurationVar(&o.budget, "budget", 10*time.Minute, "fail if the whole run takes longer than this")
	fl.DurationVar(&o.timeout, "timeout", 25*time.Minute, "time limit of the tests")
	fl.BoolVar(&o.keep, "keep", false, "leave the accounts, units and files in place")
	if err := fl.Parse(args[1:]); err != nil {
		return 2
	}
	if err := realaccount.CheckDisposable(getenv); err != nil {
		fmt.Fprintln(out, "realaccount:", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	start := clk.Now()
	err := do(ctx, out, o)
	took := clock.Since(clk, start).Round(time.Second)
	switch {
	case err != nil:
		fmt.Fprintf(out, "FAIL realaccount in %s: %v\n", took, err)
		return 1
	case took > o.budget:
		fmt.Fprintf(out, "FAIL realaccount: %s, over the budget of %s\n", took, o.budget)
		return 1
	}
	fmt.Fprintf(out, "PASS realaccount in %s (budget %s)\n", took, o.budget)
	return 0
}
