package classes_test

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/providers"
)

// CA-PERM-02
func TestExceptionPassesOnceAndADecreeNeverLiftsAFixedRule(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	until := now.AddDate(0, 0, 7)
	r := rules(t, `version: 1
files:
  write: ["tests/**"]
  deny: ["src/**"]
commands: {allow: ["*"], default: allow}
`,
		classes.Decree{ID: 7, Kind: classes.DecreeException, Level: classes.LevelApprover,
			Effect: classes.Effect{Kind: classes.EffectPermission},
			Action: &classes.Action{Kind: providers.KindEdit, Paths: []string{"src/a.ts"}}},
		classes.Decree{ID: 8, Level: classes.LevelOwner, Until: &until,
			Effect: classes.Effect{Kind: classes.EffectPermission, Allow: &classes.Grant{Commands: []string{"git push", "git push *"}}}},
	)
	edit := m.file(providers.KindEdit, m.in("src/a.ts"))

	want(t, decide(env, r, edit), classes.Allow, classes.LayerException, "exceção pontual 7")
	want(t, decide(env, r, edit), classes.Deny, classes.LayerClass, "src/a.ts", "files.deny")
	want(t, decide(env, r, m.shell("git push")), classes.Deny, classes.LayerFixed, "git push")
	want(t, decide(env, r, m.shell("git push origin epico/42-login")), classes.Deny, classes.LayerFixed, "git push")
}

// CA-PERM-03
func TestCompoundCommandsAndSubstitutions(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	for _, def := range []string{"ask", "allow"} {
		r := rules(t, "version: 1\ncommands:\n  allow: [\"npm test\"]\n  default: "+def+"\n")
		want(t, decide(env, r, m.shell("npm test && git push")), classes.Deny, classes.LayerFixed, "git push")
		d := decide(env, r, m.shell("npm test $(curl x)"))
		if d.Verdict == classes.Allow {
			t.Fatalf("default %s: npm test $(curl x) allowed (%q); want at least ask", def, d.Reason)
		}
		want(t, d, classes.Ask, "")
		if def == "allow" && !strings.Contains(d.Reason, "não analisa") {
			t.Fatalf("reason %q does not say the substitution is not analysed", d.Reason)
		}
		want(t, decide(env, r, m.shell("npm test")), classes.Allow, "")
	}
}

// CA-PERM-08
func TestRefsOfOtherBranchesAndAccountCredentialsAreFixedDenials(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	r := rules(t, anyYAML)
	r.Git.Branches = []string{"epico/42-login"} // the table's branch only
	want(t, decide(env, r, m.shell("git branch -f epico/7-outra HEAD")), classes.Deny, classes.LayerFixed, "epico/7-outra")
	auth := filepath.Join(m.accounts, "codex-time", ".codex", "auth.json")
	want(t, decide(env, r, m.file(providers.KindRead, auth)), classes.Deny, classes.LayerFixed, "contas/")
	want(t, decide(env, r, m.shell("cat "+auth)), classes.Deny, classes.LayerFixed, "contas/")
	// The table's own branch goes by the git block.
	want(t, decide(env, r, m.shell("git branch -f epico/42-login HEAD")), classes.Allow, "")
}

// CA-PERM-09
func TestMCPToolsByNativeNameInEveryProvider(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	r := rules(t, `version: 1
tools:
  allow: ["mcp__playwright__*"]
  deny: ["mcp__*__delete_*"]
  default: deny
`)
	same := func(p string) (string, error) { return p, nil }
	adapters := map[string]providers.Adapter{
		"claude-code": providers.NewClaudeCode(providers.Options{Resolve: same}),
		"codex":       providers.NewCodex(providers.Options{Resolve: same}),
	}
	cases := map[string]classes.Verdict{
		"mcp__playwright__browser_navigate": classes.Allow,
		"mcp__playwright__delete_cookies":   classes.Deny,
		"mcp__github__create_issue":         classes.Deny,
	}
	for prov, a := range adapters {
		for name, v := range cases {
			stdin := `{"hook_event_name":"PreToolUse","session_id":"s1","prompt_id":"p1","turn_id":"t1","cwd":"` + filepath.ToSlash(m.wt) + `","tool_name":"` + name + `","tool_input":{}}`
			ev, err := a.NormalizeHook(providers.HookInput{NativeEvent: "PreToolUse", Stdin: []byte(stdin), AgentID: agentID, ReceivedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			if ev.Tool == nil || ev.Tool.Kind != providers.KindMCP {
				t.Fatalf("%s %s: tool %+v, want kind mcp", prov, name, ev.Tool)
			}
			if d := decide(env, r, ev); d.Verdict != v {
				t.Fatalf("%s %s = %s (%q); want %s", prov, name, d.Verdict, d.Reason, v)
			}
		}
	}
}

// CA-PERM-11
func TestAttachmentsAreNeverExecuted(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	for _, class := range []string{qaYAML, anyYAML} {
		r := rules(t, class)
		for _, c := range []string{"chmod +x .mestre/anexos/x.sh", "sh .mestre/anexos/x.sh", "bash ./.mestre/anexos/x.sh", "./.mestre/anexos/x.sh", "cd .mestre/anexos && sh x.sh"} {
			want(t, decide(env, r, m.shell(c)), classes.Deny, classes.LayerFixed, "anexos")
		}
	}
	// Reading an attachment is what attachments are for.
	want(t, decide(env, rules(t, anyYAML), m.shell("cat .mestre/anexos/nota.txt")), classes.Allow, "")
}

// CA-PERM-12
func TestNativeSubagentsByNameAndTheirToolsByTheCharactersRules(t *testing.T) {
	t.Parallel()
	m := newMachine()
	denied := rules(t, "version: 1\ntools: {deny: [\"Agent\"], default: allow}\n")
	want(t, decide(m.env(), denied, toolEvent(providers.KindSubagent, "Agent")), classes.Deny, classes.LayerClass, "Agent")

	freed := rules(t, "version: 1\nfiles: {write: [\"tests/**\"]}\ntools: {allow: [\"Agent\"], default: deny}\n")
	p := &classes.Policy{
		Source: sourceOf{agentID: freed},
		Env: func(_ context.Context, id string) (classes.Env, error) {
			if id != agentID {
				return classes.Env{}, errors.New("unknown")
			}
			return m.env(), nil
		},
	}
	ctx := context.Background()
	want(t, p.Decide(ctx, toolEvent(providers.KindSubagent, "Agent")), classes.Allow, "")
	// A write from inside the subagent carries the character's MESTRE_AGENT_ID.
	want(t, p.Decide(ctx, m.file(providers.KindWrite, m.in("src/a.ts"))), classes.Deny, classes.LayerClass, "files.write")
	want(t, p.Decide(ctx, m.file(providers.KindWrite, m.in("tests/a.test.ts"))), classes.Allow, "")
	other := m.file(providers.KindWrite, m.in("tests/a.test.ts"))
	other.AgentID = "01JQOUTRO"
	want(t, p.Decide(ctx, other), classes.Deny, classes.LayerFixed)
}

type sourceOf map[string]*classes.Rules

func (s sourceOf) Rules(_ context.Context, id string) (*classes.Rules, error) {
	if r, ok := s[id]; ok {
		return r, nil
	}
	return nil, errors.New("no rules")
}

// CA-PERM-13
func TestAgentAndClassDefinitionsAndTheTmpOfOthers(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	r := rules(t, anyYAML)
	want(t, decide(env, r, m.shell("cp x.md .mestre/classes/qa/CLASS.md")), classes.Deny, classes.LayerFixed, ".mestre/classes/qa/CLASS.md")
	want(t, decide(env, r, m.shell("echo y > .claude/agents/a.md")), classes.Deny, classes.LayerFixed, ".claude/agents/a.md")
	want(t, decide(env, r, m.file(providers.KindWrite, filepath.Join(m.tmp, "sessao.json"))), classes.Allow, "")
	other := filepath.Join(m.agents, "01JQOUTRO", "tmp", "sessao.json")
	want(t, decide(env, r, m.file(providers.KindWrite, other)), classes.Deny, classes.LayerFixed, "outro personagem")
	// The own MESTRE_TMP is open to a class that writes nothing else.
	qa := rules(t, qaYAML)
	want(t, decide(env, qa, m.file(providers.KindWrite, filepath.Join(m.tmp, "x"))), classes.Allow, "")
}

// CA-PERM-16
func TestAccountProfilesAreClosedToEveryProfile(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	r := rules(t, anyYAML)
	for _, profile := range []string{"claude-pessoal", "claude-time"} {
		bin := filepath.Join(m.accounts, profile, ".local", "share", "claude", "versions", "2.1.296")
		want(t, decide(env, r, m.file(providers.KindWrite, bin)), classes.Deny, classes.LayerFixed, "contas/")
		want(t, decide(env, r, m.file(providers.KindEdit, bin)), classes.Deny, classes.LayerFixed, "contas/")
		want(t, decide(env, r, m.shell("rm -f "+bin)), classes.Deny, classes.LayerFixed, "contas/")
		want(t, decide(env, r, m.shell("echo x > "+bin)), classes.Deny, classes.LayerFixed, "contas/")
	}
}

// The decision under CA-PERM-05: a command in ask, and the exception its
// approval becomes, bound to that action. The whole criterion, with the
// request #N and the fake agent, is internal/hook's.
func TestAskThenTheApprovedActionPassesOnce(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	r := rules(t, qaYAML)
	ev := m.shell("npm install x")
	d := decide(env, r, ev)
	want(t, d, classes.Ask, classes.LayerClass, "npm install x")
	if got := classes.AskReason(4, d.Action); got != "pedido de aprovação #4 aberto para npm install x; encerre o turno, você será avisado" {
		t.Fatalf("AskReason = %q", got)
	}
	r.Decrees = append(r.Decrees, classes.Decree{ID: 4, Kind: classes.DecreeException, Level: classes.LevelGrandMaster,
		Effect: classes.Effect{Kind: classes.EffectPermission}, Action: &d.Action})
	want(t, decide(env, r, ev), classes.Allow, classes.LayerException)
	want(t, decide(env, r, ev), classes.Ask, classes.LayerClass)
	// Bound to that action exactly.
	want(t, decide(env, r, m.shell("npm install y")), classes.Ask, classes.LayerClass)
}

// The decision under CA-PERM-07: over the snapshot. The whole criterion, the
// dispatcher that falls back to it with the engine down, is internal/hook's.
func TestDegradedDecisionOverTheSnapshot(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	snap := rules(t, "version: 1\nfiles: {write: [\"**\"], deny: [\"src/**\"]}\ncommands: {ask: [\"npm install *\"], allow: [\"npm test\"], default: deny}\n")
	ctx := context.Background()
	d := classes.DecideDegraded(ctx, env, snap, m.file(providers.KindEdit, m.in("src/a.ts")))
	want(t, d, classes.Deny, classes.LayerClass, "src/a.ts")
	d = classes.DecideDegraded(ctx, env, snap, m.shell("npm install x"))
	want(t, d, classes.Deny, classes.LayerDegraded)
	if d.Reason != classes.DegradedReason {
		t.Fatalf("reason %q; want %q", d.Reason, classes.DegradedReason)
	}
	want(t, classes.DecideDegraded(ctx, env, snap, m.shell("npm test")), classes.Allow, "")
	want(t, classes.DecideDegraded(ctx, env, snap, m.shell("git push")), classes.Deny, classes.LayerFixed)
}

func TestDegradedWithoutSnapshotOnlyReads(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	ctx := context.Background()
	want(t, classes.DecideDegraded(ctx, env, nil, m.file(providers.KindRead, m.in("src/a.ts"))), classes.Allow, "")
	want(t, classes.DecideDegraded(ctx, env, nil, m.shell("git status")), classes.Allow, "")
	want(t, classes.DecideDegraded(ctx, env, nil, m.file(providers.KindWrite, m.in("tests/a.ts"))), classes.Deny, classes.LayerDegraded, classes.DegradedReason)
	want(t, classes.DecideDegraded(ctx, env, nil, m.shell("npm test")), classes.Deny, classes.LayerDegraded, classes.DegradedReason)
	want(t, classes.DecideDegraded(ctx, env, nil, m.shell("sudo x")), classes.Deny, classes.LayerFixed)
	// An exception is never used with the engine down.
	r := rules(t, qaYAML, classes.Decree{ID: 9, Kind: classes.DecreeException, Level: classes.LevelApprover, Effect: classes.Effect{Kind: classes.EffectPermission},
		Action: &classes.Action{Kind: providers.KindShell, Command: "npm install x"}})
	want(t, classes.DecideDegraded(ctx, env, r, m.shell("npm install x")), classes.Deny, classes.LayerDegraded)
}

// Part of CA-PERM-14: the fixed rule over the tool name; the Operador do PC
// base and its ceiling arrive in M7, where the criterion closes.
func TestComputerControlMCPOnlyInTheOperatorBase(t *testing.T) {
	t.Parallel()
	m := newMachine()
	r := rules(t, "version: 1\ntools: {allow: [\"mcp__*\"], default: deny}\noperator: true\n")
	for _, name := range []string{"mcp__claude-in-chrome__navigate", "mcp__computer-use__left_click"} {
		want(t, decide(m.env(), r, toolEvent(providers.KindMCP, name)), classes.Deny, classes.LayerFixed, "Operador do PC")
		op := m.env()
		op.OperatorBase = true
		want(t, decide(op, r, toolEvent(providers.KindMCP, name)), classes.Allow, "")
	}
	// In the Operator base, a class without operator: true gets nothing.
	op := m.env()
	op.OperatorBase = true
	r.Operator = false
	want(t, decide(op, r, toolEvent(providers.KindMCP, "mcp__computer-use__left_click")), classes.Deny, classes.LayerClass, "Operador do PC")
}

// Part of CA-PERM-15: the elevation commands are fixed denials in every
// base; the refusal of the MCP server in an account profile closes in M7.
func TestElevationIsAlwaysDenied(t *testing.T) {
	t.Parallel()
	m := newMachine()
	r := rules(t, anyYAML)
	for _, operator := range []bool{false, true} {
		env := m.env()
		env.OperatorBase = operator
		for _, os := range []string{"linux", "windows"} {
			env.OS = os
			for _, c := range []string{"Start-Process pwsh -Verb RunAs", "runas /user:Administrador cmd", "gsudo x", "start-process pwsh -verb:runas", "sudo ls"} {
				want(t, decide(env, r, m.shell(c)), classes.Deny, classes.LayerFixed, "privilégio")
			}
		}
	}
}

func TestFixedGitRules(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	r := rules(t, anyYAML)
	r.Git.Branches = []string{"epico/42-login"}
	for _, c := range []string{
		"git push", "/usr/bin/git push", `"git" push`, "g\\it push", "git -c x=y push", "bash -c 'git push'", "echo $(git push)", "env FOO=1 git push",
		"git remote add o https://x", "git remote set-url origin x", "git update-ref refs/heads/x HEAD", "git tag v1", "git worktree add ../x",
		"git gc", "git fetch origin", "git pull", "git stash", "git config remote.origin.url x", "git -C /tmp status", "git --git-dir=/x/.git log",
		"GIT_DIR=/x git status", "export GIT_DIR=/x", "git branch -D main", "git checkout -b outra", "git switch -c outra", "git clean -fdx",
		"claude", "codex exec x", "agy", "npx @openai/codex", "nohup claude &",
	} {
		want(t, decide(env, r, m.shell(c)), classes.Deny, classes.LayerFixed)
	}
	for _, c := range []string{
		"git status", "git log --oneline", "git remote -v", "git tag", "git tag -l", "git worktree list", "git stash list", "git config --get user.name",
		"git branch", "git branch -a", "git checkout -b epico/42-login", "git -C tests status", "git config user.name",
	} {
		want(t, decide(env, r, m.shell(c)), classes.Allow, "")
	}
}

func TestGitCommitChecksFlagBranchAndFiles(t *testing.T) {
	t.Parallel()
	m := newMachine()
	r := rules(t, qaYAML)
	env := m.env()
	want(t, decide(env, r, m.shell("git commit -m x")), classes.Deny, classes.LayerFixed, "git")
	env.Git = fakeGit{branch: "epico/42-login", staged: []string{"tests/a.test.ts"}, all: []string{"src/b.ts"}}
	want(t, decide(env, r, m.shell("git commit -m x")), classes.Allow, "")
	want(t, decide(env, r, m.shell("git commit -am x")), classes.Deny, classes.LayerClass, "src/b.ts")
	env.Git = fakeGit{branch: "main", staged: []string{"tests/a.test.ts"}}
	want(t, decide(env, r, m.shell("git commit -m x")), classes.Deny, classes.LayerClass, "main")
	env.Git = fakeGit{branch: "epico/42-login", staged: []string{".mestre/classes/qa/CLASS.md"}}
	want(t, decide(env, r, m.shell("git commit -m x")), classes.Deny, classes.LayerFixed, ".mestre")
	off := rules(t, strings.Replace(qaYAML, "commit: true", "commit: false", 1))
	want(t, decide(env, off, m.shell("git commit -m x")), classes.Deny, classes.LayerClass, "git.commit")
	want(t, decide(env, off, m.shell("git merge epico/42-login")), classes.Deny, classes.LayerClass, "git.commit")
}

func TestFilesBlock(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	r := rules(t, qaYAML)
	want(t, decide(env, r, m.file(providers.KindEdit, m.in("src/app.ts"))), classes.Deny, classes.LayerClass, "files.write")
	want(t, decide(env, r, m.file(providers.KindWrite, m.in("tests/app.test.ts"))), classes.Allow, "")
	want(t, decide(env, r, m.file(providers.KindRead, m.in("api/.env"))), classes.Deny, classes.LayerClass, "files.deny")
	want(t, decide(env, r, m.file(providers.KindRead, m.in("secrets/k"))), classes.Deny, classes.LayerClass, "files.deny")
	want(t, decide(env, r, m.file(providers.KindRead, m.outside)), classes.Deny, classes.LayerClass, "fora do worktree")
	// A class may read outside the worktree by an absolute path it lists,
	// never the folders of the Mestre.
	above := filepath.Dir(filepath.Dir(m.base)) // /var/lib, C:\ProgramData
	abs := rules(t, "version: 1\nfiles: {read: [\"**\", \""+filepath.ToSlash(m.outside)+"\", \""+filepath.ToSlash(above)+"/**\"]}\n")
	want(t, decide(env, abs, m.file(providers.KindRead, m.outside)), classes.Allow, "")
	want(t, decide(env, abs, m.file(providers.KindRead, filepath.Join(above, "outro", "x"))), classes.Allow, "")
	want(t, decide(env, abs, m.file(providers.KindRead, filepath.Join(m.base, "banco", "mestre.db"))), classes.Deny, classes.LayerFixed)
	// A search of a folder above the Mestre's would read them.
	search := event(&providers.Tool{NativeName: "Grep", Kind: providers.KindSearch, Paths: []string{above}}, m.wt)
	want(t, decide(env, abs, search), classes.Deny, classes.LayerFixed)
	want(t, decide(env, abs, m.shell("grep -r senha "+above)), classes.Deny, classes.LayerFixed)
	want(t, decide(env, r, m.shell("cat api/.env")), classes.Deny, classes.LayerClass, "files.deny")
	want(t, decide(env, r, m.shell("cd src && echo x > a.ts")), classes.Deny, classes.LayerClass, "src/a.ts")
	want(t, decide(env, r, m.shell("npm test > tests/out.txt")), classes.Allow, "")
	want(t, decide(env, r, m.shell("npm test > src/out.txt")), classes.Deny, classes.LayerClass, "src/out.txt")
	// A cd that may not hold (a pipeline) checks both folders.
	want(t, decide(env, r, m.shell("cd tests | npm test > a.txt")), classes.Deny, classes.LayerClass, "a.txt")
	// The fixed write zones and the data folders, whatever the class.
	any := rules(t, anyYAML)
	for _, c := range []string{"rm -rf .", "rm -rf .git", "rm -rf .m*", "echo x > .git/config", "sed -i s/a/b/ .codex/config.toml",
		"tee .agents/hooks.json", "mv .claude/settings.json x", "rm -rf ..", "cat " + filepath.Join(m.base, "config", "cofre.key"),
		"cat " + filepath.Join(m.agents, agentID, "policy.json"), "ls " + filepath.Join(m.base, "workspaces", "loja", "mesas", "7-outra")} {
		want(t, decide(env, any, m.shell(c)), classes.Deny, classes.LayerFixed)
	}
}

func TestReadOnlyCommandsGoByTheProgramTheShellFinds(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	r := rules(t, "version: 1\nfiles: {write: [\"tests/**\"]}\ncommands: {allow: [\"npm test\"], default: deny}\n")
	for _, c := range []string{"ls -la src", "cat src/a.ts | grep x | wc -l", "git status", "git log --oneline", "find . -name '*.ts'", "cd src && ls", "mestre done ok"} {
		want(t, decide(env, r, m.shell(c)), classes.Allow, "")
	}
	for _, c := range []string{"tests/ls", "./tests/git status", "find . -delete", "git diff --output=tests/x", "tests/mestre done"} {
		want(t, decide(env, r, m.shell(c)), classes.Deny, "")
	}
	want(t, decide(env, r, m.shell("git -c core.pager=tests/x log")), classes.Deny, classes.LayerClass)
	for _, c := range []string{"PATH=tests ls", "env PATH=tests ls", "LD_PRELOAD=tests/x.so npm test", "GIT_PAGER=tests/x git log"} {
		d := decide(env, r, m.shell(c))
		if runtime.GOOS == "windows" && d.Verdict == classes.Deny {
			continue // read as PowerShell, VAR=x is a program the class does not free
		}
		want(t, d, classes.Ask, "", "muda o programa")
	}
}

func TestDecreesByLevel(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	permit := func(id int64, l classes.Level, allow bool, cmd string) classes.Decree {
		g := &classes.Grant{Commands: []string{cmd}}
		d := classes.Decree{ID: id, Level: l, Effect: classes.Effect{Kind: classes.EffectPermission}}
		if allow {
			d.Effect.Allow = g
		} else {
			d.Effect.Deny = g
		}
		return d
	}
	r := rules(t, qaYAML, permit(1, classes.LevelAdmin, true, "make deploy-teste"))
	want(t, decide(env, r, m.shell("make deploy-teste")), classes.Allow, classes.LayerDecree)
	want(t, decide(env, r, m.shell("make deploy-teste && npm install x")), classes.Ask, classes.LayerClass)
	r = rules(t, qaYAML, permit(1, classes.LevelAdmin, true, "npm test"), permit(2, classes.LevelOwner, false, "npm test"))
	want(t, decide(env, r, m.shell("npm test")), classes.Deny, classes.LayerDecree, "decreto 2")
	// Same level, opposite effects: both suspended, the class decides.
	r = rules(t, qaYAML, permit(1, classes.LevelAdmin, true, "curl *"), permit(2, classes.LevelAdmin, false, "curl *"))
	want(t, decide(env, r, m.shell("curl x")), classes.Deny, classes.LayerClass)
	// An expired decree does not count.
	past := now.Add(-1)
	expired := permit(3, classes.LevelOwner, true, "curl *")
	expired.Until = &past
	r = rules(t, qaYAML)
	r.Decrees = []classes.Decree{expired}
	want(t, decide(env, r, m.shell("curl x")), classes.Deny, classes.LayerClass)
	// A deny decree of a higher level beats an exception.
	r = rules(t, qaYAML, permit(4, classes.LevelOwner, false, "npm install *"),
		classes.Decree{ID: 5, Kind: classes.DecreeException, Level: classes.LevelApprover, Effect: classes.Effect{Kind: classes.EffectPermission},
			Action: &classes.Action{Kind: providers.KindShell, Command: "npm install x"}})
	want(t, decide(env, r, m.shell("npm install x")), classes.Deny, classes.LayerDecree)
}

func TestLockedAndMismatchedRules(t *testing.T) {
	t.Parallel()
	m := newMachine()
	r := rules(t, anyYAML)
	r.Locked = true
	want(t, decide(m.env(), r, m.shell("ls")), classes.Deny, classes.LayerLocked)
	r.Locked = false
	ev := m.shell("ls")
	ev.AgentID = "01JQOUTRO"
	want(t, decide(m.env(), r, ev), classes.Deny, classes.LayerFixed)
	want(t, decide(m.env(), nil, m.shell("ls")), classes.Deny, classes.LayerFixed)
	post := m.shell("ls")
	post.Event = providers.EventPostTool
	want(t, decide(m.env(), nil, post), classes.Allow, "")
	// A tool without what its kind is decided on is denied.
	want(t, decide(m.env(), r, m.file(providers.KindWrite)), classes.Deny, classes.LayerFixed)
	want(t, decide(m.env(), r, m.shell("  ")), classes.Deny, classes.LayerFixed)
	want(t, decide(m.env(), r, event(nil, m.wt)), classes.Deny, classes.LayerFixed)
}

func TestWebByDomain(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	web := func(u string) providers.Event {
		return event(&providers.Tool{NativeName: "WebFetch", Kind: providers.KindWeb, URL: u}, m.wt)
	}
	off := rules(t, qaYAML)
	want(t, decide(env, off, web("https://github.com/x")), classes.Deny, classes.LayerClass, "internet")
	some := rules(t, "version: 1\nnetwork: {internet: true, domains: [\"github.com\", \"*.npmjs.org\"]}\n")
	want(t, decide(env, some, web("https://github.com/x")), classes.Allow, "")
	want(t, decide(env, some, web("https://registry.npmjs.org/x")), classes.Allow, "")
	want(t, decide(env, some, web("https://example.com/")), classes.Deny, classes.LayerClass, "example.com")
	want(t, decide(env, some, event(&providers.Tool{NativeName: "WebSearch", Kind: providers.KindWeb}, m.wt)), classes.Deny, classes.LayerClass)
	anyDomain := rules(t, "version: 1\nnetwork: {internet: true}\n")
	want(t, decide(env, anyDomain, web("https://example.com/")), classes.Allow, "")
}

func TestOtherEditorOfTheTurn(t *testing.T) {
	t.Parallel()
	m := newMachine()
	edits := classes.NewEditTracker()
	edits.Record("42-login", "01JQDEV", "Dev 1", []string{m.in("tests/a.ts")})
	env := m.env()
	env.Edits = edits
	r := rules(t, qaYAML)
	want(t, decide(env, r, m.file(providers.KindEdit, m.in("tests/a.ts"))), classes.Deny, classes.LayerFixed, "Dev 1")
	edits.EndTurn("42-login", "01JQDEV")
	want(t, decide(env, r, m.file(providers.KindEdit, m.in("tests/a.ts"))), classes.Allow, "")
}

func TestWindowsCommandsInBothShells(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	env.OS = "windows"
	any := rules(t, anyYAML)
	for _, c := range []string{
		"Remove-Item -Recurse .mestre", `Set-Content -Path .claude\agents\a.md -Value x`, `powershell -Command "git push"`,
		"cmd /c git push", "iex 'git push'", "& git push", "pwsh -EncodedCommand ZwBpAHQAIABwAHUAcwBoAA==",
	} {
		want(t, decide(env, any, m.shell(c)), classes.Deny, classes.LayerFixed)
	}
	// The other spellings of the same file on Windows.
	for _, p := range []string{".mestre./classes/x", ".mestre::$INDEX_ALLOCATION/classes/x", ".GIT /config"} {
		want(t, decide(env, any, m.file(providers.KindWrite, m.wt+"/"+p)), classes.Deny, classes.LayerFixed)
	}
	want(t, decide(env, any, m.file(providers.KindRead, `\\?\`+filepath.Join(m.accounts, "p", "auth.json"))), classes.Deny, classes.LayerFixed)
	want(t, decide(env, any, m.shell("Get-ChildItem | Remove-Item")), classes.Ask, "")
	want(t, decide(env, any, m.shell("echo $env:PATH")), classes.Ask, "")
	want(t, decide(env, rules(t, qaYAML), m.shell("Get-Content src/a.ts")), classes.Allow, "")
}

// CA-APROV-POL-07
func TestNoAgentWritesTheApprovalPolicyFile(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	r := rules(t, anyYAML)
	const policy = ".mestre/regras/politica-de-aprovacao.yaml"
	want(t, decide(env, r, m.file(providers.KindWrite, filepath.Join(m.wt, policy))), classes.Deny, classes.LayerFixed, "politica-de-aprovacao.yaml")
	want(t, decide(env, r, m.file(providers.KindEdit, filepath.Join(m.wt, policy))), classes.Deny, classes.LayerFixed, "politica-de-aprovacao.yaml")
	want(t, decide(env, r, m.shell("echo 'versao: 1' > "+policy)), classes.Deny, classes.LayerFixed, "politica-de-aprovacao.yaml")
	want(t, decide(env, r, m.shell("rm "+policy)), classes.Deny, classes.LayerFixed, ".mestre")
}
