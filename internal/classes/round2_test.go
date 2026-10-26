package classes_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/providers"
)

// Regression tests of the second round of the M3 audit
// (docs/classes/classificacao-de-risco.md, formato-das-permissoes.md,
// permissoes.md). They turn the verifier's probes into permanent tests.

// wantRisk checks the risk the engine gives a shell command.
func wantRisk(t *testing.T, env classes.Env, r *classes.Rules, cmd string, want classes.Risk) {
	t.Helper()
	if got := shellRisk(env, r, cmd); got.Risk != want {
		t.Errorf("risk %q = %s (%s); want %s", cmd, got.Risk, got.Rule, want)
	}
}

// N-D: a quoted brace alternative keeps an empty word, so {"git",} push and
// {'git',} push origin main expand to git push and are a fixed deny.
func TestRound2QuotedBraceKeepsEmptyWord(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	env.OS = "linux"
	free := rules(t, anyYAML)
	for _, cmd := range []string{`{"git",} push`, `{'git',} push origin main`, `{"g"it,} push`, `{$'git',} push`} {
		want(t, decide(env, free, m.shell(cmd)), classes.Deny, classes.LayerFixed, "git push")
	}
	want(t, decide(env, free, m.shell(`{"rm",} -rf .git`)), classes.Deny, classes.LayerFixed, ".git")
	// An empty word that is not quoted is still dropped, as in bash.
	def := rules(t, "version: 1\n")
	for _, cmd := range []string{"g{,}it log", "echo a{,}b"} {
		if d := decide(env, def, m.shell(cmd)); d.Verdict == classes.Deny {
			t.Errorf("%q wrongly denied: %q", cmd, d.Reason)
		}
	}
}

// The brace scan is linear in the word and bounded: a word with many braces
// is denied fast, never quadratic.
func TestRound2BraceScanIsBounded(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	env.OS = "linux"
	free := rules(t, anyYAML)
	for _, n := range []int{10000, 100000} {
		for _, cmd := range []string{
			"echo " + strings.Repeat("{", n),
			"echo " + strings.Repeat("{,", n/2),
			"echo " + strings.Repeat("{a,", n/3) + strings.Repeat("}", n/3),
			"echo " + strings.Repeat("{a..a}", n/6),
		} {
			start := time.Now() //nolint:forbidigo // the point is to measure the scan is not quadratic
			d := decide(env, free, m.shell(cmd))
			if el := time.Since(start); el > 2*time.Second { //nolint:forbidigo // same
				t.Errorf("brace scan of %d took %v (quadratic?)", n, el)
			}
			if d.Verdict != classes.Deny {
				t.Errorf("a brace word of %d was not denied: %s", n, d.Verdict)
			}
		}
	}
}

// F3: busybox name variants are unwrapped like busybox.
func TestRound2BusyboxNameVariants(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	env.OS = "linux"
	free := rules(t, anyYAML)
	for _, cmd := range []string{
		"busybox.static rm -rf .git", "busybox-static rm -rf .git", "busybox_1_36 rm -rf .git",
		"busybox1 rm -rf .git", "toybox-x rm -rf .git", "toybox.static rm -rf .git",
	} {
		want(t, decide(env, free, m.shell(cmd)), classes.Deny, classes.LayerFixed, ".git")
	}
	for _, cmd := range []string{"busybox_1_36 git push", "busybox.static sh -c 'git push'"} {
		want(t, decide(env, free, m.shell(cmd)), classes.Deny, classes.LayerFixed, "git push")
	}
	// A program that only starts with "box" is not a multi-call binary.
	if d := decide(env, free, m.shell("box ls")); d.Verdict == classes.Deny {
		t.Errorf("box ls wrongly denied: %q", d.Reason)
	}
}

// F4: git writers hidden behind global options, and the extra extractors,
// are medio (they ask); the class and provider configuration paths are a
// fixed write denial, except skills the project owns.
func TestRound2HiddenWritersAreMedio(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	env.OS = "linux"
	def := rules(t, "version: 1\n")
	for _, cmd := range []string{
		"git -C . apply x.diff", "git -c core.x=y apply x.diff", "git --no-pager am x", "git -C . am x",
		"unar a.rar", "quilt push", "dpkg -x a.deb out", "rpm2cpio x.rpm",
	} {
		wantRisk(t, env, def, cmd, classes.RiskMedium)
	}
}

func TestRound2ConfigPathsFixedDeny(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	env.OS = "linux"
	free := rules(t, anyYAML)
	for _, cmd := range []string{
		"touch .mcp.json", "echo x > .gemini/settings.json", "cp a .claude/commands/x.md",
		"echo x > .agents/skills/mestre-comunicacao/SKILL.md", "echo x > .claude/skills/mestre-comunicacao/SKILL.md",
		"sed -i s/a/b/ .mcp.json", "cp a .gemini/config.yaml",
	} {
		want(t, decide(env, free, m.shell(cmd)), classes.Deny, classes.LayerFixed)
	}
	// A skill the project owns (not mestre-*) is not protected.
	if d := decide(env, free, m.shell("echo x > .claude/skills/outra/SKILL.md")); d.Verdict == classes.Deny && d.Layer == classes.LayerFixed {
		t.Errorf("a project-owned skill was fixed-denied: %q", d.Reason)
	}
	// The Write tool on the same paths is denied too.
	for _, p := range []string{".mcp.json", ".gemini/settings.json", ".claude/commands/x.md", ".agents/skills/mestre-x/SKILL.md"} {
		d := decide(env, free, m.file(providers.KindWrite, m.in(p)))
		want(t, d, classes.Deny, classes.LayerFixed)
	}
}

// F5: a script changed at the table is medio whatever the launcher (options
// skipped, versioned names normalized, runners unwrapped).
func TestRound2InterpreterLaunderingIsMedio(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	env.OS = "linux"
	env.Git = fakeGit{branch: "epico/42-login", changed: []string{"x.sh", "x.py", "x.js", "x.rb", "x.ps1"}}
	def := rules(t, "version: 1\n")
	for _, cmd := range []string{
		"bash x.sh", "bash -x x.sh", "sh -e ./x.sh", "bash -- x.sh", "bash -o pipefail x.sh",
		"python3 x.py", "python3.11 x.py", "pypy3 x.py", "python -u x.py", "node --inspect x.js", "node -r ./dep.js x.js",
		"pwsh -File x.ps1", "strace ./x.sh", "ltrace ./x.sh", "uv run x.py", "uv run python x.py",
		"poetry run python x.py", "pipenv run python x.py", "deno run x.js", "bun run x.js",
		"ruby x.rb", "perl -I. x.rb", "env bash x.sh", "nice ./x.sh",
	} {
		wantRisk(t, env, def, cmd, classes.RiskMedium)
	}
	// An unchanged script of the repository stays baixo through the same
	// launchers (nothing changed here).
	clean := m.env()
	clean.OS = "linux"
	clean.Git = fakeGit{branch: "epico/42-login"}
	for _, cmd := range []string{"bash -x x.sh", "python3.11 x.py", "pwsh -File x.ps1", "uv run python x.py", "deno run x.js"} {
		wantRisk(t, clean, def, cmd, classes.RiskLow)
	}
}

// A program created earlier in the same line is unanalysable for the rest
// of the line; a link or copy of a program is analysed as that program.
func TestRound2ProgramMadeInTheLine(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	env.OS = "linux"
	free := rules(t, anyYAML)
	for _, cmd := range []string{
		"ln -s /usr/bin/git g && ./g push", "cp /usr/bin/git gg && ./gg push",
		"ln -s /usr/bin/busybox b && ./b rm -rf .git",
	} {
		want(t, decide(env, free, m.shell(cmd)), classes.Deny, classes.LayerFixed)
	}
	for _, cmd := range []string{"echo x > run.sh && ./run.sh", "chmod +x x.sh && ./x.sh"} {
		d := decide(env, free, m.shell(cmd))
		if !strings.Contains(d.Reason, "programa criado antes na mesma linha") {
			t.Errorf("%q reason = %q; want 'programa criado antes na mesma linha'", cmd, d.Reason)
		}
		wantRisk(t, env, free, cmd, classes.RiskMedium)
	}
}

// N-B: a write through a dangling symlink to a protected target is a fixed
// deny, both by shell command creating the link and by the Write tool.
func TestRound2DanglingSymlink(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, "wt")
	for _, dir := range []string{filepath.Join(wt, "src"), filepath.Join(wt, ".git", "hooks")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The system of this machine: the real normalizer folds case on Windows,
	// and so must the decision.
	env := classes.Env{AgentID: agentID, Worktree: wt, Exceptions: newLedger(), Normalize: providers.Options{}.NormalizePath}
	dev := rules(t, "version: 1\nfiles: {read: [\"**\"], write: [\"src/**\"]}\n")
	m := machine{wt: wt}
	// Creating a link whose target is protected is a fixed deny.
	want(t, decide(env, dev, m.shell("ln -s ../.git/hooks/pre-commit src/e")), classes.Deny, classes.LayerFixed, ".git")
	// A dangling link already on disk: a write through it (shell or tool)
	// lands on the target, a fixed deny.
	if err := os.Symlink("../.git/hooks/pre-commit", filepath.Join(wt, "src", "d")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	for _, cmd := range []string{"echo x > src/d", "cp /dev/null src/d", "touch src/d", "tee src/d"} {
		want(t, decide(env, dev, m.shell(cmd)), classes.Deny, classes.LayerFixed, ".git")
	}
	n := providers.Options{}.NormalizePath("src/d", wt)
	d := decide(env, dev, event(&providers.Tool{NativeName: "Write", Kind: providers.KindWrite, Paths: []string{n}}, wt))
	want(t, d, classes.Deny, classes.LayerFixed, ".git")
}
