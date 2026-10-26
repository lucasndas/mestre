package sessionzero_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/sessionzero"
)

// --- fakes of the session zero of the table ---

type noticeLog struct {
	mu   sync.Mutex
	list []sessionzero.Notice
}

func (n *noticeLog) Notify(_ context.Context, _ string, x sessionzero.Notice) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.list = append(n.list, x)
	return nil
}

func (n *noticeLog) kinds() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []string
	for _, x := range n.list {
		out = append(out, x.Kind)
	}
	return out
}

type classFake struct {
	files map[string]classes.Files
	err   error
}

func (c *classFake) Approved(_ context.Context, name, _ string) (*classes.Class, error) {
	if c.err != nil {
		return nil, c.err
	}
	f, ok := c.files[name]
	if !ok {
		return nil, classes.ErrNoApproved
	}
	return &classes.Class{Name: name, Files: f}, nil
}

type skillFlags struct{ read, approved bool }

func (s *skillFlags) AgentsSkillsRead(context.Context) (bool, error) { return s.read, nil }
func (s *skillFlags) ClaudeCopyOffApproved(context.Context) (bool, error) {
	return s.approved, nil
}

// tableEnv is the worktree of a table over the fake git, with a Table.
type tableEnv struct {
	*wtEnv
	table   *sessionzero.Table
	notices *noticeLog
	opts    sessionzero.TableOptions
	req     sessionzero.TableRequest
	home    string // root of the account profiles
}

func newTableEnv(t *testing.T, committed map[string]string, tweak func(*sessionzero.TableOptions)) *tableEnv {
	t.Helper()
	w := newWT(t, committed)
	e := &tableEnv{wtEnv: w, notices: &noticeLog{}, home: t.TempDir()}
	e.opts = sessionzero.TableOptions{Worktree: w.wt, Notices: e.notices}
	if tweak != nil {
		tweak(&e.opts)
	}
	e.req = sessionzero.TableRequest{Workspace: "meu-app", Table: "3-login", Dir: w.dir, Repo: w.repo, RepoRoot: filepath.Join(e.home, "espelho.git"), ExcludeMestre: true}
	e.rebuild()
	return e
}

func (e *tableEnv) rebuild() {
	e.t.Helper()
	tb, err := sessionzero.NewTable(e.opts)
	if err != nil {
		e.t.Fatal(err)
	}
	e.table = tb
}

func (e *tableEnv) prepare() sessionzero.TableReport {
	e.t.Helper()
	rep, err := e.table.Prepare(e.ctx, e.req)
	if err != nil {
		e.t.Fatal(err)
	}
	return rep
}

// profiles makes an account profile for each provider and lists them.
func (e *tableEnv) profiles() []sessionzero.Profile {
	e.t.Helper()
	var out []sessionzero.Profile
	for _, id := range []providers.ID{providers.ClaudeCode, providers.Codex, providers.Antigravity} {
		dir := filepath.Join(e.home, string(id))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, sessionzero.Profile{Provider: id, Dir: dir})
	}
	return out
}

// holds says whether a native file names the folder, in whichever way the file
// writes a path (JSON doubles the backslashes of Windows; TOML escapes them).
func holds(content, dir string) bool {
	return strings.Contains(content, dir) || strings.Contains(content, filepath.ToSlash(dir)) || strings.Contains(content, strings.ReplaceAll(dir, `\`, `\\`))
}

// trustFiles are the files of the three providers that hold the trust.
func (e *tableEnv) trustFiles() []string {
	return []string{
		filepath.Join(e.home, "claude-code", ".claude.json"),
		filepath.Join(e.home, "codex", "config.toml"),
		filepath.Join(e.home, "antigravity", ".gemini", "antigravity-cli", "settings.json"),
	}
}

// --- CA-SZ-MESA-01 ---

// CA-SZ-MESA-01
func TestPrepareAgainChangesNoFileNorDate(t *testing.T) {
	t.Parallel()
	e := newTableEnv(t, nil, func(o *sessionzero.TableOptions) {
		o.Classes = &classFake{files: map[string]classes.Files{"revisor": {{Path: "CLASS.md", Data: []byte("# revisor\n")}}}}
	})
	e.req.Classes = []string{"revisor"}
	e.req.Profiles = e.profiles()
	rep := e.prepare()
	if len(rep.Problems) != 0 || len(rep.Written) == 0 {
		t.Fatalf("first run = %+v", rep)
	}
	snapshot := func() map[string]string {
		out := e.fingerprint()
		for _, rel := range append(e.trustFiles(), filepath.Join(e.dir, ".mestre", "run", "classes", "revisor", "CLASS.md")) {
			b, err := os.ReadFile(rel)
			if err != nil {
				t.Fatalf("%s: %v", rel, err)
			}
			out[rel] = string(b) + stat(t, rel).ModTime().String()
		}
		return out
	}
	before := snapshot()
	if again := e.prepare(); len(again.Written) != 0 || len(again.Removed) != 0 || len(again.Problems) != 0 {
		t.Fatalf("second run = %+v; want nothing changed", again)
	}
	if after := snapshot(); !equalMaps(before, after) {
		t.Fatalf("a file changed on the second run:\n%v\n%v", before, after)
	}
	// A new version of the models regenerates what is generated, and only that.
	e.opts.Worktree.Skill = append(sessionzero.CommunicationSkill(), []byte("- A new line of the new version.\n")...)
	e.rebuild()
	rep = e.prepare()
	if !slices.Equal(rep.Written, []string{".agents/skills/mestre-comunicacao/SKILL.md", ".claude/skills/mestre-comunicacao/SKILL.md"}) {
		t.Fatalf("after a new version of the models, written = %v", rep.Written)
	}
}

// --- CA-SZ-MESA-02 ---

// CA-SZ-MESA-02
func TestMergeTwiceKeepsThePersonsContentAndTheMestreHandlersOnceAfterTheirs(t *testing.T) {
	t.Parallel()
	const person = `{
  "permissions": {"allow": ["Bash(make test)"], "deny": ["Read(./.env)"]},
  "hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "./minha-checagem.sh"}]}]}
}`
	e := newTableEnv(t, nil, nil)
	cfg := filepath.Join(e.dir, ".claude", "settings.local.json")
	write(t, cfg, person)
	e.prepare()
	first := read(t, cfg)
	e.prepare()
	if second := read(t, cfg); second != first {
		t.Fatalf("the second merge changed the file:\n%s\n%s", first, second)
	}
	var doc struct {
		Permissions map[string][]string `json:"permissions"`
		Hooks       map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(first), &doc); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(doc.Permissions["allow"], []string{"Bash(make test)"}) || !slices.Equal(doc.Permissions["deny"], []string{"Read(./.env)"}) {
		t.Fatalf("the permissions of the person changed: %v", doc.Permissions)
	}
	pre := doc.Hooks["PreToolUse"]
	if len(pre) != 2 || pre[0].Matcher != "Bash" || len(pre[0].Hooks) != 1 || pre[0].Hooks[0].Command != "./minha-checagem.sh" {
		t.Fatalf("PreToolUse = %+v; want the person's group first and the Mestre's after it", pre)
	}
	if n := strings.Count(first, "hook --provider claude-code --event PreToolUse"); n != 1 {
		t.Fatalf("the Mestre's PreToolUse handler appears %d times", n)
	}
	for event, groups := range doc.Hooks {
		n := 0
		for _, g := range groups {
			for _, h := range g.Hooks {
				if strings.Contains(h.Command, "--event "+event) {
					n++
				}
			}
		}
		if n != 1 {
			t.Errorf("%s has %d Mestre handlers; want 1", event, n)
		}
	}
}

// --- CA-SZ-MESA-03 ---

// CA-SZ-MESA-03: the invalid hook file is not touched, an alert is raised and
// the Antigravity start is refused by CheckStart (the call every start of a
// character makes first: internal/orchestration/characters, TestTheFilesOfTheTable…
// and TestOnlyTheProviderThatIsHeldBackIsRefusedAtTheStart).
func TestInvalidHookFileIsKeptAnAlertIsRaisedAndNoAntigravityCharacterStarts(t *testing.T) {
	t.Parallel()
	e := newTableEnv(t, nil, nil)
	hooks := filepath.Join(e.dir, ".agents", "hooks.json")
	write(t, hooks, "{isto não é json")
	before := stat(t, hooks)

	rep := e.prepare()
	if read(t, hooks) != "{isto não é json" || !stat(t, hooks).ModTime().Equal(before.ModTime()) {
		t.Fatal("the invalid hook file was touched")
	}
	if !slices.Contains(e.notices.kinds(), "provider_files") {
		t.Fatalf("notices = %v; want an alert about the provider files", e.notices.kinds())
	}
	if len(rep.Blocked[providers.Antigravity]) == 0 {
		t.Fatalf("blocked = %v; want Antigravity", rep.Blocked)
	}
	_, err := e.table.CheckStart(e.ctx, e.req, providers.Antigravity)
	var be *sessionzero.BlockedError
	if !errors.Is(err, sessionzero.ErrBlocked) || !errors.As(err, &be) || be.Provider != providers.Antigravity || !strings.Contains(err.Error(), ".agents/hooks.json") {
		t.Fatalf("CheckStart(antigravity) = %v; want it blocked for the hook file", err)
	}
	// The other providers go on, and the same problem is not told twice.
	for _, id := range []providers.ID{providers.ClaudeCode, providers.Codex} {
		if _, err := e.table.CheckStart(e.ctx, e.req, id); err != nil {
			t.Fatalf("CheckStart(%s) = %v; want it free", id, err)
		}
	}
	if n := len(e.notices.kinds()); n != 1 {
		t.Fatalf("%d notices; want the problem told once", n)
	}
	// Once the person fixes the file, the provider starts.
	write(t, hooks, `{}`)
	if _, err := e.table.CheckStart(e.ctx, e.req, providers.Antigravity); err != nil {
		t.Fatalf("after the fix: %v", err)
	}
}

// A skill the person made, in the folder the other providers share, holds
// back Codex and Antigravity, not Claude Code.
func TestConflictingSkillBlocksTheProvidersThatReadItsFolder(t *testing.T) {
	t.Parallel()
	e := newTableEnv(t, nil, nil)
	write(t, filepath.Join(e.dir, ".agents", "skills", "mestre-comunicacao", "SKILL.md"), "feita à mão\n")
	e.prepare()
	for id, blocked := range map[providers.ID]bool{providers.Codex: true, providers.Antigravity: true, providers.ClaudeCode: false} {
		_, err := e.table.CheckStart(e.ctx, e.req, id)
		if blocked != errors.Is(err, sessionzero.ErrBlocked) {
			t.Errorf("CheckStart(%s) = %v; blocked should be %v", id, err, blocked)
		}
	}
}

// --- CA-SZ-MESA-04 and the markers of the block ---

// CA-SZ-MESA-04
func TestExcludeBlockKeepsTheLinesOfThePersonAndIsOneBlock(t *testing.T) {
	t.Parallel()
	e := newTableEnv(t, nil, nil)
	const person = "# minhas linhas\n*.log\n/build/\n\n# depois\n!/build/keep\n"
	file := person
	// The block is written, then updated by each answer about versioning, over
	// the file of the person, the way the repository does it (gitx).
	for _, only := range []bool{true, false, true, true} {
		e.req.ExcludeMestre = only
		e.prepare()
		file = gitx.ApplyExcludeBlock(file, sessionzero.ExcludeBlock, e.block())
		if !strings.HasPrefix(file, person) {
			t.Fatalf("the lines of the person changed:\n%s", file)
		}
		if n := strings.Count(file, "# >>> mestre:"); n != 1 || strings.Count(file, "# <<< mestre:") != 1 {
			t.Fatalf("%d blocks of the Mestre in:\n%s", n, file)
		}
		if got, _ := gitx.ParseExcludeBlock(file, sessionzero.ExcludeBlock); !slices.Equal(got, e.block()) || slices.Contains(got, "/.mestre/") == !only {
			t.Fatalf("block of the file = %v, the engine's = %v (only local: %v)", got, e.block(), only)
		}
	}
}

// The block in docs/sessao-zero/mesa.md is the one the code writes, markers
// included (owner decision: the markers are the ones of gitx).
func TestExcludeBlockIsTheOneOfTheDocument(t *testing.T) {
	t.Parallel()
	doc := read(t, filepath.Join("..", "..", "docs", "sessao-zero", "mesa.md"))
	_, sec, _ := strings.Cut(doc, "### `.git/info/exclude`")
	m := regexp.MustCompile("(?s)```\n(.*?)```").FindStringSubmatch(sec)
	if m == nil {
		t.Fatal("the document has no block")
	}
	e := newTableEnv(t, nil, nil)
	e.prepare()
	if got := gitx.ApplyExcludeBlock("", sessionzero.ExcludeBlock, e.block()); got != m[1] {
		t.Fatalf("the block of the code:\n%s\nthe block of the document:\n%s", got, m[1])
	}
}

// --- CA-SZ-MESA-08: the line of the hook ---

// hookLines are the commands of the PreToolUse of Claude Code and of
// Antigravity in a generated worktree.
func hookLines(t *testing.T, dir string) (claude, agy string) { //nolint:unused // used by the full suite
	t.Helper()
	var c struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(read(t, filepath.Join(dir, ".claude", "settings.local.json"))), &c); err != nil {
		t.Fatal(err)
	}
	var a struct {
		Mestre map[string]json.RawMessage `json:"mestre"`
	}
	if err := json.Unmarshal([]byte(read(t, filepath.Join(dir, ".agents", "hooks.json"))), &a); err != nil {
		t.Fatal(err)
	}
	var groups []struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(a.Mestre["PreToolUse"], &groups); err != nil {
		t.Fatal(err)
	}
	return c.Hooks["PreToolUse"][0].Hooks[0].Command, groups[0].Hooks[0].Command
}

// Part of CA-SZ-MESA-08 (the full suite runs the lines in a real shell, which the
// fast suite cannot start): the line of every hook file, for each provider and
// system, tests MESTRE_AGENT_ID before anything else and answers with the
// neutral reply of the event otherwise; the call comes only after the test.
func TestHookLinesTestTheAgentIDBeforeCallingTheMestre(t *testing.T) {
	t.Parallel()
	reg := providers.NewRegistry(providers.Options{NoFlush: true})
	for _, c := range []struct {
		goos, binary string
	}{{"linux", "/opt/mestre/bin/mestre"}, {"windows", `C:\mestre\bin\mestre.exe`}} {
		for _, id := range []providers.ID{providers.ClaudeCode, providers.Antigravity} {
			ad, err := reg.For(id)
			if err != nil {
				t.Fatal(err)
			}
			files, err := ad.HookFiles(providers.HookFilesRequest{OS: c.goos, Root: "/wt", Binary: c.binary})
			if err != nil || len(files) != 1 {
				t.Fatalf("%s %s: %v, %v", id, c.goos, files, err)
			}
			doc, err := files[0].Merge(nil)
			if err != nil {
				t.Fatal(err)
			}
			lines := commandsOf(t, doc)
			if len(lines) == 0 {
				t.Fatalf("%s %s: no command in %s", id, c.goos, doc)
			}
			for _, line := range lines {
				guard := strings.Index(line, "MESTRE_AGENT_ID")
				call := strings.Index(line, "hook --provider")
				if guard < 0 || call < 0 || guard > call {
					t.Errorf("%s %s: the line calls the mestre before testing MESTRE_AGENT_ID: %s", id, c.goos, line)
				}
				if id == providers.Antigravity && !strings.Contains(line, "echo ") && !strings.Contains(line, "printf ") {
					t.Errorf("%s %s: the line has no neutral answer: %s", id, c.goos, line)
				}
			}
		}
	}
}

// commandsOf lists the "command" values of a hook file, whatever its shape.
func commandsOf(t *testing.T, doc []byte) []string {
	t.Helper()
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		t.Fatal(err)
	}
	var out []string
	var walk func(any)
	walk = func(x any) {
		switch x := x.(type) {
		case map[string]any:
			for k, y := range x {
				if c, ok := y.(string); ok && k == "command" {
					out = append(out, c)
				} else {
					walk(y)
				}
			}
		case []any:
			for _, y := range x {
				walk(y)
			}
		}
	}
	walk(v)
	slices.Sort(out)
	return out
}

// --- CA-SZ-MESA-09: the copy of the skill for Claude Code ---

// CA-SZ-MESA-09
func TestSkillCopyForClaudeStaysUntilAnOwnerApprovesAndThenIsGoneForGood(t *testing.T) {
	t.Parallel()
	flags := &skillFlags{read: true}
	e := newTableEnv(t, nil, func(o *sessionzero.TableOptions) { o.Conformance, o.CopyApproval = flags, flags })
	copyPath := filepath.Join(e.dir, ".claude", "skills", "mestre-comunicacao", "SKILL.md")
	sharedPath := filepath.Join(e.dir, ".agents", "skills", "mestre-comunicacao", "SKILL.md")

	// The conformance says Claude Code reads .agents/skills/, nobody approved:
	// the copy is generated, and generated again if it goes missing.
	e.prepare()
	if read(t, copyPath) != read(t, sharedPath) {
		t.Fatal("the copy for Claude Code was not generated without the approval")
	}
	if err := os.Remove(copyPath); err != nil {
		t.Fatal(err)
	}
	e.prepare()
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("the copy did not come back: %v", err)
	}

	// Approved but the conformance did not find that it reads: it stays too.
	flags.read, flags.approved = false, true
	e.prepare()
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("the copy was removed without the conformance finding: %v", err)
	}

	// Both: the copy is removed, the shared skill stays, and the copy does not come back.
	flags.read = true
	rep := e.prepare()
	if _, err := os.Stat(copyPath); !errors.Is(err, os.ErrNotExist) || !slices.Contains(rep.Removed, ".claude/skills/mestre-comunicacao/SKILL.md") {
		t.Fatalf("the copy is still there after the approval: %v, removed %v", err, rep.Removed)
	}
	if _, err := os.Stat(sharedPath); err != nil {
		t.Fatalf("the shared skill is gone: %v", err)
	}
	for range 2 {
		if again := e.prepare(); len(again.Written) != 0 || len(again.Removed) != 0 {
			t.Fatalf("a later run = %+v", again)
		}
	}
	if _, err := os.Stat(copyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the copy came back: %v", err)
	}
}

// The copy turned off is only the one the Mestre generated: a skill of the
// person with the same name is never removed.
func TestSkillCopyOffNeverRemovesAFileWithoutTheMarker(t *testing.T) {
	t.Parallel()
	flags := &skillFlags{read: true, approved: true}
	e := newTableEnv(t, nil, func(o *sessionzero.TableOptions) { o.Conformance, o.CopyApproval = flags, flags })
	mine := filepath.Join(e.dir, ".claude", "skills", "mestre-comunicacao", "SKILL.md")
	write(t, mine, "feita à mão\n")
	rep := e.prepare()
	if read(t, mine) != "feita à mão\n" || len(rep.Problems) == 0 {
		t.Fatalf("a skill of the person was removed or not reported: %+v", rep)
	}
}

// --- copies of the classes and of the attachments ---

func TestClassesAreCopiedToTheRunFolderAndStaleFilesGo(t *testing.T) {
	t.Parallel()
	pkg := classes.Files{
		{Path: "CLASS.md", Data: []byte("# revisor\n")},
		{Path: "hooks/hooks.yaml", Data: []byte("pre_tool: []\n")},
		{Path: "hooks/checar.sh", Data: []byte("#!/bin/sh\nexit 0\n")},
	}
	src := &classFake{files: map[string]classes.Files{"revisor": pkg}}
	e := newTableEnv(t, nil, func(o *sessionzero.TableOptions) { o.Classes = src })
	e.req.Classes = []string{"revisor", "ausente"}
	rep := e.prepare()
	root := filepath.Join(e.dir, ".mestre", "run", "classes", "revisor")
	for _, f := range pkg {
		if got := read(t, filepath.Join(root, filepath.FromSlash(f.Path))); got != string(f.Data) {
			t.Fatalf("%s = %q", f.Path, got)
		}
	}
	if runtime.GOOS != "windows" {
		if fi := stat(t, filepath.Join(root, "hooks", "checar.sh")); fi.Mode().Perm()&0o100 == 0 {
			t.Fatalf("the script of the hook is not executable: %v", fi.Mode())
		}
	}
	if len(rep.Problems) != 1 || !strings.Contains(rep.Problems[0].Path, "ausente") {
		t.Fatalf("problems = %v; want the class that was never approved", rep.Problems)
	}

	// A new version drops a script: the copy follows, and a file the class
	// never had is not an agent's to leave there.
	src.files["revisor"] = pkg[:2]
	write(t, filepath.Join(root, "hooks", "plantado.sh"), "exit 0\n")
	rep = e.prepare()
	for _, gone := range []string{"hooks/checar.sh", "hooks/plantado.sh"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(gone))); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s is still there: %v", gone, err)
		}
	}
	if !slices.Contains(rep.Removed, ".mestre/run/classes/revisor/hooks/checar.sh") {
		t.Fatalf("removed = %v", rep.Removed)
	}
}

func TestAClassNameCannotLeaveTheFolder(t *testing.T) {
	t.Parallel()
	src := &classFake{files: map[string]classes.Files{"../fora": {{Path: "CLASS.md", Data: []byte("x")}}}}
	e := newTableEnv(t, nil, func(o *sessionzero.TableOptions) { o.Classes = src })
	e.req.Classes = []string{"../fora"}
	if rep := e.prepare(); len(rep.Problems) != 1 {
		t.Fatalf("problems = %v", rep.Problems)
	}
	if _, err := os.Stat(filepath.Join(e.dir, ".mestre", "run", "fora")); err == nil {
		t.Fatal("a class was copied outside its folder")
	}
}

type attachFake struct{ files []sessionzero.File }

func (a *attachFake) For(context.Context, string, string) ([]sessionzero.File, error) {
	return a.files, nil
}

func TestAttachmentsAreCopiedAndKept(t *testing.T) {
	t.Parallel()
	src := &attachFake{files: []sessionzero.File{{Path: "2026-10-15/print.png", Data: []byte{1, 2, 3}}, {Path: "../fuga.txt", Data: []byte("x")}}}
	e := newTableEnv(t, nil, func(o *sessionzero.TableOptions) { o.Attachments = src })
	rep := e.prepare()
	if got := read(t, filepath.Join(e.dir, ".mestre", "anexos", "2026-10-15", "print.png")); got != "\x01\x02\x03" {
		t.Fatalf("attachment = %q", got)
	}
	if len(rep.Problems) != 1 || !strings.Contains(rep.Problems[0].Path, "fuga.txt") {
		t.Fatalf("problems = %v; want the path that leaves the folder", rep.Problems)
	}
	if _, err := os.Stat(filepath.Join(e.dir, ".mestre", "fuga.txt")); err == nil {
		t.Fatal("an attachment left its folder")
	}
	src.files = nil // an attachment already copied is not taken away
	e.prepare()
	if _, err := os.Stat(filepath.Join(e.dir, ".mestre", "anexos", "2026-10-15", "print.png")); err != nil {
		t.Fatalf("a copied attachment was removed: %v", err)
	}
}

func TestThePreparationOfTheMainCloneCopiesNoClassNorAttachment(t *testing.T) {
	t.Parallel()
	cls := &classFake{files: map[string]classes.Files{"revisor": {{Path: "CLASS.md", Data: []byte("# revisor\n")}}}}
	att := &attachFake{files: []sessionzero.File{{Path: "2026-10-15/print.png", Data: []byte{1}}}}
	e := newTableEnv(t, nil, func(o *sessionzero.TableOptions) { o.Classes, o.Attachments = cls, att })
	e.req.Table, e.req.Classes = "", []string{"revisor"}
	if rep := e.prepare(); len(rep.Problems) != 0 {
		t.Fatalf("problems = %v", rep.Problems)
	}
	for _, dir := range []string{"anexos", "run"} {
		if _, err := os.Stat(filepath.Join(e.dir, ".mestre", dir)); err == nil {
			t.Errorf(".mestre/%s was made in the main clone", dir)
		}
	}
	// the same preparation for a table copies them
	e.req.Table = "3-login"
	e.prepare()
	if _, err := os.Stat(filepath.Join(e.dir, ".mestre", "anexos", "2026-10-15", "print.png")); err != nil {
		t.Errorf("a table gets its attachments: %v", err)
	}
}

// --- trust ---

func TestTrustIsWrittenInEachProfileAndTakenOutOnRelease(t *testing.T) {
	t.Parallel()
	e := newTableEnv(t, nil, nil)
	e.req.Profiles = e.profiles()
	rep := e.prepare()
	if len(rep.Problems) != 0 {
		t.Fatalf("problems = %v", rep.Problems)
	}
	for _, f := range e.trustFiles() {
		if !holds(read(t, f), e.dir) {
			t.Fatalf("%s does not trust %s:\n%s", f, e.dir, read(t, f))
		}
	}
	// The provider rewrote the file and the entry is gone: the next run writes it again.
	claudeJSON := e.trustFiles()[0]
	write(t, claudeJSON, `{}`)
	e.prepare()
	if !holds(read(t, claudeJSON), e.dir) {
		t.Fatalf("the entry that vanished was not written again:\n%s", read(t, claudeJSON))
	}
	if err := e.table.Release(e.req); err != nil {
		t.Fatal(err)
	}
	for _, f := range e.trustFiles() {
		if holds(read(t, f), e.dir) {
			t.Fatalf("%s still trusts the worktree:\n%s", f, read(t, f))
		}
	}
}

// The trust goes out of every profile it was written in, also the one whose
// account is no longer in the list when the table ends.
func TestReleaseTakesTheTrustOutOfAProfileThatWasRemoved(t *testing.T) {
	t.Parallel()
	e := newTableEnv(t, nil, nil)
	all := e.profiles()
	e.req.Profiles = all
	e.prepare()
	files := e.trustFiles()
	rel := e.req
	rel.Profiles = all[:1] // the other accounts were removed meanwhile
	if err := e.table.Release(rel); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if holds(read(t, f), e.dir) {
			t.Fatalf("%s still trusts the worktree:\n%s", f, read(t, f))
		}
	}
}

// A profile that cannot be written holds back only its provider.
func TestTrustFailureBlocksOnlyThatProvider(t *testing.T) {
	t.Parallel()
	e := newTableEnv(t, nil, nil)
	ps := e.profiles()
	ps[1].Dir = filepath.Join(e.home, "nao-existe") // Codex
	e.req.Profiles = ps
	e.prepare()
	_, err := e.table.CheckStart(e.ctx, e.req, providers.Codex)
	if !errors.Is(err, sessionzero.ErrBlocked) {
		t.Fatalf("CheckStart(codex) = %v; want it blocked", err)
	}
	if _, err := e.table.CheckStart(e.ctx, e.req, providers.ClaudeCode); err != nil {
		t.Fatalf("CheckStart(claude-code) = %v", err)
	}
}

// --- the update of the branch ---

// A hook file the project versions leaves the worktree as the project has it
// while the branch is updated, with skip-worktree off, and comes back merged,
// with the flag, over what the project has afterwards. CA-SZ-MESA-06 (full)
// does it with the real git.
func TestSyncGivesTheVersionedFileBackAndMergesAgain(t *testing.T) {
	t.Parallel()
	const project = `{"projeto": {"Stop": []}}`
	e := newTableEnv(t, map[string]string{".agents/hooks.json": project}, nil)
	hooks := filepath.Join(e.dir, ".agents", "hooks.json")
	e.prepare()
	if got := read(t, hooks); !strings.Contains(got, `"mestre"`) || !strings.Contains(got, "projeto") {
		t.Fatalf("hooks.json = %s", got)
	}
	skipped := func() []string {
		s, err := e.repo.SkipWorktreePaths(e.ctx)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if !slices.Equal(skipped(), []string{".agents/hooks.json"}) {
		t.Fatalf("skip-worktree = %v", skipped())
	}

	var during string
	var duringSkipped []string
	err := e.table.Sync(e.ctx, e.req, func(context.Context) error {
		during, duringSkipped = read(t, hooks), skipped()
		// The update brings a new version of the file from the project.
		write(t, hooks, `{"projeto": {"Stop": []}, "novo": {"PreToolUse": []}}`)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if during != project || len(duringSkipped) != 0 {
		t.Fatalf("during the update: hooks.json %q, skip-worktree %v; want the project's file and no flag", during, duringSkipped)
	}
	after := read(t, hooks)
	if !strings.Contains(after, `"mestre"`) || !strings.Contains(after, "projeto") || !strings.Contains(after, "novo") {
		t.Fatalf("after the update: %s; want the project's new version with the Mestre's part again", after)
	}
	if !slices.Equal(skipped(), []string{".agents/hooks.json"}) {
		t.Fatalf("skip-worktree after = %v", skipped())
	}

	// When the update fails the Mestre's part is still put back, and the error comes out.
	boom := errors.New("rebase stopped")
	err = e.table.Sync(e.ctx, e.req, func(context.Context) error { return boom })
	if !errors.Is(err, boom) || !strings.Contains(read(t, hooks), `"mestre"`) || len(skipped()) != 1 {
		t.Fatalf("failed update: %v, hooks.json %s, skip-worktree %v", err, read(t, hooks), skipped())
	}
}

// A file the project does not version is not touched by the update.
func TestSyncLeavesUnversionedFilesAlone(t *testing.T) {
	t.Parallel()
	e := newTableEnv(t, nil, nil)
	e.prepare()
	before := e.fingerprint()
	var during map[string]string
	if err := e.table.Sync(e.ctx, e.req, func(context.Context) error { during = e.fingerprint(); return nil }); err != nil {
		t.Fatal(err)
	}
	if !equalMaps(before, during) || !equalMaps(before, e.fingerprint()) {
		t.Fatal("the update touched files that are not versioned")
	}
}
