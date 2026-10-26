package testkit

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/mestre-ia/mestre/internal/testkit/fakegit"
)

// Training projects (docs/qualidade/projetos-de-treino.md). The models live
// in testdata/projetos/ at the repository root, which the go tool ignores.
const (
	// DefaultVariation is applied to every copy.
	DefaultVariation = "com-agents-md"
	// ProjectBranch, ProjectCommitMessage, ProjectAuthorName,
	// ProjectAuthorEmail and ProjectCommitDate make the initial commit of a
	// copy, fake or real, the same on every run.
	ProjectBranch        = "main"
	ProjectCommitMessage = "chore: initial commit"
	ProjectAuthorName    = "Mestre Treino"
	ProjectAuthorEmail   = "treino@example.invalid"
	ProjectCommitDate    = "2026-01-01T00:00:00Z"

	variationsDir = "variacoes"
	checksDir     = "conferencia"
	tasksFile     = "tarefas.yaml"
)

// Project is one copy of a training project in t.TempDir().
type Project struct {
	Name string
	Dir  string
	// Head is the initial commit: of Git in the fast suite, of the real
	// repository in Dir in the full and heavy suites.
	Head string
	// Git is the fake git holding the copy in the fast suite; nil when Dir
	// is a real repository (build tag full).
	Git *fakegit.Repo
}

// NewProject copies the model name plus the default variation and the
// given variations (overlays, applied in order) to a new folder under
// t.TempDir(), and makes the initial commit: in the fake git in the fast
// suite, with the real git binary under the build tag full.
func NewProject(t testing.TB, name string, variations ...string) *Project {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	files, err := copyProject(projectsRoot(), name, variations, dir)
	if err != nil {
		t.Fatal(err)
	}
	p := &Project{Name: name, Dir: dir}
	initRepo(t, p, files)
	return p
}

// ApplyHiddenTests copies the hidden tests of task into the copy, for the
// check after the agent finished, and returns their paths.
func (p *Project) ApplyHiddenTests(t testing.TB, task string) []string {
	t.Helper()
	tasks := LoadTasks(t, p.Name)
	i := slices.IndexFunc(tasks, func(k Task) bool { return k.ID == task })
	if i < 0 || tasks[i].Verify.Hidden == "" {
		t.Fatalf("project %s: task %s has no hidden tests", p.Name, task)
	}
	src := filepath.Join(projectsRoot(), checksDir, p.Name, filepath.FromSlash(tasks[i].Verify.Hidden))
	files, err := overlay(src, p.Dir, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	return slices.Sorted(maps.Keys(files))
}

// Task is one entry of tarefas.yaml.
type Task struct {
	ID        string   `yaml:"id"`
	Prompt    string   `yaml:"prompt"`
	MayChange []string `yaml:"may_change"`
	Verify    Verify   `yaml:"verify"`
	Heavy     *Limits  `yaml:"heavy"` // only tasks run with real providers
}

// Verify says how the result of a task is checked.
type Verify struct {
	Hidden  string   `yaml:"hidden"` // folder of hidden tests, relative to conferencia/<project>/
	Command []string `yaml:"command"`
}

// Limits caps a task in the heavy suite.
type Limits struct {
	MaxMinutes int `yaml:"max_minutes"`
	MaxTurns   int `yaml:"max_turns"`
}

var taskIDRe = regexp.MustCompile(`^T[0-9]+$`)

// ParseTasks reads and validates tarefas.yaml; unknown fields are errors.
func ParseTasks(data []byte) ([]Task, error) {
	var doc struct {
		Tasks []Task `yaml:"tasks"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%s: %w", tasksFile, err)
	}
	if len(doc.Tasks) == 0 {
		return nil, fmt.Errorf("%s: no tasks", tasksFile)
	}
	var errs []error
	seen := map[string]bool{}
	for _, k := range doc.Tasks {
		switch {
		case !taskIDRe.MatchString(k.ID):
			errs = append(errs, fmt.Errorf("%s: id %q: want T<n>", tasksFile, k.ID))
		case seen[k.ID]:
			errs = append(errs, fmt.Errorf("%s: id %s repeated", tasksFile, k.ID))
		}
		seen[k.ID] = true
		if k.Prompt == "" || len(k.MayChange) == 0 || len(k.Verify.Command) == 0 {
			errs = append(errs, fmt.Errorf("%s: %s: prompt, may_change and verify.command are required", tasksFile, k.ID))
		}
		if k.Heavy != nil && (k.Heavy.MaxMinutes <= 0 || k.Heavy.MaxTurns <= 0) {
			errs = append(errs, fmt.Errorf("%s: %s: heavy needs max_minutes and max_turns above zero", tasksFile, k.ID))
		}
	}
	return doc.Tasks, errors.Join(errs...)
}

// LoadTasks reads conferencia/<name>/tarefas.yaml and checks that every
// hidden test folder exists.
func LoadTasks(t testing.TB, name string) []Task {
	t.Helper()
	base := filepath.Join(projectsRoot(), checksDir, name)
	data, err := os.ReadFile(filepath.Join(base, tasksFile))
	if err != nil {
		t.Fatalf("project %s: %v", name, err)
	}
	tasks, err := ParseTasks(data)
	if err != nil {
		t.Fatalf("project %s: %v", name, err)
	}
	for _, k := range tasks {
		if k.Verify.Hidden == "" {
			continue
		}
		if fi, err := os.Stat(filepath.Join(base, filepath.FromSlash(k.Verify.Hidden))); err != nil || !fi.IsDir() {
			t.Fatalf("project %s: %s: hidden tests %s: not a folder", name, k.ID, k.Verify.Hidden)
		}
	}
	return tasks
}

// projectsRoot is testdata/projetos at the repository root, found from this
// source file so it works from any package's tests.
func projectsRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "projetos")
}

// copyProject writes the model and its variations to dst and returns every
// file (slash path -> content).
func copyProject(root, name string, variations []string, dst string) (map[string]string, error) {
	model := filepath.Join(root, name)
	if fi, err := os.Stat(filepath.Join(model, "README.md")); err != nil || fi.IsDir() || !validName(name) {
		return nil, fmt.Errorf("unknown training project %q", name)
	}
	all := []string{DefaultVariation}
	for _, v := range variations {
		if !slices.Contains(all, v) {
			all = append(all, v)
		}
	}
	for _, v := range all {
		if fi, err := os.Stat(filepath.Join(root, variationsDir, v)); err != nil || !fi.IsDir() || !validName(v) {
			return nil, fmt.Errorf("unknown variation %q (known: %v)", v, knownVariations(root))
		}
	}
	files := map[string]string{}
	if _, err := overlay(model, dst, files); err != nil {
		return nil, err
	}
	for _, v := range all {
		if _, err := overlay(filepath.Join(root, variationsDir, v), dst, files); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func validName(s string) bool { return s != "" && filepath.Base(s) == s && s != "." && s != ".." }

func knownVariations(root string) []string {
	entries, _ := os.ReadDir(filepath.Join(root, variationsDir))
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// overlay copies the tree src onto dst, overwriting, and records each file
// in files. A .git entry in a model is an error (no nested repositories).
func overlay(src, dst string, files map[string]string) (map[string]string, error) {
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".git" {
			return fmt.Errorf("%s: nested git repository in a training project", p)
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(out, b, 0o644); err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("copy %s: %w", src, err)
	}
	return files, nil
}
