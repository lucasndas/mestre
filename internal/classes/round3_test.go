package classes_test

import (
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/providers"
)

// Regression tests of the third round of the M3 audit (git writes): the git
// server and remote-helper subcommands and their dashed forms, the git
// features that run a program (aliases, config keys, exec options), and the
// commands that write a protected path without naming it (directory copies,
// ln -t, checkout of a pathspec, decompressor and archive outputs). They
// turn the verifier's probes into permanent tests.

func r3env(t *testing.T) (machine, classes.Env) {
	m := newMachine()
	e := m.env()
	e.OS = "linux"
	return m, e
}

// H1: the git subcommands that serve or transport a repository, and their
// dashed and remote-helper forms, are a fixed deny; a receive-pack can move
// the shared mirror's refs.
func TestRound3GitServerAndDashedForms(t *testing.T) {
	t.Parallel()
	m, env := r3env(t)
	free := rules(t, anyYAML)
	for _, c := range []string{
		"git receive-pack .", "git-receive-pack .", "git upload-pack .", "git-upload-pack .",
		"git upload-archive .", "git shell -c x", "git daemon --export-all", "git http-backend", "git instaweb",
		"git receive-pack . < req", "printf x | git receive-pack .",
		"git remote-ext host cmd", "git-remote-https origin https://x", "git remote-fd 3",
		"git-push", "git-fetch", "git-update-ref refs/heads/x y",
	} {
		want(t, decide(env, free, m.shell(c)), classes.Deny, classes.LayerFixed)
	}
	// Dashed forms route through the subcommand rules: under a class whose
	// branches are only the table's, a dashed branch/checkout of another
	// branch is denied.
	def := rules(t, "version: 1\n")
	for _, c := range []string{"git-branch -D other", "git-checkout -B main"} {
		want(t, decide(env, def, m.shell(c)), classes.Deny, classes.LayerFixed)
	}
	// git remote (the porcelain) still lists freely.
	for _, c := range []string{"git remote -v", "git remote show origin"} {
		if d := decide(env, free, m.shell(c)); d.Verdict == classes.Deny {
			t.Errorf("%q wrongly denied: %q", c, d.Reason)
		}
	}
}

// H2: git features that run a program. An alias with ! or that names a
// fixed subcommand, and a program-running config key whose literal value
// holds a fixed rule, are fixed denies; the other exec features are at
// least an ask (never a silent baixo run).
func TestRound3GitExecFeatures(t *testing.T) {
	t.Parallel()
	m, env := r3env(t)
	free := rules(t, anyYAML)
	deny := []string{
		"git -c alias.x='!git push' x", "git -calias.x=!true x", "git -c alias.p=push p",
		"git -c core.pager='git push' log", "git -c core.fsmonitor='git push' status",
		"git -c diff.external='git push' diff", "git -c sequence.editor='git push' rebase -i HEAD~1",
		"git -c filter.x.clean='git push' status",
		"git rebase -x 'git push' HEAD~1", "git rebase --exec 'git push' HEAD~1",
		"git bisect run git push", "git difftool -y -x 'git push'", "git grep -O'git push' foo",
		"git grep --open-files-in-pager='git push' foo",
		"HOME=/tmp/h git status", "XDG_CONFIG_HOME=/tmp/x git status",
	}
	for _, c := range deny {
		want(t, decide(env, free, m.shell(c)), classes.Deny, classes.LayerFixed)
	}
	// Program-running keys and exec options with a harmless or opaque value:
	// never a silent baixo run (they ask).
	ask := []string{
		"git -c core.pager=less log", "git -c core.hooksPath=/tmp/h status",
		"git --config-env=core.pager=X log", "git rebase -x 'echo hi' HEAD~1",
		"git bisect run true", "git -c alias.x='log --oneline' x",
	}
	for _, c := range ask {
		if d := decide(env, free, m.shell(c)); d.Verdict != classes.Ask {
			t.Errorf("%q = %s by %s (%q); want ask", c, d.Verdict, d.Layer, d.Reason)
		}
	}
}

// H4: commands that write a protected path without naming it as a plain
// argument are a fixed deny when the target tree holds a protected path.
func TestRound3ProtectedWritesWithoutVisiblePath(t *testing.T) {
	t.Parallel()
	m, env := r3env(t)
	free := rules(t, anyYAML)
	for _, c := range []string{
		// directory copies into the worktree root or a protected folder
		"cp -rT tmpl .", "cp -r tmpl/. .", "cp -a tmpl/. .", "rsync -a tmpl/ .",
		// ln -t with combined short flags
		"ln -st .claude x/settings.json", "ln -s -t .claude x/settings.json",
		// git checkout/restore of a pathspec from a tree
		"git checkout x -- .claude", "git checkout x -- .", "git checkout HEAD -- .claude",
		"git restore --source HEAD~1 -- .claude",
		// plumbing that writes the worktree or the index
		"git checkout-index -f -- .claude/settings.json", "git read-tree --prefix=.git/ HEAD",
		"git update-index --cacheinfo 100644,abc123,.git/config",
		// decompressor implicit outputs
		"gunzip .git/config.gz", "gunzip .mcp.json.gz", "xz -d .claude/settings.json.xz",
		"zstd -d .git/x.zst", "bunzip2 .git/x.bz2",
		// archive destinations
		"dpkg-deb -x a.deb .git", "unzip a.zip -d .claude", "tar -xf a.tar -C .mestre", "7z x a.7z -o.git",
	} {
		want(t, decide(env, free, m.shell(c)), classes.Deny, classes.LayerFixed)
	}
}

// H4 on Windows: the copy and link tools write a protected destination too,
// read in the PowerShell and cmd dialect.
func TestRound3ProtectedWritesWindows(t *testing.T) {
	t.Parallel()
	m := newMachine()
	w := windowsEnv()
	free := rules(t, anyYAML)
	for _, c := range []string{
		`xcopy tmpl .git /E`, `robocopy tmpl .git /E`, `fsutil hardlink create .git\x y`,
		`Copy-Item -Recurse tmpl .git`, `copy a .git\config`, `gunzip .git\config.gz`,
	} {
		want(t, decide(w, free, event(m.shell(c).Tool, w.Worktree)), classes.Deny, classes.LayerFixed)
	}
	if d := decide(w, free, event(m.shell(`xcopy tmpl build /E`).Tool, w.Worktree)); d.Verdict == classes.Deny {
		t.Errorf("xcopy into a normal folder wrongly denied: %q", d.Reason)
	}
}

// H4 legit: the same tools, with a destination inside the worktree that is
// not protected, are not denied; a decompressor to stdout writes nothing.
func TestRound3ProtectedWritesLegit(t *testing.T) {
	t.Parallel()
	m, env := r3env(t)
	free := rules(t, anyYAML)
	for _, c := range []string{
		"cp -r src dst", "cp -a tmpl build", "cp a.txt b.txt", "ln -s target link", "ln -st build x",
		"gunzip data.tar.gz", "gzip -k file.txt", "gunzip -c .git/config.gz", "zcat .git/x.gz",
		"tar -xf a.tar", "tar -xf a.tar -C build", "unzip a.zip -d out", "7z x a.7z -oout",
		"git checkout -- src/a.go", "git restore src/a.go", "git read-tree HEAD",
	} {
		if d := decide(env, free, m.shell(c)); d.Verdict == classes.Deny {
			t.Errorf("%q wrongly denied: %s (%q)", c, d.Layer, d.Reason)
		}
	}
}

// M7: a write to a project hook script that git runs (husky, a git-hooks
// folder, .claude/hooks) is never a silent baixo run; .git/hooks stays a
// fixed deny.
func TestRound3ProjectHookScripts(t *testing.T) {
	t.Parallel()
	m, env := r3env(t)
	def := rules(t, "version: 1\n")
	for _, rel := range []string{".husky/pre-commit", ".githooks/pre-push", ".claude/hooks/on_write.sh"} {
		d := decide(env, def, m.file(providers.KindWrite, m.in(rel)))
		if d.Verdict == classes.Allow {
			t.Errorf("write %s = allow (%q); want an approval, not a silent write", rel, d.Reason)
		}
	}
	// .git/hooks is a fixed deny as before.
	want(t, decide(env, def, m.file(providers.KindWrite, m.in(".git/hooks/pre-commit"))), classes.Deny, classes.LayerFixed)
}

// The legit git commands the characters run are unaffected.
func TestRound3LegitGitUnaffected(t *testing.T) {
	t.Parallel()
	m, env := r3env(t)
	free := rules(t, anyYAML)
	for _, c := range []string{
		"git status", "git diff", "git diff --stat", "git log --oneline", "git show HEAD",
		"git add -A", "git add .", "git add src/a.go", "git switch epico/42-login",
		"git checkout -- src/a.go", "git restore src/a.go", "git restore --staged src/a.go",
		"git -c user.name=x -c user.email=y status", "git rebase epico/42-login",
		"git branch", "git branch -a", "git stash list", "git tag -l", "git log -p | less",
	} {
		if d := decide(env, free, m.shell(c)); d.Verdict == classes.Deny && d.Layer == classes.LayerFixed {
			t.Errorf("%q denied by a fixed rule: %q", c, d.Reason)
		}
	}
}
