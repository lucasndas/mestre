package testkit_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/testkit"
)

const modelDir = "../../testdata/projetos/go-basico"

// copyFiles lists the files of a copy (slash paths), without .git.
func copyFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// CA-TREINO-01
func TestNewProjectCopiesModelWithDefaultVariation(t *testing.T) {
	t.Parallel()
	a := testkit.NewProject(t, "go-basico")
	b := testkit.NewProject(t, "go-basico", testkit.DefaultVariation)

	want := []string{"AGENTS.md", "README.md", "go.mod", "stats/stats.go", "stats/stats_test.go"}
	for _, p := range []*testkit.Project{a, b} {
		if got := copyFiles(t, p.Dir); !slices.Equal(got, want) {
			t.Fatalf("copy %s has %v, want %v (no tasks, no hidden tests)", p.Dir, got, want)
		}
	}
	if abs, _ := filepath.Abs(modelDir); a.Dir == b.Dir || strings.HasPrefix(a.Dir, abs) {
		t.Fatalf("copies %s and %s must be separate temporary folders", a.Dir, b.Dir)
	}
	if _, err := os.Stat(filepath.Join(modelDir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("the model holds a nested git repository: %v", err)
	}
	if err := os.WriteFile(filepath.Join(a.Dir, "README.md"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(b.Dir, "README.md"), filepath.Join(modelDir, "README.md")} {
		if got, err := os.ReadFile(p); err != nil || string(got) == "changed" {
			t.Fatalf("%s changed with the other copy: %v", p, err)
		}
	}
}

// CA-TREINO-03
func TestTasksFileAndHiddenTests(t *testing.T) {
	t.Parallel()
	tasks := testkit.LoadTasks(t, "go-basico")
	t1 := tasks[0]
	if t1.ID != "T1" || t1.Prompt == "" || !slices.Equal(t1.MayChange, []string{"stats/stats.go", "stats/stats_test.go"}) ||
		t1.Verify.Hidden == "" || len(t1.Verify.Command) == 0 || t1.Heavy == nil || t1.Heavy.MaxMinutes <= 0 {
		t.Fatalf("T1 incomplete: %+v", t1)
	}

	p := testkit.NewProject(t, "go-basico")
	got := p.ApplyHiddenTests(t, "T1")
	if !slices.Equal(got, []string{"stats/max_hidden_test.go"}) {
		t.Fatalf("hidden tests applied = %v", got)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "stats", "max_hidden_test.go")); err != nil {
		t.Fatal(err)
	}
}

func TestParseTasksRejectsBadEntries(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{
		"unknown field": "tasks:\n  - id: T1\n    prompt: x\n    may_change: [a]\n    verify: {command: [go]}\n    owner: me\n",
		"repeated id":   "tasks:\n  - {id: T1, prompt: x, may_change: [a], verify: {command: [go]}}\n  - {id: T1, prompt: y, may_change: [a], verify: {command: [go]}}\n",
		"no command":    "tasks:\n  - {id: T1, prompt: x, may_change: [a], verify: {hidden: ocultos/T1}}\n",
		"bad id":        "tasks:\n  - {id: tarefa1, prompt: x, may_change: [a], verify: {command: [go]}}\n",
		"zero limits":   "tasks:\n  - {id: T1, prompt: x, may_change: [a], verify: {command: [go]}, heavy: {max_minutes: 0, max_turns: 3}}\n",
		"empty":         "tasks: []\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := testkit.ParseTasks([]byte(src)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
