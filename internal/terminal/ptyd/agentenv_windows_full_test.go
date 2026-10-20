//go:build full && windows

package ptyd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// CA-WIN-04
//
// A program planted in the worktree (the current folder of the agent) is
// found by cmd with the environment of the machine and is not run with the
// environment of the agent.
func TestAProgramPlantedInTheWorktreeIsNotRunByTheNameOfAnotherOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran.txt")
	plant := "@echo off\r\necho planted> \"" + marker + "\"\r\n"
	if err := os.WriteFile(filepath.Join(dir, "planted.cmd"), []byte(plant), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(env []string) error {
		c := exec.Command("cmd", "/c", "planted")
		c.Dir, c.Env = dir, env
		return c.Run()
	}

	// Control: with the environment as it is, cmd looks in the current folder
	// first, so the plant would run (the test proves something).
	_ = run(os.Environ())
	if _, err := os.Stat(marker); err != nil {
		t.Skipf("the machine does not run a program of the current folder by default (%v): nothing to prove", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	if err := run(agentEnv("windows", os.Environ(), dir)); err == nil {
		t.Error("planted.cmd was found with the environment of the agent")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the program planted in the worktree ran with the environment of the agent")
	}
}
