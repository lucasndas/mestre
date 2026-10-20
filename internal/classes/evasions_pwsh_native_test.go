package classes_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
)

// judgeAs decides a command line as the tool of a provider names it.
func judgeAs(env classes.Env, r *classes.Rules, tool, cmd string) classes.Decision {
	return decide(env, r, event(&providers.Tool{NativeName: tool, Kind: providers.KindShell, Command: cmd}, env.Worktree))
}

// A name with an extension or a path is a program even when its bare word is the alias of
// a cmdlet (sort.exe is not Sort-Object), a hyphen does not make a cmdlet, and the program
// takes the legacy arguments of Windows PowerShell 5.1: an inner quote splits the argument,
// a trailing backslash after a space eats the closing quote, an empty argument is dropped,
// and an alias set in the line may stand for any name.
// CA-PERM-33
func TestNativeProgramsThatAliasesAndHyphensHideStillAsk(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	win := evasionEnvs()[1]
	for _, tool := range []string{"Bash", "PowerShell"} {
		for _, c := range []string{
			`sort.exe 'evil.txt" /O ".git/hooks/pre-commit'`,
			`rm.exe 'x" -rf .git "y'`,
			`cp.exe 'evil.sh" ".git/hooks/pre-commit'`,
			`where.exe 'a" b "c'`,
			`tee.exe 'a" b "c'`,
			`diff.exe 'a" b "c'`,
			win.env.Worktree + `\rm.exe 'x" -rf .git "y'`,
			`.\rm 'x" -rf .git "y'`,
			`& 'rg.exe' 'a" --pre=calc.exe "b c' .`,
			`& "` + win.env.Worktree + `\rg.exe" 'a" --pre=calc.exe "b c' .`,
			`cross-env 'A=1" HOME=C:\tmp "B=2' git status`,
			`git-lfs 'a" b "c'`,
			`docker-compose 'a" b "c'`,
			`clang-format 'a" b "c'`,
			`ts-node 'a" b "c'`,
			`pre-commit 'a" b "c'`,
			`rg 'a b\' 'c --pre=calc.exe .'`,
			`git log 'a b\' 'c --output=.git/hooks/pre-commit'`,
			"rg 'a\tb\\' x",
			`git -c '' core.fsmonitor='git push' status`,
			`git -c "" core.fsmonitor='git push' status`,
			`Set-Alias rg echo; rg 'a" b "c'`,
			`sal foo git; foo 'a" b "c'`,
			`New-Alias zz git; zz 'a" b "c'`,
			`nal zz git; zz 'a" b "c'`,
			`Set-Alias sort git; sort 'a" b "c'`,
			`Set-Alias Get-Item git; Get-Item 'a" b "c'`,
			`Set-Item Alias:foo git; foo 'a" b "c'`,
		} {
			if d := judgeAs(win.env, r, tool, c); d.Verdict != classes.Ask {
				t.Errorf("%s: %q = %s by %s (%q); want ask", tool, c, d.Verdict, d.Layer, d.Reason)
			}
		}
		// the cmdlets, and the aliases of them as bare words, take the arguments as they are
		for _, c := range []string{
			`sort 'a"b'`, `Sort-Object 'a"b'`, `Select-String -Pattern 'a"b' x.txt`, `echo 'say "hi"'`,
			`Get-ChildItem 'a b\'`, `rg 'a b' .`, `rg 'a\' .`, `git log 'a b'`, `.\build.ps1 'a"b'`,
		} {
			if d := judgeAs(win.env, r, tool, c); d.Verdict != classes.Allow {
				t.Errorf("%s: %q = %s (%q); want allow", tool, c, d.Verdict, d.Reason)
			}
		}
	}
	// Linux hands the argument over as it is
	lin := evasionEnvs()[0]
	for _, c := range []string{`rg 'a b\' .`, `sort.exe 'a"b'`, `git -c '' status`, `cross-env 'A=1" B=2' x`} {
		if d := judgeAs(lin.env, r, "Bash", c); d.Verdict != classes.Allow {
			t.Errorf("linux: %q = %s (%q); want allow", c, d.Verdict, d.Reason)
		}
	}
}
