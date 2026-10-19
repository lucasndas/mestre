package classes_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

// The evasions of the shell analysis (docs/classes/permissoes.md, fixed
// rules): a command the fixed rules deny must be denied whatever program
// launches it, runs it or writes through it.

// evasionEnv is a character on one of the two readings a command line gets
// on any machine: the native one of the system running the tests, and the
// Windows one, where a line is read as POSIX and as PowerShell and the more
// restrictive holds. The paths are the folders the fixed rules close, in the
// spelling of the system.
type evasionEnv struct {
	name                    string
	env                     classes.Env
	accounts, agents, vault string
}

func evasionEnvs() []evasionEnv {
	m := newMachine()
	native := m.env()
	native.Git = fakeGit{branch: "epico/42-login"}
	win := windowsEnv()
	win.Git = fakeGit{branch: "epico/42-login"}
	back := func(p string) string { return strings.ReplaceAll(p, "/", `\`) }
	return []evasionEnv{
		{"native", native, m.accounts, m.agents, filepath.Join(m.base, "config", "cofre.key")},
		{"windows", win, back(win.Layout.Accounts), back(win.Layout.Agents), back(filepath.Join(filepath.Dir(win.Layout.Accounts), "config", "cofre.key"))},
	}
}

// judge decides a command line for a character.
func judge(env classes.Env, r *classes.Rules, cmd string) classes.Decision {
	return decide(env, r, event(newMachine().shell(cmd).Tool, env.Worktree))
}

// evasion is a command line and a piece of the reason it is denied for.
type evasion struct{ cmd, why string }

// fixedDenials reports the lines a fixed rule does not deny, or whose
// reason does not cite why.
func fixedDenials(t *testing.T, name string, env classes.Env, r *classes.Rules, cases []evasion) {
	t.Helper()
	for _, c := range cases {
		d := judge(env, r, c.cmd)
		if d.Verdict != classes.Deny || d.Layer != classes.LayerFixed || !strings.Contains(strings.ToLower(d.Reason), strings.ToLower(c.why)) {
			t.Errorf("%s: %q = %s by %s (%q); want a fixed denial citing %q", name, c.cmd, d.Verdict, d.Layer, d.Reason, c.why)
		}
	}
}

// allowances reports the lines the rules do not allow.
func allowances(t *testing.T, name string, env classes.Env, r *classes.Rules, cmds ...string) {
	t.Helper()
	for _, c := range cmds {
		if d := judge(env, r, c); d.Verdict != classes.Allow {
			t.Errorf("%s: %q = %s by %s (%q); want allow", name, c, d.Verdict, d.Layer, d.Reason)
		}
	}
}

// neverAllowed reports the lines that run on their own: the verdict must be
// ask or deny, by any layer.
func neverAllowed(t *testing.T, name string, env classes.Env, r *classes.Rules, cmds ...string) {
	t.Helper()
	for _, c := range cmds {
		if d := judge(env, r, c); d.Verdict == classes.Allow {
			t.Errorf("%s: %q = allow; want ask or deny", name, c)
		}
	}
}

// CA-PERM-27
func TestLaunchersElevationAndRunnersStartNothingTheFixedRulesDeny(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	// Start-Process, saps and start run the program they name, with the
	// -ArgumentList they carry: a fixed rule on that command line denies.
	launched := []evasion{
		{"Start-Process git -ArgumentList push", "git push"},
		{"Start-Process -FilePath git -ArgumentList push", "git push"},
		{"Start-Process -FilePath:git -ArgumentList:push", "git push"},
		{"Start-Process -File git -Arg push", "git push"},
		{"Start-Process -ArgumentList push -FilePath git", "git push"},
		{"Start-Process -Wait -NoNewWindow git -ArgumentList push", "git push"},
		{"Start-Process -WorkingDirectory . git -ArgumentList push", "git push"},
		{"Start-Process git push", "git push"},
		{"saps git -ArgumentList push", "git push"},
		{"start git push", "git push"},
		{"start /wait git push", "git push"},
		{"START git push", "git push"},
		{"Start-Process git -ArgumentList 'remote','add','o','https://x'", "remotos"},
		{"Start-Process git -ArgumentList 'remote', 'add', 'o', 'https://x'", "remotos"},
		{"Start-Process claude", "claude"},
		{"Start-Process -FilePath claude", "claude"},
		{"Start-Process codex -ArgumentList exec", "codex"},
		{"saps agy", "agy"},
		{"Start-Process doas -ArgumentList id", "privilégio"},
		{"Start-Process sudo -ArgumentList id", "privilégio"},
		{"Start-Process run0 -ArgumentList id", "privilégio"},
		{"Start-Process cmd -ArgumentList '/c','git push'", "git push"},
		{"Start-Process cmd -ArgumentList '/c', 'git push'", "git push"},
		{"Start-Process cmd -ArgumentList '/c git push'", "git push"},
		{`Start-Process cmd.exe -ArgumentList "/c","git push"`, "git push"},
		{"Start-Process powershell -ArgumentList '-Command','git push'", "git push"},
		{"Start-Process pwsh -ArgumentList '-c git push'", "git push"},
		{"Start-Process bash -ArgumentList '-c','\"git push\"'", "git push"},
		{"Start-Process bash -ArgumentList '-c \"git push\"'", "git push"},
		{"Start-Process npx -ArgumentList '@openai/codex'", "agente"},
		// what the check of -Verb RunAs denied stays denied
		{"Start-Process pwsh -Verb RunAs", "administrador"},
		{"Start-Process git -ArgumentList push -Verb RunAs", "administrador"},
	}
	// The elevation commands: sudo and doas were denied, their kin were not.
	elevated := []evasion{
		{"run0 id", "privilégio"}, {"run0 --user=root id", "privilégio"}, {"please id", "privilégio"}, {"pleaser id", "privilégio"},
		{"runuser -u root id", "privilégio"}, {"runuser -l root -c id", "privilégio"}, {"/usr/bin/run0 id", "privilégio"},
		{"env run0 id", "privilégio"}, {"nohup please id", "privilégio"}, {"sudo id", "privilégio"}, {"doas id", "privilégio"}, {"sudoedit /etc/hosts", "privilégio"},
	}
	// The CLI of an agent started by a package runner, as npx always was.
	runners := []evasion{
		{"npx @openai/codex", "agente"}, {"bunx claude", "agente"}, {"npm exec @anthropic-ai/claude-code", "agente"},
		{"bun x @anthropic-ai/claude-code", "agente"}, {"bun x claude", "agente"}, {"bun x codex exec x", "agente"},
		{"bun x @openai/codex@latest", "agente"}, {"bun x -p @openai/codex codex", "agente"}, {"bun x --package=@openai/codex codex", "agente"},
		{"bun x agy", "agente"}, {"bun x antigravity", "agente"},
		{"deno run npm:@openai/codex", "agente"}, {"deno run -A npm:@openai/codex", "agente"},
		{"deno run --allow-all npm:@anthropic-ai/claude-code", "agente"}, {"deno run npm:claude", "agente"},
		{"deno run npm:@anthropic-ai/claude-code@1.0.0 --help", "agente"}, {"deno run jsr:@x/claude", "agente"},
		{"deno run --config deno.json npm:@openai/codex", "agente"},
		{"deno x npm:@openai/codex", "agente"}, {"deno x claude", "agente"}, {"dx npm:@anthropic-ai/claude-code", "agente"},
		{"uvx claude", "agente"}, {"uvx codex", "agente"}, {"uvx claude-code", "agente"}, {"uvx claude@latest", "agente"},
		{"uvx --from claude-code claude", "agente"}, {"uvx --from=claude-code claude", "agente"}, {"uvx -p 3.12 claude", "agente"},
		{"uv tool run claude", "agente"}, {"uv tool run --from claude-code claude", "agente"},
		{"pipx run claude", "agente"}, {"pipx run codex", "agente"}, {"pipx run antigravity", "agente"},
		{"pipx run --spec claude-code claude", "agente"},
		{"npm --yes exec claude", "agente"}, {"pnpm --silent dlx @openai/codex", "agente"},
		// what a runner runs is a command: git push, an elevation, a write of .git
		{"npx git push", "git push"}, {"npx -y git push", "git push"}, {"bunx git push", "git push"}, {"bun x git push", "git push"},
		{"pnpm exec git push", "git push"}, {"yarn exec git push", "git push"}, {"npm exec git push", "git push"}, {"npm exec -- git push", "git push"},
		{"pnpm --filter x exec git push", "git push"}, {"deno x git push", "git push"}, {"uvx git push", "git push"}, {"pipx run git push", "git push"},
		{"uv tool run git push", "git push"}, {"npx sudo id", "privilégio"}, {"npx run0 id", "privilégio"}, {"npx cp x .git/hooks/pre-commit", ".git"},
		{"npx -c 'git push'", "git push"}, {"npx --call 'git push'", "git push"}, {"npx --call='git push'", "git push"},
		{"npm exec -c 'git push'", "git push"}, {"npm exec --call 'git push'", "git push"},
	}
	// Programs that run another with other privilege, namespace or environment:
	// not the safe list's business, but what they run is a command to the fixed rules.
	wrapped := []evasion{
		{"setarch x86_64 git push", "git push"}, {"linux64 git push", "git push"}, {"setpriv --reuid=1000 git push", "git push"}, {"setpriv --reuid 1000 --regid 1000 git push", "git push"},
		{"prlimit --nofile=10 git push", "git push"}, {"unshare -r git push", "git push"}, {"unshare --propagation private git push", "git push"},
		{"nsenter -t 1 -m git push", "git push"}, {"chroot / git push", "git push"}, {"chroot --userspec=u / git push", "git push"},
		{"gosu user git push", "git push"}, {"su-exec user git push", "git push"}, {"fakeroot -l lib git push", "git push"}, {"dumb-init git push", "git push"},
		{"tini -p SIGTERM -- git push", "git push"}, {"ssh-agent -t 10 git push", "git push"}, {"firejail git push", "git push"}, {"rlwrap git push", "git push"},
		{"systemd-run --user --unit x git push", "git push"}, {"systemd-run --on-active=10 git push", "git push"}, {"sg grp -c 'git push'", "git push"}, {"sg grp git push", "git push"},
		{"cross-env FOO=1 git push", "git push"}, {"cross-env HOME=/tmp git status", "HOME"}, {"env-cmd -f .env git push", "git push"}, {"dotenv -e .env -- git push", "git push"},
		{"dotenvx run -f .env -- git push", "git push"}, {"doppler run -p x -- git push", "git push"}, {"direnv exec . git push", "git push"},
		{"corepack npx claude", "agente"}, {"corepack pnpm exec git push", "git push"}, {"corepack npm exec -- git push", "git push"},
		{"doppler run --command 'git push'", "git push"}, {"doppler run --command='git push'", "git push"}, {"doppler run --command 'sudo id'", "privilégio"},
		{"doppler run --command 'npx claude'", "agente"}, {"doppler run --command \"git push\" -p proj", "git push"}, {"sg -c 'git push'", "git push"},
		{"setarch x86_64 claude", "claude"}, {"unshare sudo id", "privilégio"}, {"gosu root run0 id", "privilégio"}, {"nsenter -t 1 -m cp x .git/hooks/pre-commit", ".git"},
	}
	for _, ee := range evasionEnvs() {
		name, env := ee.name, ee.env
		for _, operator := range []bool{false, true} {
			env.OperatorBase = operator
			fixedDenials(t, fmt.Sprintf("%s (operator %v)", name, operator), env, r, launched)
			fixedDenials(t, fmt.Sprintf("%s (operator %v)", name, operator), env, r, elevated)
			fixedDenials(t, fmt.Sprintf("%s (operator %v)", name, operator), env, r, runners)
			fixedDenials(t, fmt.Sprintf("%s (operator %v)", name, operator), env, r, wrapped)
		}
		// A launcher, an elevation-free program and a runner of other
		// packages are what they were.
		allowances(t, name, env, r,
			"Start-Process notepad", "Start-Process notepad -ArgumentList a.txt", "saps notepad a.txt", "start notepad a.txt",
			"Start-Process git -ArgumentList status", "Start-Process git status", "Start-Process -FilePath git -ArgumentList 'log','--oneline'",
			"bun x prettier --check .", "bun x vitest", "bun x tsc", "deno run -A main.ts", "deno run npm:prettier", "deno x npm:prettier",
			"uvx ruff check .", "uv tool run ruff check .", "pipx run black .", "uv tool list", "uvx --from ruff ruff check .",
			"uvx -p 3.12 ruff check .", "pipx run --spec black black .", "npm exec prettier -- --check .", "pnpm dlx prettier --check .",
			"bun x prettier claude/", "npx git status", "npx --yes create-react-app app", "pnpm exec jest", "yarn exec tsc", "npm exec -- prettier --check .",
			"npx cp a.txt b.txt", "setarch x86_64 ls", "unshare -r ls", "chroot rootfs ls", "gosu user ls", "systemd-run --user ls", "cross-env NODE_ENV=test npm test",
			"dotenv -e .env -- npm test", "doppler run -- npm test", "direnv exec . make", "corepack enable", "corepack pnpm install", "sg grp ls", "fakeroot make", "doppler run --command 'npm test'", "doppler secrets")
		neverAllowed(t, name, env, r, "npx -c 'echo hi'", "npm exec -c 'echo hi'")
	}
}

// CA-PERM-28
func TestGitWritesThroughItsOwnOutputOptionsAreJudgedLikeAnyWrite(t *testing.T) {
	t.Parallel()
	free := rules(t, anyYAML)
	tests := rules(t, "version: 1\nfiles: {write: [\"tests/**\"]}\ncommands: {allow: [\"*\"], default: allow}\n")
	protected := []evasion{
		{"git archive -o .git/x.tar HEAD", ".git"},
		{"git archive -o.git/x.tar HEAD", ".git"},
		{"git archive --output .git/x.tar HEAD", ".git"},
		{"git archive --output=.git/x.tar HEAD", ".git"},
		{"git archive HEAD -o .git/x.tar", ".git"},
		{"git archive --format=zip -o .mestre/classes/qa/CLASS.md HEAD", ".mestre"},
		{"git -C . archive -o .git/x.tar HEAD", ".git"},
		{"git bundle create .git/x.bundle --all", ".git"},
		{"git bundle create .mestre/x.bundle --all", ".mestre"},
		{"git bundle create -q .mestre/x.bundle HEAD", ".mestre"},
		{"git format-patch -1 -o .git/hooks", ".git"},
		{"git format-patch -1 -o.git/hooks", ".git"},
		{"git format-patch -1 -o .mestre/p", ".mestre"},
		{"git format-patch -1 --output-directory .mestre/classes", ".mestre"},
		{"git format-patch -1 --output-directory=.mestre/classes", ".mestre"},
		{"git diff --output .git/x HEAD", ".git"},
		{"git diff --output=.git/x HEAD", ".git"},
		{"git log --output .mestre/x", ".mestre"},
		{"git show --output .mestre/x HEAD", ".mestre"},
		{"git pack-objects .git/objects/pack/x --all", ".git"},
		{"git clone ./src .mestre/y", ".mestre"},
		{"git clone --depth 1 ./src .git/hooks", ".git"},
		{"git init .mestre/y", ".mestre"},
		{"git init --separate-git-dir .git/x y", ".git"},
		{"git apply --directory=.mestre/classes x.patch", ".mestre"}, {"git apply --directory .git/hooks x.patch", ".git"},
	}
	for _, ee := range evasionEnvs() {
		name, env := ee.name, ee.env
		fixedDenials(t, name, env, free, protected)
		// Outside protected folders the path is judged like any write: the
		// class that writes only tests/ takes the output there.
		allowances(t, name, env, free,
			"git archive -o out.tar HEAD", "git archive --output out.tar HEAD", "git bundle create out.bundle --all",
			"git bundle verify out.bundle", "git bundle list-heads .git/x.bundle", "git format-patch -1 -o patches",
			"git format-patch -1 --output-directory patches", "git diff --output out.diff HEAD", "git archive HEAD", "git format-patch -1 --stdout",
			"git clone ./src copy", "git init sub", "git archive --prefix=x/ HEAD", "git apply x.patch", "git apply --directory=sub x.patch", "git apply --directory=. x.patch", "git apply -p1 --directory=./ x.patch",
			"git diff --output=/dev/null HEAD", "git diff --output /dev/null HEAD")
		allowances(t, name, env, tests, "git archive -o tests/out.tar HEAD", "git bundle create tests/o.bundle --all",
			"git format-patch -1 -o tests/patches", "git diff --output tests/out.diff HEAD")
		for _, c := range []string{"git archive -o src/out.tar HEAD", "git archive --output src/out.tar HEAD", "git bundle create src/o.bundle --all",
			"git format-patch -1 -o src/patches", "git format-patch -1 --output-directory=src/patches", "git diff --output src/out.diff HEAD"} {
			want(t, judge(env, tests, c), classes.Deny, classes.LayerClass, "files.write")
		}
	}
	// The subcommands that write into the object store the tables share, and
	// the upstream of a branch, which is a line of .git/config.
	shared := []evasion{
		{"git commit-graph write", "git commit-graph"}, {"git commit-graph verify", "git commit-graph"}, {"git multi-pack-index write", "git multi-pack-index"},
		{"git index-pack x.pack", "git index-pack"}, {"git unpack-objects", "git unpack-objects"}, {"git -C . commit-graph write", "git commit-graph"},
		{"git-commit-graph write", "git commit-graph"}, {"git-index-pack x.pack", "git index-pack"},
		{"git branch -u origin/main", "configuração"}, {"git branch --set-upstream-to=origin/main", "configuração"},
		{"git branch --set-upstream-to origin/main", "configuração"}, {"git branch --unset-upstream", "configuração"},
		{"git branch -u origin/main epico/42-login", "configuração"}, {"git branch --edit-description", "configuração"},
	}
	for _, ee := range evasionEnvs() {
		name, env := ee.name, ee.env
		fixedDenials(t, name, env, free, shared)
		allowances(t, name, env, free, "git branch", "git branch -a", "git branch -vv", "git branch --show-current", "git branch --list 'epico/*'",
			"git branch -f epico/42-login HEAD", "git log --oneline", "git status")
	}
}

// CA-PERM-28: a relative output is read from the folder the command is in, an
// absolute one is a path of the Mestre like any other.
func TestGitOutputPathsAreReadFromTheFolderOfTheCommand(t *testing.T) {
	t.Parallel()
	free := rules(t, anyYAML)
	for _, ee := range evasionEnvs() {
		// After a cd, a relative output is read from the folder it ends in.
		want(t, judge(ee.env, free, "cd .git && git archive -o x.tar HEAD"), classes.Deny, classes.LayerFixed, ".git")
		// An absolute output into the data folders of the Mestre.
		want(t, judge(ee.env, free, "git archive -o '"+filepath.Join(ee.accounts, "p", "x.tar")+"' HEAD"), classes.Deny, classes.LayerFixed, "contas")
		want(t, judge(ee.env, free, "git bundle create '"+filepath.Join(ee.agents, agentID, "policy.json")+"' --all"), classes.Deny, classes.LayerFixed)
	}
}

// CA-PERM-31: on Windows a line is also read as PowerShell reads it, which
// takes more characters for a space, a quote or a dash than a POSIX shell.
func TestPowerShellReadsTheLineWithItsOwnSpacesQuotesAndDashes(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	win := evasionEnvs()[1]
	fixedDenials(t, win.name, win.env, r, []evasion{
		// typographic quotes quote
		{"git ‘push’", "git push"}, {"git “push”", "git push"}, {"git ‚push‛", "git push"}, {"‘git’ push", "git push"}, {"& ‘git’ push", "git push"},
		{"git p“us”h", "git push"}, {"pwsh -c ‘git push’", "git push"}, {"cmd /c “git push”", "git push"},
		// spaces other than the plain one separate
		{"git\u00a0push", "git push"}, {"git\u2003push", "git push"}, {"git\u3000push", "git push"}, {"git\u2028push", "git push"}, {"git\u0085push", "git push"},
		// the en dash, the em dash and the horizontal bar start a parameter
		{"Start-Process git –ArgumentList push", "git push"}, {"Start-Process git —ArgumentList push", "git push"}, {"Start-Process git ―ArgumentList push", "git push"},
		{"Start-Process pwsh –Verb RunAs", "administrador"}, {"Start-Process pwsh -Verb ‘RunAs’", "administrador"}, {"Start-Process pwsh -Verb “RunAs”", "administrador"},
		{"pwsh –Command \"git push\"", "git push"}, {"powershell —EncodedCommand ZwBpAHQAIABwAHUAcwBoAA==", "git push"},
		// the dot operator runs a command as & does
		{". git push", "git push"}, {". claude", "claude"}, {". sudo id", "privilégio"},
	})
	// An expression in parentheses is an argument, or the name of the command
	// after the call operator, that only PowerShell resolves.
	neverAllowed(t, win.name, win.env, r, "git (echo push)", "git ('pu'+'sh')", "& git (Write-Output push)", "Start-Process git -ArgumentList (echo push)",
		"&('git') push", "& ('git') push", "&(echo git) push", ".('git') push", "& (Get-Command git) push", "&('git') remote add o https://x")
	// Invoke-Expression runs what the pipe feeds it, as a shell does, and the
	// tools that create a process take the command line in a string.
	fixedDenials(t, win.name, win.env, r, []evasion{
		{"echo 'git push' | iex", "git push"}, {"'git push' | Invoke-Expression", "git push"}, {"Write-Output 'git push' | iex", "git push"},
		{"wmic process call create \"git push\"", "git push"}, {"Invoke-WmiMethod -Class Win32_Process -Name Create -ArgumentList \"git push\"", "git push"},
		{"iwmi -Class Win32_Process -Name Create -ArgumentList 'git push'", "git push"},
		{"psexec -s git push", "privilégio"}, {"PsExec64.exe -s cmd", "privilégio"}, {"nsudo git push", "privilégio"},
	})
	allowances(t, win.name, win.env, r, "& { git status }", "echo 'hello' | Write-Output", "wmic cpu get name")
	// Quotes, spaces and dashes of that kind in text are text.
	allowances(t, win.name, win.env, r, "git status", "git commit -m “feat — x”", "git commit -m ‘feat – x’", "git log --format=“%H”",
		"Get-Content “a b.txt”", "Write-Output ‘it’’s’", "echo “a”“b”", "git commit -m 'a\u00a0b'")
}

// CA-PERM-25: the analysis of a chain of commands that run commands is bounded.
func TestChainsOfLaunchersAndWrappersAreBounded(t *testing.T) {
	t.Parallel()
	free := rules(t, anyYAML)
	for _, ee := range evasionEnvs() {
		for name, cmd := range map[string]string{
			"start":        strings.Repeat("start ", 60) + "git push",
			"saps":         strings.Repeat("saps ", 200) + "git push",
			"start /wait":  strings.Repeat("start /wait ", 60) + "git push",
			"npm x exec":   strings.Repeat("npm -a x exec ", 60) + "git push",
			"npx":          strings.Repeat("npx ", 200) + "git push",
			"setarch":      strings.Repeat("setarch x86_64 ", 200) + "git push",
			"cross-env":    strings.Repeat("cross-env A=1 ", 200) + "git push",
			"dot operator": strings.Repeat(". ", 200) + "git push",
			"corepack":     strings.Repeat("corepack npx ", 100) + "git push",
		} {
			d, _ := boundedDecision(t, ee.name+": "+name, ee.env, free, cmd)
			if name == "dot operator" && ee.name != "windows" {
				continue // . is source in a POSIX shell, and the call operator in PowerShell
			}
			if d.Verdict != classes.Deny || d.Layer != classes.LayerFixed {
				t.Errorf("%s: %s = %s by %s (%q); want a fixed denial", ee.name, name, d.Verdict, d.Layer, d.Reason)
			}
		}
	}
}

// CA-PERM-25: nor does a chain of the transparent wrappers (exec, nice, env,
// timeout…) grow the analysis as the square of its length: the text of every
// level is built, so every level is paid for, and a line of a thousand and a half of them is too
// laborious to analyse, not allowed.
func TestChainsOfTransparentWrappersAreTooLaborious(t *testing.T) {
	t.Parallel()
	free := rules(t, anyYAML)
	for _, ee := range evasionEnvs() {
		chains := map[string]string{"exec": strings.Repeat("exec ", 1500) + "git push"}
		if ee.name == "native" { // the second reading of Windows doubles the work; one chain shows it
			chains["env -i"] = strings.Repeat("env -i ", 1200) + "ls"
		}
		for name, cmd := range chains {
			d, _ := boundedDecision(t, ee.name+": "+name, ee.env, free, cmd)
			want(t, d, classes.Deny, classes.LayerFixed, "trabalhoso demais")
		}
		// A chain of a few is what it was.
		allowances(t, ee.name, ee.env, free, "exec nice env -i time ls", "timeout 5 nice -n 5 stdbuf -oL ls")
	}
}
