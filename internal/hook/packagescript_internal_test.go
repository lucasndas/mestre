package hook

import "testing"

// The refusal of the scripts of the package (until the engine runs the
// approved copy, docs/classes/classes.md#hooks-extras-da-classe) holds for
// every spelling of the folder: the audit's probe found these passing the
// check of whitespace-separated words.
func TestEveryWayToReachTheScriptsOfThePackageIsRefused(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		// the plain ones
		"./hooks/x", "sh hooks/x", "bash -c 'hooks/x'", `bash -c "hooks/x"`, "HOOKS/x", `hooks\x`, `.\hooks\x`,
		"../pkg/hooks/x", "env FOO=1 hooks/x", "./scripts/hooks/x", "python3 -m hooks.x", "make -C hooks",
		// the evasions
		"cd hooks && ./x", "cd hooks; sh x", "true;hooks/x", "true&&hooks/x", "true||hooks/x", "true | hooks/x", "(hooks/x)",
		"$PWD/hooks/x", "${MESTRE_WT}/hooks/x", `"$(pwd)/hooks/x"`, ".//hooks/x", "hooks/../hooks/x",
		"/home/u/wt/hooks/x", `C:\wt\hooks\x`, `%CD%\hooks\x`,
		`h""ooks/x`, `h\ooks/x`, `h''ooks/x`, "h`ooks/x", "h^ooks/x", `"h"o"oks"/x`, "sh <hooks/x", "sh < hooks/x",
		"x=hooks; $x/y", `sh -c "$(cat hooks/x)"`, "`echo hooks`/x", "HoOkS/x", "HOO\u212aS/x",
		// built at run time: no "hooks" in the line at all
		"$d/x", "${d}/x", "$(echo ho)oks/x", "./?ooks/x", "./h*/x", "./[h]ooks/x", "./{h,}ooks/x", "~/x", "%d%\\x",
		"cd $d && ./x", "cd h* && ./x", "pushd {h,x}ooks; ./x", "Set-Location $d; .\\x", "make -C $d", "cd ho?ks;./x",
		"true;$d/x", "true&&./$d/x",
	} {
		if packageScript(line) == "" {
			t.Errorf("%q passes the refusal of the scripts of the package", line)
		}
	}
}

// A line that cannot reach hooks/ is not refused, so a class with ordinary
// commands keeps its hooks.
func TestALineThatCannotReachThePackageRuns(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		"npm test", "go vet ./...", "gofmt -l .", "make lint", "echo $HOME", "echo done > /dev/null", "git diff --stat",
		"curl -fsS https://example.com/a?b=1", "make -C build", "cd build && make", "mestre note write 'ok'",
		"sh -c 'echo hi'", "test -f go.mod || exit 1", `dir .\build`,
	} {
		if why := packageScript(line); why != "" {
			t.Errorf("%q is refused: %s", line, why)
		}
	}
}
