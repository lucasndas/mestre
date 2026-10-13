//go:build linux

// Command 01-caller-identity is the M1 spike "identificação do chamador
// subindo a árvore de processos" (docs/arquitetura/seguranca.md#verificar-na-implementação).
//
// A minimal engine accepts the CLI on a Unix socket, reads SO_PEERCRED and
// SO_PEERPIDFD and decides the terminal of the caller in four modes (tree,
// tree+sid, subreaper, cgroup) for a matrix of real process shapes.
//
//	go run ./tools/spikes/linux/01-caller-identity            # matrix + costs
//	go run ./tools/spikes/linux/01-caller-identity provider -- claude -p '…'
//
// The cgroup column needs a delegated cgroup; without one the command
// re-runs itself under `systemd-run --user --scope -p Delegate=yes`.
// The same matrix is asserted by spike_test.go, outside every suite:
//
//	go test -tags spike ./tools/spikes/linux/01-caller-identity
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func main() {
	if code, ok := RunRole(os.Args[1:]); ok {
		os.Exit(code)
	}
	if len(os.Args) > 1 && os.Args[1] == "provider" {
		if err := runProvider(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	cgroupOnly := flag.Bool("cgroup-only", false, "run only the cgroup column (used by the re-exec)")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if *cgroupOnly {
		os.Exit(printMatrix(ctx, []Mode{ModeCgroup}))
	}
	code := printMatrix(ctx, []Mode{ModeTree, ModeTreeSession, ModeSubreaper})
	if _, err := DelegatedCgroup(); err == nil {
		code |= printMatrix(ctx, []Mode{ModeCgroup})
	} else {
		self, _ := os.Executable()
		cmd := exec.CommandContext(ctx, "systemd-run", "--user", "--scope", "-p", "Delegate=yes", "--quiet", "--collect", "--", self, "-cgroup-only")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		fmt.Println("\ncgroup column via systemd-run --user --scope -p Delegate=yes:")
		if err := cmd.Run(); err != nil {
			fmt.Println("cgroup column unavailable:", err)
		}
	}
	printCosts()
	os.Exit(code)
}

// printMatrix runs every scenario in each mode, prints the decisions and
// returns 1 if any differs from Scenario.Want.
func printMatrix(ctx context.Context, modes []Mode) int {
	base := ""
	for _, m := range modes {
		if m == ModeCgroup {
			var err error
			if base, err = DelegatedCgroup(); err != nil {
				fmt.Println("no delegated cgroup:", err)
				return 1
			}
		}
	}
	fmt.Printf("%-14s", "scenario")
	for _, m := range modes {
		fmt.Printf(" %-10s", m)
	}
	fmt.Println(" (Y identified, N refused, ! unexpected)")
	code := 0
	for _, sc := range Scenarios {
		if sc.Needs != "" {
			if _, err := exec.LookPath(sc.Needs); err != nil {
				fmt.Printf("%-14s skipped: no %s\n", sc.Name, sc.Needs)
				continue
			}
		}
		fmt.Printf("%-14s", sc.Name)
		var notes []string
		for _, m := range modes {
			res, err := sc.Run(ctx, m, base)
			if err != nil {
				fmt.Printf(" %-10s", "error")
				notes = append(notes, err.Error())
				code = 1
				continue
			}
			mark := map[bool]string{true: "Y", false: "N"}[res.Identified]
			if res.Identified != sc.Want[m] {
				mark += "!"
				code = 1
			}
			fmt.Printf(" %-10s", mark)
			if !res.Identified {
				notes = append(notes, fmt.Sprintf("%s: %s", m, res.Reason))
			}
			if res.PidfdErr != nil {
				notes = append(notes, fmt.Sprintf("%s: SO_PEERPIDFD: %v", m, res.PidfdErr))
			}
			if len(res.Chain) > 0 && m == ModeTreeSession {
				notes = append(notes, "chain: "+chainString(res.Chain))
			}
		}
		fmt.Println()
		for _, n := range notes {
			fmt.Println("    " + n)
		}
	}
	return code
}

func chainString(c []Stat) string {
	parts := make([]string, len(c))
	for i, s := range c {
		parts[i] = s.String()
	}
	return strings.Join(parts, " <- ")
}

// printCosts measures the identification itself (no process start).
func printCosts() {
	self := os.Getpid()
	walk := testing.Benchmark(func(b *testing.B) {
		for b.Loop() {
			_, _, _ = Walk(SysProc{}, self, func(Stat) bool { return false })
		}
	})
	_, chain, _ := Walk(SysProc{}, self, func(Stat) bool { return false })
	cg := testing.Benchmark(func(b *testing.B) {
		for b.Loop() {
			_, _ = Cgroup(self)
		}
	})
	fmt.Printf("\ncost: tree walk of depth %d: %v/op; /proc/<pid>/cgroup read: %v/op\n",
		len(chain), time.Duration(walk.NsPerOp()), time.Duration(cg.NsPerOp()))
}

// runProvider starts a real provider command as a terminal root (new
// session, tree+sid mode, agent "agent-A") and prints every connection
// decision with its chain until the command exits.
func runProvider(args []string) error {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return errors.New("usage: provider -- <command…>")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	dir, err := os.MkdirTemp("", "spk-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	e, err := NewEngine(ModeTreeSession, dir+"/s.sock", nil)
	if err != nil {
		return err
	}
	defer e.Close()
	script := `exec "$@" </dev/null`
	r, err := e.Launch(Spec{ID: "provider", AgentID: "agent-A", Dir: dir,
		Script: fmt.Sprintf("set -- %s; %s", shellQuote(args), script)})
	if err != nil {
		return err
	}
	fmt.Printf("terminal root pid=%d; SPIKE_BIN=%s\n", r.PID, mustExe())
	done := make(chan error, 1)
	go func() { done <- r.Stop(ctx) }()
	for {
		select {
		case res := <-e.Results:
			fmt.Printf("%s identified=%v %s\n    chain: %s\n", res.Label, res.Identified, res.Reason, chainString(res.Chain))
		case err := <-done:
			log, _ := os.ReadFile(dir + "/provider.log")
			fmt.Printf("provider exited (%v); output:\n%s\n", err, log)
			return nil
		}
	}
}

func mustExe() string {
	p, _ := os.Executable()
	return p
}

func shellQuote(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}
