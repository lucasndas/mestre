package classes_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
)

// The programs that run another one and the wrapper table did not know yet
// (docs/classes/permissoes.md, fixed rules): what they run is a command like
// any other for the fixed rules, so git push, the CLI of an agent, an
// elevation command and a write of .git/** or .mestre/** deny whatever starts
// them.

// CA-PERM-32
func TestProgramsThatRunAnotherAreNotAWayAroundTheFixedRules(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	// Sandboxes and limiters: their options take zero, one, two or three words
	// (bwrap --bind SRC DEST, --overlay RW WORK DEST), and the command is what
	// comes after them.
	contained := []evasion{
		{"bwrap --bind . . git push", "git push"}, {"bwrap --bind / / git push", "git push"}, {"bwrap --ro-bind /usr /usr --dev /dev --proc /proc git push", "git push"},
		{"bwrap --unshare-all --share-net --die-with-parent git push", "git push"}, {"bwrap --setenv A B git push", "git push"},
		{"bwrap --unsetenv X --chdir . git push", "git push"}, {"bwrap --uid 1000 --gid 1000 --hostname h git push", "git push"},
		{"bwrap --file 3 etc/x --bind-data 4 y git push", "git push"}, {"bwrap --overlay-src a --overlay b c d git push", "git push"},
		{"bwrap --perms 0755 --size 1024 --tmpfs t git push", "git push"}, {"bwrap --cap-add ALL --cap-drop ALL git push", "git push"},
		{"bwrap --symlink usr/bin bin git push", "git push"}, {"bwrap --chmod 0755 x git push", "git push"}, {"bwrap -- git push", "git push"},
		{"bwrap --bind . . claude", "claude"}, {"bwrap --bind . . codex exec x", "codex"}, {"bwrap --bind . . npx @openai/codex", "agente"},
		{"bwrap --bind . . sudo id", "privilégio"}, {"bwrap --bind . . cp x .git/hooks/pre-commit", ".git"}, {"bwrap --bind . . cp x .mestre/classes/qa/CLASS.md", ".mestre"},
		{"runcon -t user_t git push", "git push"}, {"runcon -u system_u -r system_r -t unconfined_t -l s0 git push", "git push"}, {"runcon -ct user_t git push", "git push"},
		{"runcon -c git push", "git push"}, {"runcon --type=user_t git push", "git push"}, {"runcon --type user_t git push", "git push"},
		{"runcon system_u:system_r:unconfined_t:s0 git push", "git push"}, {"runcon -- system_u:system_r:unconfined_t:s0 git push", "git push"},
		{"runcon ctx sudo id", "privilégio"}, {"runcon -t user_t claude", "claude"}, {"runcon -t user_t cp x .git/hooks/pre-commit", ".git"},
		{"cpulimit -l 50 git push", "git push"}, {"cpulimit --limit=50 git push", "git push"}, {"cpulimit --limit 50 git push", "git push"},
		{"cpulimit -l 50 -- git push", "git push"}, {"cpulimit -l50 -i -z git push", "git push"}, {"cpulimit -zl 50 git push", "git push"},
		{"cpulimit -l 50 sudo id", "privilégio"}, {"cpulimit -l 50 claude", "claude"}, {"cpulimit -l 50 cp x .git/hooks/pre-commit", ".git"},
		// the short options of the wrappers that were in the table, clustered or
		// with the value attached, as their programs read them
		{"nsenter -mt 1 git push", "git push"}, {"unshare -rS 0 git push", "git push"}, {"systemd-run -uname -pCPUQuota=50% git push", "git push"},
		{"prlimit -p1 git push", "git push"}, {"rlwrap -pYellow git push", "git push"},
	}
	// Debuggers, profilers and meters: gdb after --args, lldb after --, perf
	// and rr after their subcommand and options, hyperfine in every string.
	measured := []evasion{
		{"gdb --args git push", "git push"}, {"gdb -q --args git push", "git push"}, {"gdb -batch -ex run --args git push", "git push"},
		{"gdb -ex 'set pagination off' -ex run --args git push", "git push"}, {"gdb -args git push", "git push"}, {"gdb --arg git push", "git push"},
		{"gdb --args claude", "claude"}, {"gdb --args sudo id", "privilégio"}, {"gdb --args cp x .git/hooks/pre-commit", ".git"},
		{"gdb -batch -ex 'python import os; os.system(\"git push\")'", "git push"}, {"gdb -batch -ex 'call (int)system(\"git push\")'", "git push"},
		{"gdb -ex 'shell git push'", "git push"},
		{"lldb -- git push", "git push"}, {"lldb -b -o run -- git push", "git push"}, {"lldb --batch --one-line run -- git push", "git push"},
		{"lldb -o run -- claude", "claude"}, {"lldb -- sudo id", "privilégio"}, {"lldb -f git -- push", "git push"}, {"lldb git push", "git push"},
		{"lldb -o 'platform shell git push'", "git push"}, {"lldb -- cp x .git/hooks/pre-commit", ".git"},
		{"perf stat git push", "git push"}, {"perf stat -- git push", "git push"}, {"perf stat -e cycles -r 3 -- git push", "git push"}, {"perf stat -a -d git push", "git push"},
		{"perf stat -e cycles,instructions git push", "git push"}, {"perf stat --cputype atom git push", "git push"},
		{"perf record git push", "git push"}, {"perf record -o x.data -F 99 -g -- git push", "git push"}, {"perf record -e cycles:u git push", "git push"},
		{"perf trace -- git push", "git push"}, {"perf trace claude", "claude"}, {"perf sched record -- git push", "git push"}, {"perf stat -- sudo id", "privilégio"},
		{"perf record -- cp x .git/hooks/pre-commit", ".git"}, {"perf stat --pre 'git push' ls", "git push"}, {"perf stat --post='git push' ls", "git push"},
		{"rr record git push", "git push"}, {"rr record -n git push", "git push"}, {"rr record --chaos git push", "git push"}, {"rr record -o trace git push", "git push"},
		{"rr record --output-trace-dir=t git push", "git push"}, {"rr record sudo id", "privilégio"}, {"rr record -- claude", "claude"},
		{"hyperfine 'git push'", "git push"}, {"hyperfine --warmup 3 'git push'", "git push"}, {"hyperfine -r 5 'git status' 'git push'", "git push"},
		{"hyperfine -p 'git push' ls", "git push"}, {"hyperfine --prepare 'git push' ls", "git push"}, {"hyperfine --setup='sudo id' ls", "privilégio"},
		{"hyperfine -c 'git push' ls", "git push"}, {"hyperfine --conclude 'git push' ls", "git push"}, {"hyperfine -N 'git push'", "git push"},
		{"hyperfine --shell=none 'git push'", "git push"}, {"hyperfine --shell bash 'git push'", "git push"}, {"hyperfine 'claude'", "claude"},
		{"hyperfine 'cp x .git/hooks/pre-commit'", ".git"},
	}
	// python -m starts the runner a module stands for: pipx run, uv tool run,
	// uv run, poetry run.
	modules := []evasion{
		{"python -m pipx run claude", "agente"}, {"python3 -m pipx run codex", "agente"}, {"python3.12 -m pipx run agy", "agente"}, {"py -m pipx run claude", "agente"},
		{"python -m uv tool run claude", "agente"}, {"python3 -m uv tool run codex", "agente"}, {"python -m uv tool run --from claude-code claude", "agente"},
		{"python -Bm pipx run claude", "agente"}, {"python -mpipx run claude", "agente"}, {"python -W ignore -m pipx run claude", "agente"},
		{"python -I -m uv tool run claude", "agente"}, {"python -m pipx --quiet run claude", "agente"}, {"py -3 -m pipx run claude", "agente"},
		{"python -m pipx run git push", "git push"}, {"python -m uv run git push", "git push"}, {"python -m uv tool run git push", "git push"},
		{"python -m poetry run git push", "git push"}, {"python -m pdm run sudo id", "privilégio"}, {"python -m hatch run claude", "claude"},
		{"python -m pipx run cp x .git/hooks/pre-commit", ".git"},
	}
	// ssh hands the words after the host to the shell of the other end, which
	// may be this machine (ssh localhost): the line is a command like any other.
	remote := []evasion{
		{"ssh host git push", "git push"}, {"ssh host 'git push'", "git push"}, {"ssh -p 22 host git push", "git push"}, {"ssh -p22 host git push", "git push"},
		{"ssh -o StrictHostKeyChecking=no user@host git push", "git push"}, {"ssh -i key host git push", "git push"}, {"ssh -l user host git push", "git push"},
		{"ssh -F cfg -J jump host git push", "git push"}, {"ssh -- host git push", "git push"}, {"ssh host -t git push", "git push"}, {"ssh -tt host git push", "git push"},
		{"ssh host 'cd x && git push'", "git push"}, {"ssh host sudo id", "privilégio"}, {"ssh -t host sudo -i", "privilégio"}, {"ssh host claude", "claude"},
		{"ssh host cp x .git/hooks/pre-commit", ".git"}, {"slogin host git push", "git push"},
		{"sshpass -p x ssh host git push", "git push"}, {"sshpass -f pw ssh host sudo id", "privilégio"}, {"sshpass -p x git push", "git push"},
	}
	// What a wrapper sets in the environment of the command it runs moves the
	// global configuration of git as env does.
	moved := []evasion{
		{"bwrap --setenv HOME /tmp git status", "HOME"}, {"bwrap --setenv XDG_CONFIG_HOME x git status", "XDG_CONFIG_HOME"},
		{"bwrap --unsetenv GIT_CONFIG_GLOBAL git status", "GIT_CONFIG_GLOBAL"}, {"bwrap --setenv GIT_CONFIG_GLOBAL /tmp/g git status", "GIT_CONFIG_GLOBAL"},
		{"systemd-run -E HOME=/tmp git status", "HOME"}, {"systemd-run --setenv=HOME=/tmp git status", "HOME"}, {"systemd-run --setenv HOME=/tmp git status", "HOME"},
		{"systemd-run --user -E XDG_CONFIG_HOME=x git status", "XDG_CONFIG_HOME"}, {"firejail --env=HOME=/tmp git status", "HOME"},
		{"dotenv -v HOME=/tmp -- git status", "HOME"}, {"dotenvx run --env HOME=/tmp -- git status", "HOME"},
	}
	for _, ee := range evasionEnvs() {
		name, env := ee.name, ee.env
		for _, operator := range []bool{false, true} {
			env.OperatorBase = operator
			label := fmt.Sprintf("%s (operator %v)", name, operator)
			fixedDenials(t, label, env, r, contained)
			fixedDenials(t, label, env, r, measured)
			fixedDenials(t, label, env, r, modules)
			fixedDenials(t, label, env, r, remote)
			fixedDenials(t, label, env, r, moved)
		}
		// What they run, when it is not a fixed rule, is for the class to decide.
		allowances(t, name, env, r,
			"nsenter -mt 1 ls", "unshare -rS 0 ls", "systemd-run -uname ls", "bwrap --bind . . ls", "bwrap --unshare-all --die-with-parent -- ls", "bwrap --setenv FOO 1 --bind . . git status", "runcon -t user_t ls", "runcon ctx ls",
			"cpulimit -l 50 ls", "cpulimit -l 50 -- git status", "cpulimit -p 123 -l 50",
			"gdb --args ls", "gdb -batch -ex run --args ls", "gdb a.out", "lldb -- ls", "lldb -b -o run -- ls", "lldb a.out",
			"perf stat ls", "perf stat -- ls", "perf record -o x.data ls", "perf record -F 99 -g -- git status", "perf report", "perf stat -e cycles -r 3 -- ls",
			"rr record ls", "rr replay", "rr record -n git status", "hyperfine 'ls'", "hyperfine --warmup 3 'git status'", "hyperfine -r 5 'ls' 'pwd'", "hyperfine -p 'ls' ls",
			"python -m pipx run black .", "python -m uv tool run ruff check .", "python -m uv run pytest", "python -m pytest", "python -m pip list", "python -m json.tool x.json",
			"python -W ignore -m pytest", "python x.py", "py -3 -m pytest",
			"ssh host ls", "ssh -p 22 host 'git status'", "ssh -N -L 8080:localhost:80 host", "ssh host", "ssh -i key user@host uname -a", "ssh host git log",
			"sshpass -p x ssh host ls", "systemd-run -E FOO=1 ls", "firejail --env=FOO=1 ls", "dotenv -v FOO=1 -- ls", "dotenvx run --env FOO=1 -- ls")
	}
}

// CA-PERM-32: a chain of the new wrappers is bounded like the others, and a
// line made to take long is never allowed by running out of time.
func TestChainsOfTheNewWrappersAreBounded(t *testing.T) {
	t.Parallel()
	free := rules(t, anyYAML)
	for _, ee := range evasionEnvs() {
		for name, cmd := range map[string]string{
			"bwrap":    strings.Repeat("bwrap --bind . . ", 200) + "git push",
			"runcon":   strings.Repeat("runcon -t t ", 200) + "git push",
			"cpulimit": strings.Repeat("cpulimit -l 50 ", 200) + "git push",
			"gdb":      strings.Repeat("gdb --args ", 200) + "git push",
			"perf":     strings.Repeat("perf stat -a ", 200) + "git push",
			"ssh":      strings.Repeat("ssh host ", 200) + "git push",
			"python":   strings.Repeat("python -m pipx run ", 200) + "git push",
			"mixed":    strings.Repeat("bwrap --bind . . perf stat -- ssh host ", 50) + "git push",
		} {
			start := time.Now() //nolint:forbidigo // the point is to measure the bound of the analysis
			d := judge(ee.env, free, cmd)
			if took := time.Since(start); took > raceFactor*time.Second { //nolint:forbidigo // same
				t.Errorf("%s: %s took %v; want well under a second", ee.name, name, took)
			}
			if d.Verdict != classes.Deny || d.Layer != classes.LayerFixed {
				t.Errorf("%s: %s = %s by %s (%q); want a fixed denial", ee.name, name, d.Verdict, d.Layer, d.Reason)
			}
		}
	}
}
