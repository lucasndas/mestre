//go:build full

package gitx_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
)

// envRecord turns this test binary into a fake git that appends each call
// (arguments and the GIT_CONFIG_COUNT/KEY/VALUE environment) to the file it
// names.
const envRecord = "MESTRE_GITX_RECORD"

type recordedCall struct {
	Args []string `json:"args"`
	Env  []string `json:"env"`
}

func TestMain(m *testing.M) {
	if file := os.Getenv(envRecord); file != "" {
		os.Exit(recordCall(file))
	}
	os.Exit(m.Run())
}

func recordCall(file string) int {
	call := recordedCall{Args: os.Args[1:]}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_CONFIG_COUNT=") || strings.HasPrefix(kv, "GIT_CONFIG_KEY_") || strings.HasPrefix(kv, "GIT_CONFIG_VALUE_") {
			call.Env = append(call.Env, kv)
		}
	}
	line, _ := json.Marshal(call)
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 1
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", line)
	switch {
	case containsArg(call.Args, "version"):
		fmt.Println("git version 2.99.0")
	case containsArg(call.Args, "rev-parse"):
		wd, _ := os.Getwd()
		fmt.Printf("%s\ntrue\n", filepath.ToSlash(wd))
	}
	return 0
}

func containsArg(args []string, a string) bool {
	for _, x := range args {
		if x == a {
			return true
		}
	}
	return false
}

// The token of an Auth reaches git only through its environment, never its
// arguments (readable by every local account in /proc/<pid>/cmdline).
func TestRealAuthIsNeverInTheArguments(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	log := filepath.Join(root, "calls.jsonl")
	g := newReal(gitx.Options{Binary: self, Env: []string{envRecord + "=" + log}})
	ctx := context.Background()
	const secret = "ghs_recordedSecretToken0123456789abc"
	auth := gitx.BasicAuth("https://github.com/", "x-access-token", secret)

	dir := filepath.Join(root, "espelho.git")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mirror, err := g.Clone(ctx, "https://github.com/o/r.git", dir, gitx.CloneOptions{Bare: true, Auth: &auth})
	if err != nil {
		t.Fatal(err)
	}
	if err := mirror.FetchWithAuth(ctx, auth, "https://github.com/o/r.git", "+refs/heads/*:refs/heads/*"); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(log)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	withAuth := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var c recordedCall
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		args := strings.Join(c.Args, " ")
		if strings.Contains(args, secret) || strings.Contains(args, auth.Header) || strings.Contains(args, "extraHeader") {
			t.Errorf("credential in the arguments: %q", args)
		}
		if containsArg(c.Args, "clone") || containsArg(c.Args, "fetch") {
			withAuth++
			want := []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://github.com/.extraHeader", "GIT_CONFIG_VALUE_0=" + auth.Header}
			for _, w := range want {
				if !containsArg(c.Env, w) {
					t.Errorf("%s: environment %q lacks %q", c.Args, c.Env, w)
				}
			}
		} else if len(c.Env) > 0 {
			t.Errorf("%s: auth in the environment of a call without it: %q", c.Args, c.Env)
		}
	}
	if withAuth != 2 {
		t.Fatalf("recorded %d clone/fetch calls, want 2", withAuth)
	}
}

// rawGit runs the system git directly, outside gitx: only to prepare what
// gitx cannot (a signed commit) and to prove the hostile configuration does
// run commands when nothing neutralizes it.
func rawGit(t *testing.T, dir string, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "LC_ALL=C")
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// A repository whose own configuration (written by whoever can write its
// config, hooks or attributes) asks git to run commands: none of them runs
// when the engine works on it through gitx.
func TestRealHostileRepoConfigRunsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := newReal(gitx.Options{})
	root := t.TempDir()
	slash := filepath.ToSlash
	marker := filepath.Join(root, "ran.txt")
	writeScript := func(name, body string) string {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho \"$0 $*\" >> '"+slash(marker)+"'\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
		return slash(p)
	}
	filter := writeScript("filter.sh", "cat\n") // passes the content through
	cmd := writeScript("cmd.sh", "exit 1\n")
	hooks := filepath.Join(root, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"pre-commit", "post-commit", "post-checkout", "post-merge", "reference-transaction", "pre-auto-gc"} {
		if err := os.WriteFile(filepath.Join(hooks, h), []byte("#!/bin/sh\necho hook >> '"+slash(marker)+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	other := initRepo(t, g)
	writeFile(t, other, "o.txt", "o\n")
	commitAll(t, other, "chore: other")

	r := initRepo(t, g)
	writeFile(t, r, ".gitattributes", "* filter=evil\n")
	writeFile(t, r, "a.txt", "a\n")
	base := commitAll(t, r, "chore: base")

	// A signed commit on top of main, for log.showSignature and
	// merge.verifySignatures.
	tree, err := rawGit(t, r.Dir(), "", "rev-parse", "HEAD^{tree}")
	if err != nil {
		t.Fatal(tree, err)
	}
	signed := "tree " + tree + "\nparent " + string(base) + "\n" +
		"author A <a@example.invalid> 1767225600 +0000\ncommitter A <a@example.invalid> 1767225600 +0000\n" +
		"gpgsig -----BEGIN PGP SIGNATURE-----\n \n iQEzBAABCAAdFiEE\n -----END PGP SIGNATURE-----\n\nsigned\n"
	sh, err := rawGit(t, r.Dir(), signed, "hash-object", "-t", "commit", "-w", "--stdin")
	if err != nil {
		t.Fatal(sh, err)
	}
	if err := r.UpdateRef(ctx, "refs/heads/signed", gitx.Hash(sh)); err != nil {
		t.Fatal(err)
	}

	hostile := fmt.Sprintf(`
[core]
	hooksPath = %[3]s
	fsmonitor = %[2]s
	sshCommand = %[2]s
	askPass = %[2]s
[filter "evil"]
	clean = %[1]s clean
	smudge = %[1]s smudge
	process = %[1]s process
	required = true
[log]
	showSignature = true
[gpg]
	program = %[2]s
[merge]
	verifySignatures = true
[diff]
	external = %[2]s
[protocol "ext"]
	allow = always
[submodule]
	recurse = true
[remote "origin"]
	url = %[4]s
	fetch = +refs/heads/*:refs/remotes/origin/*
	uploadpack = %[2]s
`, filter, cmd, slash(hooks), slash(other.Dir()))
	cfg, err := os.OpenFile(filepath.Join(r.Dir(), ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.WriteString(hostile); err != nil {
		t.Fatal(err)
	}
	cfg.Close()

	// Control: the same repository, with git run directly, does run them.
	writeFile(t, r, "control.txt", "c\n")
	_, _ = rawGit(t, r.Dir(), "", "add", "control.txt")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the hostile configuration ran nothing with plain git (%v); the test proves nothing", err)
	}
	_, _ = rawGit(t, r.Dir(), "", "reset", "-q", "control.txt")
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	// Through gitx: every operation that could run them.
	writeFile(t, r, "b.txt", "b\n")
	if _, err := r.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Commit(ctx, gitx.CommitOptions{Message: "feat: b", Author: author}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Log(ctx, "signed"); err != nil {
		t.Fatal(err)
	}
	wt, err := r.AddWorktree(ctx, filepath.Join(root, "mesa"), gitx.WorktreeOptions{Branch: "mesa", Create: true, StartPoint: string(base)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.MergeFFOnly(ctx, "signed"); err != nil {
		t.Fatal(err)
	}
	if err := r.Fetch(ctx, "origin"); err != nil {
		t.Fatal(err)
	}
	if err := r.Fetch(ctx, "ext::"+cmd+" %S x", "refs/heads/main:refs/x"); err == nil {
		t.Error("fetch over ext:: succeeded")
	}
	if err := r.Fetch(ctx, "ssh://127.0.0.1:1/o/r.git", "refs/heads/main:refs/x"); err == nil {
		t.Error("fetch over ssh to a closed port succeeded")
	}
	if err := r.RemoveWorktree(ctx, wt.Dir(), true); err != nil {
		t.Fatal(err)
	}

	if b, err := os.ReadFile(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the repository configuration ran commands as the engine:\n%s", b)
	}
}
