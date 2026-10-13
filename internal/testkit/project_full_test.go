//go:build full

package testkit_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/testkit"
)

// CA-TREINO-05
func TestNewProjectFullSuiteMakesRealCommit(t *testing.T) {
	t.Parallel()
	a := testkit.NewProject(t, "go-basico")
	b := testkit.NewProject(t, "go-basico")

	if a.Git != nil {
		t.Fatal("full-suite copy still uses the fake git")
	}
	// Unix timestamps: the ISO form of UTC differs between git versions
	// (+00:00 in 2.43, Z in newer ones). 1767225600 is 2026-01-01T00:00:00Z.
	log := testkit.ProjectGit(t, a.Dir, "log", "--format=%an <%ae>|%at|%ct|%s", testkit.ProjectBranch)
	want := "Mestre Treino <treino@example.invalid>|1767225600|1767225600|chore: initial commit"
	if got := strings.TrimSpace(log); got != want {
		t.Fatalf("git log = %q, want %q", got, want)
	}
	if st := testkit.ProjectGit(t, a.Dir, "status", "--porcelain"); st != "" {
		t.Fatalf("working tree not clean:\n%s", st)
	}
	if a.Head == "" || a.Head != b.Head {
		t.Fatalf("heads %q and %q differ: the commit is not reproducible", a.Head, b.Head)
	}
}

var goTestElapsed = regexp.MustCompile(`(?m)^ok\s+\S+\s+([0-9.]+)s`)

// goTest runs the project's own suite in dir and returns the output, whether
// it passed and the run time reported by go test (without compiling).
func goTest(t *testing.T, dir string) (string, bool, time.Duration) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", "test", "-count=1", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	var total time.Duration
	for _, m := range goTestElapsed.FindAllStringSubmatch(string(out), -1) {
		s, _ := strconv.ParseFloat(m[1], 64)
		total += time.Duration(s * float64(time.Second))
	}
	return string(out), err == nil, total
}

// CA-TREINO-06
func TestModelSuiteIsFastAndHiddenTestsCatchTheTasks(t *testing.T) {
	t.Parallel()
	t.Run("pristine", func(t *testing.T) {
		t.Parallel()
		p := testkit.NewProject(t, "go-basico")
		out, ok, took := goTest(t, p.Dir)
		if !ok || took <= 0 || took >= time.Second {
			t.Fatalf("model suite: passed=%v in %v (want < 1s)\n%s", ok, took, out)
		}
	})
	// A reference change per task, written by the test, stands in for the agent.
	solve := map[string]func(t *testing.T, dir string){
		"T1": func(t *testing.T, dir string) {
			replaceIn(t, filepath.Join(dir, "stats", "stats.go"), "\tvar m float64\n\tfor _, x := range xs {", "\tm := xs[0]\n\tfor _, x := range xs {")
		},
		"T2": func(t *testing.T, dir string) {
			appendTo(t, filepath.Join(dir, "stats", "stats.go"),
				"\n// Mean returns the arithmetic mean of xs.\nfunc Mean(xs []float64) (float64, error) {\n\tif len(xs) == 0 {\n\t\treturn 0, ErrEmpty\n\t}\n\treturn Sum(xs) / float64(len(xs)), nil\n}\n")
		},
		"T3": func(t *testing.T, dir string) {
			replaceIn(t, filepath.Join(dir, "README.md"), "- `CountAbove`", "- `Max`: o maior número.\n- `CountAbove`")
		},
	}
	for _, task := range testkit.LoadTasks(t, "go-basico") {
		t.Run(task.ID, func(t *testing.T) {
			t.Parallel()
			fn, ok := solve[task.ID]
			if !ok {
				t.Fatalf("no reference change for %s", task.ID)
			}
			p := testkit.NewProject(t, "go-basico")
			p.ApplyHiddenTests(t, task.ID)
			if out, ok, _ := goTest(t, p.Dir); ok || !strings.Contains(out, "TestHidden"+task.ID) && !strings.Contains(out, "undefined: stats.Mean") {
				t.Fatalf("hidden tests of %s pass on the unchanged copy:\n%s", task.ID, out)
			}
			fn(t, p.Dir)
			if out, ok, _ := goTest(t, p.Dir); !ok {
				t.Fatalf("hidden tests of %s fail after the reference change:\n%s", task.ID, out)
			}
		})
	}
}

func replaceIn(t *testing.T, path, old, new string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), old) {
		t.Fatalf("%s: %q not found (%v)", path, old, err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, text...), 0o644); err != nil {
		t.Fatal(err)
	}
}
