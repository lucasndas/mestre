package classes_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

// CA-PERM-29
func TestReadOnlyCommandsLoseTheirFreedomWhenAnOptionRunsCodeOrNamesAFile(t *testing.T) {
	t.Parallel()
	closed := rules(t, "version: 1\ncommands: {default: deny}\n")
	open := rules(t, "version: 1\n")
	// ripgrep runs a program of its own: --pre runs CMD file for every file it
	// searches, --hostname-bin runs a program, and RIPGREP_CONFIG_PATH names a
	// file of arguments, --pre among them. None of them is only reading.
	runs := []string{
		"rg --pre=sh foo", "rg --pre sh foo x.sh", "rg --pre=./filter.sh foo", "rg --hostname-bin=sh foo", "rg --hostname-bin sh foo",
		"rg -n --pre=sh foo src", "RIPGREP_CONFIG_PATH=x.conf rg foo", "env RIPGREP_CONFIG_PATH=x.conf rg foo",
		"env -i RIPGREP_CONFIG_PATH=x.conf rg foo", "export RIPGREP_CONFIG_PATH=x.conf; rg foo", "ripgrep_config_path=x.conf rg foo",
	}
	for _, ee := range evasionEnvs() {
		name, env := ee.name, ee.env
		cred := "'" + filepath.Join(ee.accounts, "p", ".credentials.json") + "'"
		policy := "'" + filepath.Join(ee.agents, agentID, "policy.json") + "'"
		// The file an option names is read like a path: the folders the fixed
		// rules close are closed to it, in the forms with an equal sign, with a
		// space, attached, and as the last letter of a cluster. And the options
		// the command has without a value do not hide the file after them.
		reads := []string{
			"wc --files0-from='" + strings.Trim(cred, "'") + "'", "wc --files0-from " + cred, "grep --file=" + policy + " x", "grep -f " + policy + " x",
			"grep -f" + policy + " x", "grep -rf " + policy + " .", "grep -r --exclude-from=" + cred + " x .", "grep --exclude-from " + cred + " -r x .",
			"rg --file=" + cred + " .", "rg --ignore-file=" + cred + " foo", "rg --ignore-file " + cred + " foo", "rg -f " + policy + " .",
			"wc --files0-from='" + ee.vault + "'",
			"cat " + cred, "cat -n " + cred, "cat -A " + cred, "cat -e " + cred, "cat -t " + cred, "cat -s " + cred, "wc -c " + cred, "wc -m " + cred,
			"grep x " + cred, "grep -n x " + cred, "grep -E x " + cred, "grep -c x " + cred, "grep -s x " + cred, "grep -I x " + cred, "grep -T x " + cred,
			"grep -i -e x " + cred, "rg x " + cred, "rg -n x " + cred, "rg -c x " + cred, "rg -s x " + cred, "rg -I x " + cred,
			"ls " + cred, "ls -A " + cred, "ls -B " + cred, "ls -C " + cred, "ls -g " + cred, "ls -t " + cred, "ls --color " + cred, "ls -I x " + cred,
			"head -n 1 " + cred, "head -q " + cred, "tail -f " + cred, "tail -n 1 " + cred, "tail -s 1 " + cred,
			"rg --files " + cred, "rg --files -g '*.json' " + cred,
		}
		for _, c := range runs {
			if d := judge(env, closed, c); d.Verdict == classes.Allow {
				t.Errorf("%s: %q = allow under commands.default deny; want it out of the read-only commands", name, c)
			}
			if d := judge(env, open, c); d.Verdict == classes.Allow {
				t.Errorf("%s: %q = allow under the default class; want a request", name, c)
			}
		}
		// The code a ripgrep option runs is the agent's, outside the dispatcher.
		want(t, judge(env, closed, "rg --pre=sh foo"), classes.Deny, classes.LayerClass)
		want(t, judge(env, closed, "rg --hostname-bin sh foo"), classes.Deny, classes.LayerClass)
		d := judge(env, open, "rg --pre=sh foo")
		want(t, d, classes.Ask, classes.LayerClass)
		if d.Risk.Risk != classes.RiskMedium {
			t.Errorf("%s: rg --pre=sh foo has risk %+v; want medio", name, d.Risk)
		}
		for _, c := range reads {
			if d := judge(env, closed, c); d.Verdict != classes.Deny || d.Layer != classes.LayerFixed {
				t.Errorf("%s: %q = %s by %s (%q); want a fixed denial", name, c, d.Verdict, d.Layer, d.Reason)
			}
		}
		// A file outside the worktree is read like any other path.
		for _, c := range []string{"wc --files0-from=/etc/passwd", "grep --file=/etc/passwd x", "rg --ignore-file=/etc/passwd foo", "grep -f/etc/passwd x", "rg --ignore-file /etc/passwd foo", "cat -n /etc/passwd"} {
			want(t, judge(env, closed, c), classes.Deny, classes.LayerClass, "fora do worktree")
		}
		// What they were: the pattern and the glob are not paths, and a file of
		// the worktree is read.
		allowances(t, name, env, closed,
			"rg foo", "rg -n foo src", "rg --glob=!vendor/** foo", "rg --glob '!vendor/**' foo", "rg --max-count=5 foo", "rg --regexp=../../etc foo",
			"rg -e ../../etc foo", "wc -l a.txt", "wc -c a.txt", "wc --files0-from=list.txt", "grep --file=pats.txt a.txt",
			"grep -rn --exclude-from=ignore.txt x .", "grep -f pats.txt a.txt", "rg --ignore-file=.ignore foo", "rg --ignore-file .ignore foo",
			"grep --exclude-dir=.git -r x .", "grep -m 5 x a.txt", "grep -n x a.txt", "grep -A 2 x a.txt", "cat a.txt | wc -l", "ls --ignore=*.o",
			"ls -A src", "ls -I '*.o' src", "cat -n a.txt", "head -n 5 a.txt", "tail -n 5 a.txt", "head -c 5 a.txt", "tail -s 1 -f a.txt")
		allowances(t, name, env, open, "rg foo", "rg -n --glob=!vendor/** foo src", "RUST_LOG=debug rg foo")
	}
}

// CA-PERM-30
func TestTheGlobalConfigOfGitCannotBeMovedOnTheLine(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	moved := []evasion{
		// The prefix of git itself was denied already.
		{"HOME=/tmp git status", "HOME"}, {"XDG_CONFIG_HOME=/tmp git status", "XDG_CONFIG_HOME"}, {"GIT_CONFIG_GLOBAL=/tmp/g git status", "GIT_CONFIG_GLOBAL"},
		// env, in any order of its options
		{"env HOME=/tmp git status", "HOME"}, {"env -i HOME=/tmp git status", "HOME"}, {"env XDG_CONFIG_HOME=/tmp git status", "XDG_CONFIG_HOME"},
		{"env -u GIT_CONFIG_GLOBAL git status", "GIT_CONFIG_GLOBAL"}, {"env -u GIT_CONFIG_GLOBAL HOME=/tmp git status", "GIT_CONFIG_GLOBAL"},
		{"env --unset=GIT_CONFIG_GLOBAL git status", "GIT_CONFIG_GLOBAL"}, {"env --unset GIT_CONFIG_GLOBAL git status", "GIT_CONFIG_GLOBAL"},
		{"env -u HOME git status", "HOME"}, {"env -i -u X HOME=/tmp git status", "HOME"}, {"env FOO=1 HOME=/tmp git status", "HOME"},
		{"env -S 'HOME=/tmp git status'", "HOME"}, {"env -S 'FOO=1 git push'", "git push"},
		// the options of env in a cluster, with their values: the command is where env finds it
		{"env -iu FOO git push", "git push"}, {"env -a X git push", "git push"}, {"env -iC . git push", "git push"},
		{"env -iS 'git push'", "git push"}, {"env --split-string='git push'", "git push"}, {"env -C . HOME=/tmp git status", "HOME"},
		// a wrapper between the assignment and git, a shell that runs it
		{"HOME=/tmp nohup git status", "HOME"}, {"HOME=/tmp command git status", "HOME"}, {"HOME=/tmp time git status", "HOME"},
		{"HOME=/tmp bash -c 'git status'", "HOME"}, {"env HOME=/tmp sh -c 'git status'", "HOME"}, {"HOME=/tmp xargs git status", "HOME"},
		{"HOME=/tmp env git status", "HOME"},
		// a variable set for the rest of the line
		{"export HOME=/tmp; git status", "HOME"}, {"export HOME=/tmp && git status", "HOME"}, {"declare HOME=/tmp; git status", "HOME"},
		{"typeset XDG_CONFIG_HOME=/tmp; git status", "XDG_CONFIG_HOME"}, {"export XDG_CONFIG_HOME=/tmp\ngit status", "XDG_CONFIG_HOME"},
		{"HOME=/tmp; git status", "HOME"}, {"HOME=/tmp\ngit status", "HOME"}, {"export HOME=/tmp; bash -c 'git status'", "HOME"},
		{"export HOME=/tmp; echo $(git status)", "HOME"}, {"export HOME=/tmp; (git status)", "HOME"}, {"export HOME=/tmp; cd src && git status", "HOME"},
		{"export HOME=/tmp; /usr/bin/git status", "HOME"}, {"export HOME=/tmp; git-status", "HOME"}, {"unset HOME; git status", "HOME"},
		{"export -n HOME; git status", "HOME"}, {"export FOO=1 HOME=/tmp; git status", "HOME"},
		// the variable the engine sets for the character is not unset or moved
		{"unset GIT_CONFIG_GLOBAL; git status", "GIT_CONFIG_GLOBAL"}, {"unset GIT_CONFIG_GLOBAL", "GIT_CONFIG_GLOBAL"},
		{"unset -v GIT_CONFIG_GLOBAL", "GIT_CONFIG_GLOBAL"}, {"export -n GIT_CONFIG_GLOBAL; git status", "GIT_CONFIG_GLOBAL"},
		{"export -n GIT_CONFIG_GLOBAL", "GIT_CONFIG_GLOBAL"}, {"declare +x GIT_CONFIG_GLOBAL; git status", "GIT_CONFIG_GLOBAL"},
		{"unset GIT_CONFIG_SYSTEM GIT_CONFIG_NOSYSTEM", "GIT_CONFIG"}, {"export GIT_CONFIG_GLOBAL=/tmp/g", "GIT_CONFIG_GLOBAL"},
	}
	// The same assignments around a program that is not git, and git under
	// variables that do not move its configuration, are what they were.
	free := []string{
		"export HOME=/tmp && npm test", "HOME=/tmp npm test", "env HOME=/tmp npm test", "export HOME=/tmp", "HOME=/tmp; npm test",
		"env FOO=1 git status", "env -u FOO git status", "env -i FOO=1 git status", "unset FOO; git status", "export FOO=1; git status",
		"export -n FOO; git status", "FOO=1 git status", "git status", "HOME=/tmp true; git status",
		"export PATH_NOTE=home; git status", "echo HOME=/tmp; git status",
	}
	for _, ee := range evasionEnvs() {
		name, env := ee.name, ee.env
		fixedDenials(t, name, env, r, moved)
		allowances(t, name, env, r, free...)
	}
}

// CA-PERM-30: the variables and keys that choose a program or a file of
// configuration make a git line at least a request, as the others do.
func TestVariablesAndKeysThatRunAProgramMakeAGitLineARequest(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	for _, ee := range evasionEnvs() {
		name, env := ee.name, ee.env
		neverAllowed(t, name, env, r,
			"GIT_SEQUENCE_EDITOR=true git rebase -i HEAD~2", "GIT_PROXY_COMMAND=x git status", "GIT_TEMPLATE_DIR=/x git status",
			"env GIT_SEQUENCE_EDITOR=x git status", "export GIT_SEQUENCE_EDITOR=x; git status", "env GIT_PROXY_COMMAND=x git status",
			"git -c include.path=/tmp/x.conf status", "git -c includeIf.onbranch:main.path=/tmp/x.conf status",
			"git -c includeif.gitdir:/x/.path=/tmp/x.conf status", "git --config-env=include.path=INC status",
			"git -c core.pager=cat log")
		allowances(t, name, env, r, "git status", "FOO=1 git status", "git -C tests status")
	}
}

// CA-PERM-31
func TestControlBytesAndNULOnTheLineAreDeniedWhole(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	denied := []evasion{
		{"git\x00 push", "controle"}, {"git push\a\x01", "controle"}, {"git push\x00", "controle"}, {"\x00git push", "controle"},
		{"git\x0bpush", "controle"}, {"git\x0cpush", "controle"}, {"git status\x1b[0m", "controle"}, {"git\x7f push", "controle"},
		{"echo hi\x00", "controle"}, {"echo \"a\x00b\"", "controle"}, {"cat <<'EOF'\nok\x01\nEOF", "controle"}, {"bash -c 'git\x00 push'", "controle"},
		{"echo $(git\x00 status)", "controle"}, {"ls\x08 -la", "controle"}, {"git\x1fpush", "controle"},
		{"Start-Process git -ArgumentList 'push\x00'", "controle"}, {"git status\n\x00", "controle"},
	}
	for _, ee := range evasionEnvs() {
		name, env := ee.name, ee.env
		fixedDenials(t, name, env, r, denied)
		// Tabs, line breaks and carriage returns are what a line is made of.
		allowances(t, name, env, r, "git\tstatus", "git status\n", "echo a\r\necho b", "cat <<'EOF' > f.txt\n\tindented\nEOF", "echo 'é ã ç — “x”'", "git commit -m 'feat — x' --dry-run")
	}
}

// CA-PERM-31: the text of an -EncodedCommand is a line too, and a NUL in what
// it decodes to is a NUL on the line.
func TestAnEncodedCommandDecodedToControlBytesIsDenied(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	const (
		nul   = "bABzAAAAIAAtAGwAYQA="     // "ls\x00 -la" in UTF-16LE
		clean = "ZwBpAHQAIABwAHUAcwBoAA==" // "git push"
	)
	for _, ee := range evasionEnvs() {
		name, env := ee.name, ee.env
		fixedDenials(t, name, env, r, []evasion{{"pwsh -EncodedCommand " + nul, "controle"}, {"pwsh -EncodedCommand " + clean, "git push"}})
	}
}

// CA-PERM-30: in PowerShell the environment is $env: and the Env: drive.
func TestThePowerShellEnvironmentMovesTheGlobalConfigOfGitToo(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	win := evasionEnvs()[1]
	fixedDenials(t, win.name, win.env, r, []evasion{
		{"$env:HOME = 'C:\\x'; git status", "HOME"}, {"$env:HOME='C:\\x'; git status", "HOME"}, {"$env:XDG_CONFIG_HOME = 'C:\\x'\ngit status", "XDG_CONFIG_HOME"},
		{"$env:USERPROFILE = 'C:\\x'; git status", "USERPROFILE"}, {"Set-Item Env:HOME 'C:\\x'; git status", "HOME"}, {"Set-Item -Path Env:HOME -Value 'C:\\x'; git status", "HOME"},
		{"$env:GIT_CONFIG_GLOBAL = 'C:\\x'", "GIT_CONFIG_GLOBAL"}, {"$env:GIT_DIR = 'C:\\x'", "GIT_DIR"}, {"Remove-Item Env:GIT_CONFIG_GLOBAL", "GIT_CONFIG_GLOBAL"},
		{"Remove-Item Env:HOME; git status", "HOME"}, {"ri Env:GIT_CONFIG_GLOBAL; git status", "GIT_CONFIG_GLOBAL"},
	})
	allowances(t, win.name, win.env, r, "Set-Item Env:FOO x; git status", "Remove-Item Env:FOO", "Get-ChildItem Env:")
}
