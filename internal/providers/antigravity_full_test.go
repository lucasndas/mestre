//go:build full

package providers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// This test binary is also the stand-in for the `mestre` binary at the stable
// path and for `agy` (AGYSTUB_STUB): the hook commands and the usage
// command are run as real processes, by the shell of the system.
func init() {
	mode := os.Getenv("AGYSTUB_STUB")
	if mode == "" {
		return
	}
	switch mode {
	case "hook":
		in, _ := io.ReadAll(os.Stdin)
		out, _ := json.Marshal(map[string]any{"args": os.Args[1:], "stdin": string(in)})
		fmt.Println(string(out))
		if os.Getenv("AGYSTUB_FAIL") != "" {
			os.Exit(3)
		}
		os.Exit(0)
	case "usage":
		if !slices.Equal(os.Args[1:], []string{"-p", "/usage", "--output-format", "json"}) ||
			os.Getenv("HOME") != os.Getenv("AGYSTUB_EXPECT_HOME") || slices.ContainsFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(strings.ToUpper(kv), "MESTRE_") }) {
			os.Exit(9)
		}
		fmt.Println(os.Getenv("AGYSTUB_OUT"))
		os.Exit(0)
	}
	os.Exit(8)
}

// stand copies this test binary to dir/name, as the stand-in of a program.
func stand(t *testing.T, dir, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	src, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		// A link, not a copy: a binary just written and executed in parallel
		// with forks of other tests fails with "text file busy".
		dst := filepath.Join(dir, name)
		if err := os.Symlink(src, dst); err != nil {
			t.Fatal(err)
		}
		return dst
	}
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, name)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
}

func runHook(t *testing.T, line, dir, stdin string, env ...string) (stdout string, exit int) {
	t.Helper()
	var out, errb bytes.Buffer
	c := hookShell(t.Context(), line, dir, append(os.Environ(), env...))
	c.Stdin, c.Stdout, c.Stderr = strings.NewReader(stdin), &out, &errb
	err := c.Run()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		return out.String(), ee.ExitCode()
	case err != nil:
		t.Fatalf("%q: %v", line, err)
	}
	return out.String(), 0
}

// CA-AGY-06
func TestTheGeneratedHookPrintsTheNeutralAnswerWithoutTheMestreBinary(t *testing.T) {
	t.Parallel()
	worktree := t.TempDir()
	dir := filepath.Join(worktree, ".agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "mestre"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	// A stable path with a space and a quote, where nothing is installed; on
	// Windows a bare one, the only kind the line of cmd takes.
	binary := filepath.Join(t.TempDir(), "mestre bin it's", name)
	if runtime.GOOS == "windows" {
		binary = filepath.Join(t.TempDir(), "mestre-bin", name)
	}
	cmds := hookCommands(t, worktree, binary)
	if len(cmds) != 5 {
		t.Fatalf("commands = %v", cmds)
	}
	payload := string(recorded(t, "antigravity", "PreToolUse"))
	for event, line := range cmds {
		// With the agent id the command reaches the binary check, so a
		// missing binary is what yields the neutral answer; without it the
		// first condition already does. On Windows a character (with the id)
		// gets {} there, which blocks PreToolUse: the line of cmd gives no
		// allow the dispatcher did not give.
		for _, env := range [][]string{{"MESTRE_AGENT_ID=agy-1"}, {"MESTRE_AGENT_ID="}} {
			want := "{}"
			if event == "PreToolUse" && (runtime.GOOS != "windows" || env[0] == "MESTRE_AGENT_ID=") {
				want = `{"decision":"allow"}`
			}
			out, exit := runHook(t, line, dir, payload, env...)
			if exit != 0 || strings.TrimSpace(out) != want {
				t.Errorf("%s with %v: exit %d, stdout %q; want exit 0 and %s", event, env, exit, out, want)
			}
		}
	}
}

// With the binary in place the command runs it with the event and the stdin,
// and what it does is what the hook does: a failure of the binary is not
// turned into an allow.
func TestTheGeneratedHookRunsTheMestreBinaryAndKeepsItsExit(t *testing.T) {
	t.Parallel()
	worktree := t.TempDir()
	dir := filepath.Join(worktree, ".agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(t.TempDir(), "mestre bin it's")
	if runtime.GOOS == "windows" {
		folder = filepath.Join(t.TempDir(), "mestre-bin")
	}
	binary := stand(t, folder, "mestre")
	cmds := hookCommands(t, worktree, binary)
	payload := string(recorded(t, "antigravity", "PreToolUse"))

	out, exit := runHook(t, cmds["PreToolUse"], dir, payload, "MESTRE_AGENT_ID=agy-1", "AGYSTUB_STUB=hook")
	var got struct {
		Args  []string `json:"args"`
		Stdin string   `json:"stdin"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || exit != 0 {
		t.Fatalf("exit %d, stdout %q: %v", exit, out, err)
	}
	if !slices.Equal(got.Args, []string{"hook", "--provider", "antigravity", "--event", "PreToolUse"}) || got.Stdin != payload {
		t.Errorf("the binary got %q and stdin %q", got.Args, got.Stdin)
	}

	out, exit = runHook(t, cmds["PreToolUse"], dir, payload, "MESTRE_AGENT_ID=agy-1", "AGYSTUB_STUB=hook", "AGYSTUB_FAIL=1")
	if exit != 3 || strings.Contains(out, `"decision":"allow"`) {
		t.Errorf("a failing binary: exit %d, stdout %q; want its exit 3 and no allow", exit, out)
	}
}

// ReadUsage with the real process runner: the command, the HOME of the
// profile and the cleaned environment reach a real `agy`.
func TestReadUsageRunsTheCommandWithTheProfileHOME(t *testing.T) {
	t.Parallel()
	bin := stand(t, t.TempDir(), "agy")
	home := t.TempDir()
	prov := agy(t, providers.Options{Exec: runProcess})
	u, err := prov.ReadUsage(providers.UsageRequest{
		AgentID: "a1", Command: bin, OS: runtime.GOOS,
		Env: append(os.Environ(), "HOME="+home, "MESTRE_AGENT_ID=a1", "MESTRE_ENDPOINT=unix:///x", "AGYSTUB_STUB=usage", "AGYSTUB_EXPECT_HOME="+home,
			"AGYSTUB_OUT="+compact(t, recorded(t, "antigravity", "Usage"))),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(u.LimitWindows) != 4 || u.LimitWindows[0].UsedPct != 38 {
		t.Errorf("windows = %+v", u.LimitWindows)
	}
	_, err = prov.ReadUsage(providers.UsageRequest{Command: bin, Env: []string{"HOME=" + home, "AGYSTUB_STUB=usage", "AGYSTUB_EXPECT_HOME=" + home + "x"}})
	if err == nil {
		t.Error("a failing agy was read as usage")
	}
}

// runProcess is the runner a designated layer gives the adapter: the real
// process, with the environment as the engine built it.
func runProcess(ctx context.Context, c providers.Command) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Env, cmd.Dir = c.Env, c.Dir
	return cmd.Output()
}

func compact(t *testing.T, raw []byte) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
