package sessionzero_test

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/pkg/clock"
)

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// CA-SZ-WS-07: an existing .mestre/ with its own teto.yaml. The structure is
// made around it: the ceiling does not change, only the missing folders and
// files are created, and what is not valid is reported, not fixed.
func TestExistingMestreIsKeptAndOnlyWhatIsMissingIsCreated(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const ceiling = "version: 1\npermissions:\n  git:\n    request_push: true\n"
	teto := filepath.Join(root, ".mestre", "regras", "teto.yaml")
	write(t, teto, ceiling)
	write(t, filepath.Join(root, ".mestre", "classes", "revisor", "class.yaml"), "nome: revisor\n")
	write(t, filepath.Join(root, ".mestre", "regras", "instrucoes", "grao-mestre.md"), "Falar sempre em português.\n")
	before := stat(t, teto)

	rep := sessionzero.EnsureStructure(root)

	if got := read(t, teto); got != ceiling {
		t.Fatalf("teto.yaml = %q; want it unchanged", got)
	}
	if after := stat(t, teto); !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("teto.yaml was rewritten: %v -> %v", before.ModTime(), after.ModTime())
	}
	if got := read(t, filepath.Join(root, ".mestre", "regras", "instrucoes", "grao-mestre.md")); got != "Falar sempre em português.\n" {
		t.Fatalf("the instructions of the users = %q", got)
	}
	if got := read(t, filepath.Join(root, ".mestre", "classes", "revisor", "class.yaml")); got != "nome: revisor\n" {
		t.Fatalf("an existing class = %q", got)
	}
	want := []string{
		".mestre/agents/", ".mestre/anexos/", ".mestre/mesas/", ".mestre/modelos/", ".mestre/modelos/VERSAO",
		".mestre/regras/instrucoes/mestre-da-mesa.md", ".mestre/regras/politica-de-aprovacao.yaml", ".mestre/run/",
	}
	got := slices.Clone(rep.Created)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("created = %v; want exactly what was missing %v", got, want)
	}
	if len(rep.Invalid) != 0 {
		t.Fatalf("invalid = %v", rep.Invalid)
	}
	if v := read(t, filepath.Join(root, ".mestre", "modelos", "VERSAO")); strings.TrimSpace(v) != sessionzero.ModelVersion {
		t.Fatalf("VERSAO = %q", v)
	}
	// Run again: nothing is left to create.
	if again := sessionzero.EnsureStructure(root); len(again.Created) != 0 || len(again.Invalid) != 0 {
		t.Fatalf("second run = %+v; want nothing", again)
	}
}

func stat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

// A fresh workspace gets the defaults of the documents that own them: the
// ceiling that parses and the policy of docs/aprovacoes/arquivo-de-politica.md.
func TestStructureBornWithTheDefaults(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rep := sessionzero.EnsureStructure(root)
	if len(rep.Invalid) != 0 {
		t.Fatalf("invalid = %v", rep.Invalid)
	}
	if _, err := classes.ParseCeiling([]byte(read(t, filepath.Join(root, ".mestre", "regras", "teto.yaml")))); err != nil {
		t.Fatalf("teto.yaml: %v", err)
	}
	if got := read(t, filepath.Join(root, ".mestre", "regras", "politica-de-aprovacao.yaml")); got != sessionzero.DefaultPolicyYAML {
		t.Fatalf("politica-de-aprovacao.yaml = %q", got)
	}
	for _, d := range append([]string{"classes", "mesas"}, sessionzero.ExecutionDirs...) {
		if !dirExists(filepath.Join(root, ".mestre", d)) {
			t.Errorf(".mestre/%s was not created", d)
		}
	}
}

// What exists but is not valid is reported and left as it is.
func TestInvalidExistingFilesAreReportedAndNotTouched(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	teto := filepath.Join(root, ".mestre", "regras", "teto.yaml")
	pol := filepath.Join(root, ".mestre", "regras", "politica-de-aprovacao.yaml")
	write(t, teto, "version: 1\npermissions: [isto não é um mapa\n")
	write(t, pol, "expiracao: 24h\n")
	rep := sessionzero.EnsureStructure(root)
	var paths []string
	for _, p := range rep.Invalid {
		paths = append(paths, p.Path)
	}
	slices.Sort(paths)
	if want := []string{".mestre/regras/politica-de-aprovacao.yaml", ".mestre/regras/teto.yaml"}; !slices.Equal(paths, want) {
		t.Fatalf("invalid = %v; want %v", rep.Invalid, want)
	}
	if read(t, teto) != "version: 1\npermissions: [isto não é um mapa\n" || read(t, pol) != "expiracao: 24h\n" {
		t.Fatal("an invalid file was changed")
	}
}

// A link where .mestre/ should be is not followed: nothing is written
// through it and the problem is reported.
func TestStructureDoesNotFollowALink(t *testing.T) {
	t.Parallel()
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".mestre")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	rep := sessionzero.EnsureStructure(root)
	if len(rep.Invalid) == 0 {
		t.Fatal("a link as .mestre/ was not reported")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("the link was followed: %v", entries)
	}
}

// The default policy is the one of the document that owns the format.
func TestDefaultPolicyIsTheDocumentOne(t *testing.T) {
	t.Parallel()
	doc := read(t, filepath.Join("..", "..", "docs", "aprovacoes", "arquivo-de-politica.md"))
	_, sec, _ := strings.Cut(doc, "## Conteúdo padrão")
	m := regexp.MustCompile("(?s)```yaml\n(.*?)```").FindStringSubmatch(sec)
	if m == nil {
		t.Fatal("the document has no default content")
	}
	var fromDoc, fromCode any
	if err := yaml.Unmarshal([]byte(m[1]), &fromDoc); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte(sessionzero.DefaultPolicyYAML), &fromCode); err != nil {
		t.Fatal(err)
	}
	if !equalYAML(fromDoc, fromCode) {
		t.Fatal("DefaultPolicyYAML differs from the default content of docs/aprovacoes/arquivo-de-politica.md")
	}
}

func equalYAML(a, b any) bool {
	ab, _ := yaml.Marshal(a)
	bb, _ := yaml.Marshal(b)
	return string(ab) == string(bb)
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// --- the files of a worktree ---

type wtEnv struct {
	t    *testing.T
	ctx  context.Context
	dir  string
	repo gitx.Repo
	data *fakegit.Repo
	wt   sessionzero.Worktree
}

func newWT(t *testing.T, committed map[string]string) *wtEnv {
	t.Helper()
	g := fakegit.NewGit(clock.NewFake(time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)))
	dir := filepath.Join(t.TempDir(), "principal")
	repo, err := g.Init(t.Context(), dir, gitx.InitOptions{InitialBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	h := g.Handle(repo.Dir())
	h.Data().Commit("main", "chore: initial commit", mergeFiles(map[string]string{"README.md": "# x\n"}, committed))
	for p, body := range committed {
		write(t, filepath.Join(dir, filepath.FromSlash(p)), body)
	}
	return &wtEnv{t: t, ctx: t.Context(), dir: dir, repo: repo, data: h.Data(),
		wt: sessionzero.Worktree{Providers: providers.NewRegistry(providers.Options{NoFlush: true}), Binary: filepath.Join(t.TempDir(), "mestre")}}
}

func (w *wtEnv) ensure(excludeMestre bool) sessionzero.WorktreeReport {
	w.t.Helper()
	rep, err := w.wt.Ensure(w.ctx, sessionzero.WorktreeRequest{Dir: w.dir, Repo: w.repo, ExcludeMestre: excludeMestre})
	if err != nil {
		w.t.Fatal(err)
	}
	return rep
}

func (w *wtEnv) block() []string {
	w.t.Helper()
	b, err := w.repo.ExcludeBlock(w.ctx, sessionzero.ExcludeBlock)
	if err != nil {
		w.t.Fatal(err)
	}
	return b
}

// fingerprint is the hash and the modification date of every generated file.
func (w *wtEnv) fingerprint() map[string]string {
	w.t.Helper()
	out := map[string]string{}
	for _, rel := range []string{".claude/settings.local.json", ".agents/hooks.json", ".agents/skills/mestre-comunicacao/SKILL.md", ".claude/skills/mestre-comunicacao/SKILL.md"} {
		p := filepath.Join(w.dir, filepath.FromSlash(rel))
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(b)
		out[rel] = string(sum[:]) + stat(w.t, p).ModTime().String()
	}
	return out
}

// The files of a worktree: the skill in both folders, the hooks of Claude Code
// and Antigravity, and the block of the exclusions in the order of the
// document. Run again with nothing changed, not a byte and not a date moves.
func TestWorktreeGeneratesTheFilesAndIsIdempotent(t *testing.T) {
	t.Parallel()
	w := newWT(t, nil)
	rep := w.ensure(true)
	if len(rep.Problems) != 0 {
		t.Fatalf("problems = %v", rep.Problems)
	}
	if len(rep.Written) != 4 {
		t.Fatalf("written = %v; want the 2 skills and the 2 hook files", rep.Written)
	}
	want := []string{
		"/.mestre/agents/", "/.mestre/anexos/", "/.mestre/run/",
		"/.agents/skills/mestre-*/", "/.claude/skills/mestre-*/", "/.agents/agents/mestre-*.md",
		"/.claude/settings.local.json", "/.agents/hooks.json", "/.mestre/",
	}
	if got := w.block(); !slices.Equal(got, want) {
		t.Fatalf("exclusions = %v; want %v", got, want)
	}
	skill := read(t, filepath.Join(w.dir, ".agents", "skills", "mestre-comunicacao", "SKILL.md"))
	if skill != read(t, filepath.Join(w.dir, ".claude", "skills", "mestre-comunicacao", "SKILL.md")) {
		t.Fatal("the copy of the skill for Claude Code differs")
	}
	if !strings.HasPrefix(skill, "---\nname: mestre-comunicacao\ndescription:") || !strings.Contains(skill, "<!-- generated by Mestre, template v"+sessionzero.ModelVersion) {
		t.Fatalf("skill = %q", skill)
	}
	before := w.fingerprint()
	if again := w.ensure(true); len(again.Written) != 0 || len(again.Problems) != 0 {
		t.Fatalf("second run = %+v; want nothing written", again)
	}
	if after := w.fingerprint(); !equalMaps(before, after) {
		t.Fatalf("a file changed on the second run:\n%v\n%v", before, after)
	}
	if _, err := os.Stat(filepath.Join(w.dir, ".gitignore")); err == nil {
		t.Fatal("a .gitignore appeared")
	}
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// The answer "versioned": the block no longer excludes .mestre/ whole, and
// the lines the Mestre made for the hook file stay.
func TestWorktreeExcludeFollowsTheAnswerAboutVersioning(t *testing.T) {
	t.Parallel()
	w := newWT(t, nil)
	w.ensure(true)
	if err := w.wt.SetExcludeMestre(w.ctx, w.repo, false); err != nil {
		t.Fatal(err)
	}
	got := w.block()
	if slices.Contains(got, "/.mestre/") || !slices.Contains(got, "/.mestre/run/") || !slices.Contains(got, "/.agents/hooks.json") {
		t.Fatalf("block = %v", got)
	}
	if err := w.wt.SetExcludeMestre(w.ctx, w.repo, true); err != nil || !slices.Contains(w.block(), "/.mestre/") {
		t.Fatalf("back to local: %v, %v", w.block(), err)
	}
}

// The settings of the person are theirs: the hooks of the Mestre come after
// the person's, once, whatever the number of runs.
func TestWorktreeMergesIntoTheFileOfThePerson(t *testing.T) {
	t.Parallel()
	const person = `{
  "permissions": {"allow": ["Bash(make test)"]},
  "hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "./minha-checagem.sh"}]}]}
}`
	w := newWT(t, nil)
	cfg := filepath.Join(w.dir, ".claude", "settings.local.json")
	write(t, cfg, person)
	w.ensure(true)
	first := read(t, cfg)
	w.ensure(true)
	second := read(t, cfg)
	if first != second {
		t.Fatal("the second merge changed the file")
	}
	for _, keep := range []string{`"Bash(make test)"`, "./minha-checagem.sh"} {
		if strings.Count(second, keep) != 1 {
			t.Errorf("%s appears %d times", keep, strings.Count(second, keep))
		}
	}
	if i, j := strings.Index(second, "./minha-checagem.sh"), strings.Index(second, "mestre"); i < 0 || j < 0 {
		t.Fatalf("settings = %s", second)
	} else if k := strings.LastIndex(second, "mestre"); k < i {
		t.Fatalf("the handlers of the Mestre do not come after the person's:\n%s", second)
	}
}

// A hook file that is not valid JSON is not touched, the problem is
// reported, the other provider is generated, and the file of the person is
// not excluded from their git status.
func TestWorktreeLeavesInvalidHookFileAlone(t *testing.T) {
	t.Parallel()
	w := newWT(t, nil)
	hooks := filepath.Join(w.dir, ".agents", "hooks.json")
	write(t, hooks, "{isto não é json")
	rep := w.ensure(true)
	if read(t, hooks) != "{isto não é json" {
		t.Fatal("the invalid hook file was changed")
	}
	if len(rep.Problems) != 1 || rep.Problems[0].Path != ".agents/hooks.json" {
		t.Fatalf("problems = %v", rep.Problems)
	}
	if _, err := os.Stat(filepath.Join(w.dir, ".claude", "settings.local.json")); err != nil {
		t.Fatalf("the other provider was not generated: %v", err)
	}
	if slices.Contains(w.block(), "/.agents/hooks.json") {
		t.Fatalf("a file of the person was excluded: %v", w.block())
	}
}

// A hook file of the person, valid and not versioned yet, keeps showing in
// their git status (it is merged but not excluded); one the Mestre made is
// excluded, and stays excluded on the next runs.
func TestWorktreeExcludesTheHookFileOnlyWhenTheMestreMadeIt(t *testing.T) {
	t.Parallel()
	person := newWT(t, nil)
	write(t, filepath.Join(person.dir, ".agents", "hooks.json"), `{"meu-hook": {"PreToolUse": []}}`)
	person.ensure(true)
	person.ensure(true)
	if slices.Contains(person.block(), "/.agents/hooks.json") {
		t.Fatalf("block = %v; the file of the person must show", person.block())
	}
	if got := read(t, filepath.Join(person.dir, ".agents", "hooks.json")); !strings.Contains(got, "meu-hook") || !strings.Contains(got, `"mestre"`) {
		t.Fatalf("hooks.json = %s", got)
	}
	mine := newWT(t, nil)
	mine.ensure(true)
	mine.ensure(true)
	if !slices.Contains(mine.block(), "/.agents/hooks.json") {
		t.Fatalf("block = %v; the file the Mestre made must be excluded", mine.block())
	}
}

// A hook file the project versions is merged and told to git to ignore the
// local change (skip-worktree) instead of being excluded.
func TestWorktreeMarksAVersionedHookFileSkipWorktree(t *testing.T) {
	t.Parallel()
	w := newWT(t, map[string]string{".agents/hooks.json": `{"projeto": {"Stop": []}}`})
	w.ensure(true)
	skipped, err := w.repo.SkipWorktreePaths(w.ctx)
	if err != nil || !slices.Equal(skipped, []string{".agents/hooks.json"}) {
		t.Fatalf("skip-worktree = %v, %v", skipped, err)
	}
	if slices.Contains(w.block(), "/.agents/hooks.json") {
		t.Fatalf("block = %v", w.block())
	}
	if !strings.Contains(read(t, filepath.Join(w.dir, ".agents", "hooks.json")), "projeto") {
		t.Fatal("the hooks of the project were lost")
	}
}

// A skill folder of the Mestre's name without the marker is not generated
// over: a conflict.
func TestWorktreeDoesNotOverwriteASkillWithoutTheMarker(t *testing.T) {
	t.Parallel()
	w := newWT(t, nil)
	mine := filepath.Join(w.dir, ".agents", "skills", "mestre-comunicacao", "SKILL.md")
	write(t, mine, "feita à mão\n")
	rep := w.ensure(true)
	if read(t, mine) != "feita à mão\n" {
		t.Fatal("a skill without the marker was overwritten")
	}
	if len(rep.Problems) != 1 || rep.Problems[0].Path != ".agents/skills/mestre-comunicacao/SKILL.md" {
		t.Fatalf("problems = %v", rep.Problems)
	}
	if _, err := os.Stat(filepath.Join(w.dir, ".claude", "skills", "mestre-comunicacao", "SKILL.md")); err != nil {
		t.Fatalf("the copy for Claude Code: %v", err)
	}
}

// Antigravity blocks on a hook that fails, so its file needs the absolute
// path of the binary; without it that provider is reported and Claude Code is
// still generated.
func TestWorktreeReportsAProviderItCannotGenerate(t *testing.T) {
	t.Parallel()
	w := newWT(t, nil)
	w.wt.Binary = ""
	rep := w.ensure(true)
	if len(rep.Problems) != 1 || rep.Problems[0].Path != string(providers.Antigravity) {
		t.Fatalf("problems = %v", rep.Problems)
	}
	if _, err := os.Stat(filepath.Join(w.dir, ".claude", "settings.local.json")); err != nil {
		t.Fatalf("claude: %v", err)
	}
}

func mergeFiles(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
