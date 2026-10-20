package classes_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

// The list of known-safe programs (docs/classes/lista-de-seguros.md): baixo
// only for the programs and shapes of the list, medio for anything else,
// with the rows and the signals on top.

// tableEnvs are the character's environments on both systems, with the git
// state of the table: the scripts came from the repository.
func tableEnvs() map[string]classes.Env {
	m := newMachine()
	lin := m.env()
	lin.OS = "linux"
	lin.Git = fakeGit{branch: "epico/42-login"}
	win := windowsEnv()
	win.Git = fakeGit{branch: "epico/42-login"}
	return map[string]classes.Env{"linux": lin, "windows": win}
}

func decideOn(t *testing.T, env classes.Env, r *classes.Rules, cmd string) classes.Decision {
	t.Helper()
	m := newMachine()
	return decide(env, r, event(m.shell(cmd).Tool, env.Worktree))
}

// CA-RISCO-14
func TestEverydayCommandsAreLowOnBothReadings(t *testing.T) {
	t.Parallel()
	def := rules(t, "version: 1\n")
	everyday := []string{
		"ls -la", "cat README.md", "head -n 20 main.go", "grep -rn \"func main\" .", "rg -n TODO src",
		"find . -name '*.go' -not -path './vendor/*'", "stat main.go", "diff a.txt b.txt", "tree -L 2", "du -sh .",
		"which go", "date", "echo hello", "printf '%s\\n' x", "test -f a.txt && echo yes", "[ -d src ] || mkdir src",
		"jq . package.json", "sort a.txt | uniq -c", "cut -d, -f1 a.csv", "sha256sum dist/app", "less a.txt",
		"awk '{print $1}' a.txt", "sed -n '1,5p' a.txt", "sed -i 's/a/b/' a.txt", "tar tzf a.tgz",
		"mkdir -p build/out", "touch a.txt", "cp a.txt b.txt", "mv a.txt b.txt", "rm a.txt",
		"git status", "git diff --stat", "git log --oneline -10", "git add -A", "git add . && git commit -m \"fix: x\"",
		"git branch -a", "git tag -l",
		"npm install", "npm ci", "npm test", "npm run build", "npm run lint -- --fix", "pnpm install", "pnpm build",
		"yarn", "yarn build", "bun install", "bun run build", "bun test",
		"go build ./...", "go test -run TestX ./internal/classes", "go vet ./...", "go run ./cmd/mestre", "go mod tidy",
		"gofmt -l .", "golangci-lint run ./...", "cargo build", "cargo clippy -- -D warnings", "cargo +nightly build",
		"python -m pytest -q", "pytest -k test_x", "python -m pip list", "python -m venv .venv", "python x.py",
		"python x.py <<EOF\ndata\nEOF", "cat f.json | python -m json.tool", "cat f.json | python3 -m json.tool",
		"uv run pytest", "uv sync", "poetry install", "poetry run pytest", "make", "make -j4 test", "make test VERBOSE=1",
		"node scripts/build.js", "bash scripts/build.sh", "./scripts/build.sh", "sh -e ./scripts/ci.sh",
		"eslint src", "prettier --check .", "ruff check .", "tsc --noEmit", "dotnet test", "mvn -q package",
		"./gradlew build", "cd src && go test ./...", "export NODE_ENV=test && npm test", "NODE_ENV=test npm test",
		"set -euo pipefail", "npm test 2>&1 | tail -20", "go version", "node --version",
		"cat <<'EOF' > notes.txt\ngit push\nEOF", "git commit -F - <<'EOF'\nfix: x\n\nnever git push\nEOF",
	}
	for goos, env := range tableEnvs() {
		for _, cmd := range everyday {
			d := decideOn(t, env, def, cmd)
			if d.Verdict != classes.Allow || d.Risk.Risk != "" && d.Risk.Risk != classes.RiskLow {
				t.Errorf("%s: %q = %s/%s (%s %s); want allow, baixo", goos, cmd, d.Verdict, d.Risk.Risk, d.Risk.Rule, d.Reason)
			}
		}
	}
	// a quote inside a quoted argument: low on Linux; on Windows PowerShell 5.1 hands it to the
	// program as several arguments, so it asks (CA-PERM-33)
	if d := decideOn(t, tableEnvs()["linux"], def, "awk '{print $1 | \"sort\"}' a.txt"); d.Verdict != classes.Allow {
		t.Errorf("linux: awk with a quote = %s (%s)", d.Verdict, d.Reason)
	}
	win := tableEnvs()["windows"]
	if d := decideOn(t, win, def, "awk '{print $1 | \"sort\"}' a.txt"); d.Verdict != classes.Ask {
		t.Errorf("windows: awk with a quote = %s (%s); want ask", d.Verdict, d.Reason)
	}
	// A PowerShell script of the worktree, and the call operator that a
	// POSIX shell refuses as a syntax error.
	for _, cmd := range []string{`& .\script.ps1`, `& .\scripts\build.ps1 -Fast`, "Get-ChildItem -Recurse | Select-String foo"} {
		if d := decideOn(t, win, def, cmd); d.Verdict != classes.Allow {
			t.Errorf("windows: %q = %s/%s (%s %s); want allow", cmd, d.Verdict, d.Risk.Risk, d.Risk.Rule, d.Reason)
		}
	}
}

// CA-RISCO-14
func TestTheLongTailIsMedio(t *testing.T) {
	t.Parallel()
	def := rules(t, "version: 1\n")
	for goos, env := range tableEnvs() {
		for _, cmd := range []string{
			"foo --bar", "make --eval='x:;echo hi' x", "make SHELL=/bin/sh", "make test CC=\"sh -c 'id'\"",
			"find . -exec rm {} \\;", "npm run --script-shell=sh build", "node -r ./x.js y.js", "node -e 'console.log(1)'",
			"less +F log.txt", "vim -c ':wq' f", "gdb -batch -ex run ./a.out", "R -e 1", "julia -e 1", "tclsh x.tcl",
			"yarn exec x", "pnpm exec x", "bunx x", "npx x", "tar czf out.tgz src", "zip -r out.zip src",
			"tmux new -d 'echo hi'", "screen -dm echo hi", "expect -c 'spawn echo hi'",
			"emacs --batch -l x.el", "man -P cat ls", "sort --compress-program=gzip f", "go test -exec x ./...",
			"go build -toolexec x ./...", "cargo --config x build", "./gradlew --init-script x.gradle build",
			"mvn exec:exec", "git rebase -i HEAD~2", "git cherry-pick abc",
			"complete -C x y", "bind -x a", "mapfile -C x", "python -m code", "python -m http.server",
			"python -m pip install x", "yq -i '.a=1' f.yaml", "uniq a b", "hostname x", "date -s 2020-01-01",
			"printf -v X '%s' y", "bash --rcfile x.sh -i", "gawk -i inplace '{print}' a.txt", "awk -f prog.awk f",
			"awk '{print > $1}' f", "awk 'BEGIN{cmd=\"x\"; print | cmd}'", "awk 'BEGIN{f=\"x\"; @f()}'",
			"systemd-run --user ls", "cat f | xargs wc -l",
		} {
			d := decideOn(t, env, def, cmd)
			if d.Verdict != classes.Ask || d.Risk.Risk != classes.RiskMedium {
				t.Errorf("%s: %q = %s/%s (%s %s); want ask, medio", goos, cmd, d.Verdict, d.Risk.Risk, d.Risk.Rule, d.Reason)
			}
		}
		// The git features that run a program are alto.
		for _, cmd := range []string{"git -c core.pager=cat log", "git -c alias.st=status st", "git --config-env=core.editor=E commit",
			"git rebase -x 'make test' HEAD~2", "git bisect run make test", "git difftool -x meld", "git grep -Ox y"} {
			if d := decideOn(t, env, def, cmd); d.Verdict != classes.Ask || d.Risk.Risk != classes.RiskHigh {
				t.Errorf("%s: %q = %s/%s (%s %s); want ask, alto", goos, cmd, d.Verdict, d.Risk.Risk, d.Risk.Rule, d.Reason)
			}
		}
	}
}

// CA-RISCO-15
func TestACommandInAnArgumentIsAnalysed(t *testing.T) {
	t.Parallel()
	free := rules(t, anyYAML)
	for goos, env := range tableEnvs() {
		for _, cmd := range []string{
			"tmux new -d 'git push'", "tmux send-keys 'git push' Enter", "screen -dm git push",
			"screen -S x -X stuff 'git push\\n'", "expect -c 'spawn git push'", "flock f git push",
			"flock f -c 'git push'", "flock -c 'git push' f", "unbuffer git push", "chrt 1 git push",
			"chrt -f 1 git push", "taskset 1 git push", "taskset -c 0 git push", "faketime '2020-01-01' git push",
			"nice -n 5 git push", "ionice -c 3 git push", "timeout --signal KILL 5 git push", "stdbuf -oL git push",
			"strace -c git push", "numactl -N 0 git push", "vim -c '!git push' -c q", "vi +'!git push'",
			"emacs --batch --eval '(shell-command \"git push\")'", "man -P 'git push' ls", "gdb -batch -ex 'shell git push'",
			"less -f +'!git push' f", "tar -I 'git push' -cf x.tar .", "tar -xf a.tar --to-command='git push'",
			"tar -xf a.tar --checkpoint=1 --checkpoint-action=exec='git push'", "zip -T -TT 'git push' x.zip f",
			"sort --compress-program='git push' f", "make --eval='x:;git push' x", "rsync -e 'git push' a b",
			"ssh -o ProxyCommand='git push' x true", "parallel ::: 'git push'", "parallel git ::: push",
			"R -e 'system(\"git push\")'", "julia -e 'run(`git push`)'", "perl -e 'system(\"git push\")'",
			"ruby -e '`git push`'", "php -r 'system(\"git push\");'", "lua -e 'os.execute(\"git push\")'",
			"python -c 'import os; os.system(\"git push\")'", "python3 -c 'import subprocess; subprocess.run([\"git\", \"push\"])'",
			"node -e 'require(\"child_process\").execSync(\"git push\")'", "osascript -e 'do shell script \"git push\"'",
			"rbash -c 'git push'", "yash -c 'git push'", "nu -c 'git push'", "elvish -c 'git push'", "xonsh -c 'git push'",
			"fish --command='git push'", "tcsh -c 'git push'", "bash -c -e 'git push'",
			"echo git push | sh -o posix", "echo git push | bash -O extglob", "echo git push | bash --rcfile x",
			"gawk -i inplace '{print}' .git/config", "awk 'BEGIN{print \"x\" > \".git/config\"}'",
			"awk 'BEGIN{print \"x\" >> \".mestre/y\"}'",
		} {
			if d := decideOn(t, env, free, cmd); d.Verdict != classes.Deny || d.Layer != classes.LayerFixed {
				t.Errorf("%s: %q = %s by %s (%s); want the fixed deny", goos, cmd, d.Verdict, d.Layer, d.Reason)
			}
		}
	}
}

// CA-RISCO-16
func TestTheOwnerExtendsTheList(t *testing.T) {
	t.Parallel()
	adj, err := classes.ParseRiskAdjustments([]byte(`ajustes:
  - { comando: "mytool *", risco: baixo }
  - { comando: "make *", risco: baixo }
`))
	if err != nil {
		t.Fatal(err)
	}
	env := riskEnv(&classes.RiskPolicy{Adjustments: adj})
	if r := shellRisk(env, riskRules(), "mytool --x"); r.Risk != classes.RiskLow || r.Adjustment != 1 {
		t.Errorf("mytool --x = %+v; want baixo by adjustment 1", r)
	}
	// Code in an option of the list holds over an adjustment.
	if r := shellRisk(env, riskRules(), "make --eval='x:;echo hi' x"); r.Risk != classes.RiskMedium {
		t.Errorf("make --eval under make * baixo = %+v; want medio", r)
	}
	for _, set := range []string{
		`ajustes: [{comando: "python -c 'print(1)'", risco: baixo}]`,
		`ajustes: [{comando: "sh -c 'echo hi'", risco: baixo}]`,
		`ajustes: [{comando: "make --eval*", risco: baixo}]`,
		`ajustes: [{comando: "find . -exec *", risco: baixo}]`,
		`ajustes: [{comando: "flock *", risco: baixo}]`,
		`ajustes: [{comando: "nice *", risco: baixo}]`,
		`ajustes: [{comando: "uv run *", risco: baixo}]`,
		`ajustes: [{comando: "tmux new *", risco: baixo}]`,
		`ajustes: [{comando: "chrt 1 git push", risco: baixo}]`,
	} {
		if _, err := classes.ParseRiskAdjustments([]byte(set)); !errors.Is(err, classes.ErrInvalidRisk) {
			t.Errorf("%s: err = %v; want refused", set, err)
		}
	}
	// A wrapper or a runner with a wildcard is never an approvable pattern.
	for _, p := range []string{"nice *", "flock *", "uv run *", "pnpm exec *"} {
		act := classes.Action{Kind: "shell", Tool: "Bash", Command: strings.TrimSuffix(p, "*") + "x"}
		if _, err := classes.CheckApprovablePattern(p, act, "dev", "42-login"); !errors.Is(err, classes.ErrPatternTooBroad) {
			t.Errorf("%s: err = %v; want pattern_too_broad", p, err)
		}
	}
}

// A program of the worktree given by its path is that file, whatever its
// name: ./nohup is not the wrapper, and a .ps1 is not the program of the
// same name (script, nice). Changed at the table, it is medio.
func TestAProgramOfTheWorktreeNamedLikeAWrapperIsTheFile(t *testing.T) {
	t.Parallel()
	def := rules(t, "version: 1\n")
	for goos, env := range tableEnvs() {
		env.Git = fakeGit{branch: "epico/42-login", changed: []string{"nohup", "script.ps1", "nice"}}
		cmds := []string{"./nohup ls", "./nice git status"}
		if goos == "windows" {
			cmds = append(cmds, `& .\script.ps1`, `.\script.ps1 -x`)
		}
		for _, cmd := range cmds {
			if d := decideOn(t, env, def, cmd); d.Verdict != classes.Ask || d.Risk.Risk != classes.RiskMedium {
				t.Errorf("%s: %q = %s/%s (%s %s); want ask, medio (a script changed at the table)", goos, cmd, d.Verdict, d.Risk.Risk, d.Risk.Rule, d.Reason)
			}
		}
	}
}
