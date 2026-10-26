package sessionzero

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/safefile"
)

// ErrBlocked is a provider that cannot start characters in a worktree because
// a file it needs was not generated (docs/sessao-zero/mesa.md#regras-e-erros).
var ErrBlocked = errors.New("the provider cannot start characters in this worktree")

// BlockedError says which provider is blocked and why.
type BlockedError struct {
	Provider providers.ID
	Problems []Problem
}

func (e *BlockedError) Error() string {
	var why []string
	for _, p := range e.Problems {
		why = append(why, p.String())
	}
	return fmt.Sprintf("%s: %v: %s", e.Provider, ErrBlocked, strings.Join(why, "; "))
}

func (e *BlockedError) Unwrap() error { return ErrBlocked }

// ClassSource gives the approved version of a class (classes.Library).
type ClassSource interface {
	Approved(ctx context.Context, name, table string) (*classes.Class, error)
}

// File is a file copied into the worktree, relative to its folder.
type File struct {
	Path string
	Data []byte
}

// AttachmentSource gives the attachments forwarded to a table of a workspace
// (docs/telegram/anexos-e-transcricao.md), relative to .mestre/anexos/.
type AttachmentSource interface {
	For(ctx context.Context, workspace, table string) ([]File, error)
}

// SkillConformance says whether the conformance test found that Claude Code
// already reads .agents/skills/ (conformance.Flags.AgentsSkillsRead).
type SkillConformance interface {
	AgentsSkillsRead(ctx context.Context) (bool, error)
}

// SkillCopyApproval says whether an Owner or an Administrator approved
// turning off the managed copy in .claude/skills/ (checklist ALL-13,
// milestone 7 offers it and the approval flow of milestone 5 records it).
type SkillCopyApproval interface {
	ClaudeCopyOffApproved(ctx context.Context) (bool, error)
}

// Notifier tells the users of the workspace (Users).
type Notifier interface {
	Notify(ctx context.Context, workspace string, n Notice) error
}

// Profile is an account profile of the machine that runs characters in the
// worktree.
type Profile struct {
	Provider providers.ID
	// Dir is the folder the adapter takes as the profile: CLAUDE_CONFIG_DIR,
	// CODEX_HOME, or the HOME of Antigravity.
	Dir string
}

// TableOptions are the pieces the session zero of a table needs.
type TableOptions struct {
	Worktree Worktree
	// Classes and Attachments are optional: without them nothing is copied.
	Classes     ClassSource
	Attachments AttachmentSource
	// Conformance and CopyApproval decide together whether the copy of the
	// skill for Claude Code is still generated; without either the copy stays.
	Conformance  SkillConformance
	CopyApproval SkillCopyApproval
	// Notices tells the users about the files that were not generated; nil
	// only records them in the report.
	Notices Notifier
}

// Table is the session zero of a table: what the engine makes in each
// worktree of a table, on the Citadel and on each Tower, and checks before
// each character (docs/sessao-zero/mesa.md).
type Table struct {
	o    TableOptions
	mu   sync.Mutex
	told map[string]string // worktree -> the text of the last notice
	// trusted are the profiles that got the trust of a worktree, kept so
	// that Release also reaches a profile the account list no longer has.
	trusted map[string][]Profile
}

// NewTable returns the session zero of the tables.
func NewTable(o TableOptions) (*Table, error) {
	if o.Worktree.Providers == nil {
		return nil, errors.New("sessionzero: the table needs the provider adapters")
	}
	return &Table{o: o, told: map[string]string{}, trusted: map[string][]Profile{}}, nil
}

// TableRequest says what to prepare.
type TableRequest struct {
	Workspace string
	// Table is the id of the table, for its classes and attachments.
	Table string
	// Dir is the worktree, an absolute path; Repo is the worktree opened by
	// its record (gitx.OpenWorktree).
	Dir  string
	Repo gitx.Repo
	// RepoRoot is the root of the repository the worktree came from (the
	// mirror of the table).
	RepoRoot string
	// ExcludeMestre keeps /.mestre/ whole out of git ("only local").
	ExcludeMestre bool
	// Classes are the names of the classes used in the worktree.
	Classes []string
	// Profiles are the account profiles of this machine that will run
	// characters in the worktree.
	Profiles []Profile
}

// TableReport is what Prepare did.
type TableReport struct {
	// Written are the files made or changed, Removed those taken away,
	// relative to the worktree.
	Written, Removed []string
	// Problems are the files not generated, with the reason.
	Problems []Problem
	// Blocked are the providers that cannot start characters here, with the
	// problems that block each; the others are not held back.
	Blocked map[providers.ID][]Problem
	// Trusted are the folders the profiles now trust.
	Trusted []string
}

// Prepare makes everything a table's worktree needs, and makes it again
// without rewriting what is right: the files of the providers, the exclusions,
// the copies of the classes and of the attachments, and the trust of the
// folder in the account profiles. It runs when the table opens (Citadel),
// when a Tower gets the first character of the table, before each
// character, and after a new version of the models.
func (t *Table) Prepare(ctx context.Context, req TableRequest) (TableReport, error) {
	rep := TableReport{Blocked: map[providers.ID][]Problem{}}
	if req.Dir == "" || !filepath.IsAbs(req.Dir) || req.Repo == nil {
		return rep, errors.New("sessionzero: a table needs an absolute worktree and its repository")
	}
	off, err := t.copyOff(ctx)
	if err != nil {
		return rep, err
	}
	wr, err := t.o.Worktree.Ensure(ctx, WorktreeRequest{Dir: req.Dir, Repo: req.Repo, ExcludeMestre: req.ExcludeMestre, NoClaudeSkillCopy: off})
	rep.Written, rep.Removed, rep.Problems = wr.Written, wr.Removed, wr.Problems
	if err != nil {
		return rep, err
	}
	for _, p := range wr.Problems {
		for _, id := range affected(p) {
			rep.Blocked[id] = append(rep.Blocked[id], p)
		}
	}
	// The main clone (the table "") has no classes of a table and no attachments:
	// they are the tables'. Asking the sources for the table "" would copy what
	// belongs to no table into the clone of the project.
	if req.Table != "" {
		t.copyClasses(ctx, req, &rep)
		t.copyAttachments(ctx, req, &rep)
	}
	t.trust(req, &rep)
	t.tell(ctx, req, rep)
	return rep, nil
}

// CheckStart is what runs before each character: Prepare (it verifies and
// corrects, it does not rewrite what is right) and the refusal, as a
// BlockedError, when the files of the character's provider were not
// generated. The other providers of the worktree are not held back.
func (t *Table) CheckStart(ctx context.Context, req TableRequest, provider providers.ID) (TableReport, error) {
	rep, err := t.Prepare(ctx, req)
	if err != nil {
		return rep, err
	}
	if ps := rep.Blocked[provider]; len(ps) > 0 {
		return rep, &BlockedError{Provider: provider, Problems: ps}
	}
	return rep, nil
}

// Release takes the trust the engine gave the worktree out of the account
// profiles, when the table ends and the worktree is removed. It tries every
// profile and returns what failed.
func (t *Table) Release(req TableRequest) error {
	var errs []error
	// Every profile the table was trusted in goes, also the ones the list of
	// accounts no longer has (when the path is known).
	t.mu.Lock()
	profiles := unionProfiles(t.trusted[req.Dir], req.Profiles)
	delete(t.trusted, req.Dir)
	t.mu.Unlock()
	for _, p := range profiles {
		ad, err := t.o.Worktree.Providers.For(p.Provider)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		rm, ok := ad.(providers.TrustRemover)
		if !ok {
			continue
		}
		if _, err := rm.Untrust(providers.PreTrustRequest{OS: t.o.Worktree.OS, Profile: p.Dir, Worktree: req.Dir, RepoRoot: req.RepoRoot}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.Provider, err))
		}
	}
	t.mu.Lock()
	delete(t.told, req.Dir)
	t.mu.Unlock()
	return errors.Join(errs...)
}

// unionProfiles is a followed by the profiles of b that a does not have.
func unionProfiles(a, b []Profile) []Profile {
	out := slices.Clone(a)
	for _, p := range b {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// copyOff is whether the copy of the skill for Claude Code stops being
// generated: only when the conformance test found that Claude Code reads
// .agents/skills/ and an Owner or Administrator approved turning it off.
func (t *Table) copyOff(ctx context.Context) (bool, error) {
	if t.o.Conformance == nil || t.o.CopyApproval == nil {
		return false, nil
	}
	read, err := t.o.Conformance.AgentsSkillsRead(ctx)
	if err != nil || !read {
		return false, err
	}
	return t.o.CopyApproval.ClaudeCopyOffApproved(ctx)
}

// affected are the providers that cannot start because of a problem.
func affected(p Problem) []providers.ID {
	switch {
	case p.Path == string(providers.ClaudeCode) || p.Path == string(providers.Codex) || p.Path == string(providers.Antigravity):
		return []providers.ID{providers.ID(p.Path)}
	case strings.HasPrefix(p.Path, ".claude/"):
		return []providers.ID{providers.ClaudeCode}
	case p.Path == agyHooks:
		return []providers.ID{providers.Antigravity}
	case strings.HasPrefix(p.Path, ".agents/skills/"):
		return []providers.ID{providers.Codex, providers.Antigravity}
	}
	return nil
}

// trustProblem is the Path of a problem of the trust of a provider.
func trustPath(id providers.ID) string { return string(id) }

func (t *Table) trust(req TableRequest, rep *TableReport) {
	t.mu.Lock()
	t.trusted[req.Dir] = unionProfiles(t.trusted[req.Dir], req.Profiles)
	t.mu.Unlock()
	for _, p := range req.Profiles {
		ad, err := t.o.Worktree.Providers.For(p.Provider)
		if err == nil {
			var tr providers.Trust
			tr, err = ad.PreTrust(providers.PreTrustRequest{OS: t.o.Worktree.OS, Profile: p.Dir, Worktree: req.Dir, RepoRoot: req.RepoRoot})
			rep.Trusted = append(rep.Trusted, tr.Trusted...)
		}
		if err != nil {
			pr := Problem{Path: trustPath(p.Provider), Reason: "a confiança da pasta não foi gravada no perfil: " + err.Error()}
			rep.Problems = append(rep.Problems, pr)
			rep.Blocked[p.Provider] = append(rep.Blocked[p.Provider], pr)
		}
	}
}

// tell warns the users, once for each distinct set of problems of a worktree.
func (t *Table) tell(ctx context.Context, req TableRequest, rep TableReport) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(rep.Problems) == 0 {
		delete(t.told, req.Dir)
		return
	}
	text := problemsText("Arquivos da mesa "+req.Table+" que o Mestre não gerou (os personagens dos provedores afetados não iniciam nessa pasta até resolver)", rep.Problems)
	if t.o.Notices == nil || t.told[req.Dir] == text {
		return
	}
	if err := t.o.Notices.Notify(ctx, req.Workspace, Notice{Kind: "provider_files", Text: text}); err == nil {
		t.told[req.Dir] = text
	}
}

// copyClasses makes .mestre/run/classes/<class>/ from the approved version of
// each class, and removes what is no longer in the package.
func (t *Table) copyClasses(ctx context.Context, req TableRequest, rep *TableReport) {
	if t.o.Classes == nil {
		return
	}
	for _, name := range req.Classes {
		if name == "" || name != path.Base(name) || name == "." || name == ".." || strings.ContainsAny(name, `\:`) {
			rep.Problems = append(rep.Problems, Problem{Path: path.Join(MestreDir, "run", "classes", name), Reason: "nome de classe inválido"})
			continue
		}
		c, err := t.o.Classes.Approved(ctx, name, req.Table)
		if err != nil {
			rep.Problems = append(rep.Problems, Problem{Path: path.Join(MestreDir, "run", "classes", name), Reason: "a classe não foi copiada: " + err.Error()})
			continue
		}
		var files []File
		for _, f := range c.Files {
			files = append(files, File{Path: f.Path, Data: f.Data})
		}
		t.sync(req.Dir, path.Join(MestreDir, "run", "classes", name), files, true, rep)
	}
}

func (t *Table) copyAttachments(ctx context.Context, req TableRequest, rep *TableReport) {
	if t.o.Attachments == nil {
		return
	}
	files, err := t.o.Attachments.For(ctx, req.Workspace, req.Table)
	if err != nil {
		rep.Problems = append(rep.Problems, Problem{Path: path.Join(MestreDir, "anexos"), Reason: "os anexos não foram copiados: " + err.Error()})
		return
	}
	t.sync(req.Dir, path.Join(MestreDir, "anexos"), files, false, rep)
}

// sync makes the folder rel of the worktree hold files: it writes what is
// missing or different (never what is equal), and with prune removes what is
// not in files. Everything goes through safefile; a path that would leave
// the folder is a problem.
func (t *Table) sync(dir, rel string, files []File, prune bool, rep *TableReport) {
	w, r, p := syncFolder(dir, rel, files, prune, false)
	rep.Written, rep.Removed = append(rep.Written, w...), append(rep.Removed, r...)
	rep.Problems = append(rep.Problems, p...)
}

// syncFolder is sync for any folder of a worktree: what it wrote, what it
// removed and what it could not do. With skills, a generated skill (one whose
// new SKILL.md carries the marker) is never written over a SKILL.md that
// exists without the marker: that one is the person's, and it is a conflict.
// (The skills copied from a class have no marker and are the engine's: the
// folder is one of the character, nobody else writes there.)
func syncFolder(dir, rel string, files []File, prune, skills bool) (written, removed []string, problems []Problem) {
	handmade := func(skill string) bool {
		cur, _, err := safefile.Read(filepath.Join(dir, filepath.FromSlash(rel), skill, "SKILL.md"), maxManaged)
		return err == nil && !hasSkillMarker(cur)
	}
	generated := map[string]bool{}
	for _, f := range files {
		if top, name, nested := strings.Cut(f.Path, "/"); skills && nested && name == "SKILL.md" && hasSkillMarker(f.Data) {
			generated[top] = true
		}
	}
	skipped := map[string]bool{}
	rep := &TableReport{}
	want := map[string]bool{}
	for _, f := range files {
		if err := checkRel(f.Path); err != nil {
			rep.Problems = append(rep.Problems, Problem{Path: path.Join(rel, f.Path), Reason: err.Error()})
			continue
		}
		want[f.Path] = true
		if skills {
			if top, _, nested := strings.Cut(f.Path, "/"); nested && generated[top] && handmade(top) {
				if !skipped[top] {
					skipped[top] = true
					rep.Problems = append(rep.Problems, Problem{Path: path.Join(rel, top, "SKILL.md"), Reason: "já existe e não tem o marcador de arquivo gerado: conflito, nada foi alterado"})
				}
				continue
			}
		}
		file := filepath.Join(dir, filepath.FromSlash(rel), filepath.FromSlash(f.Path))
		cur, before, err := safefile.Read(file, maxManaged)
		switch {
		case err == nil && bytes.Equal(cur, f.Data):
			continue
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			rep.Problems = append(rep.Problems, Problem{Path: path.Join(rel, f.Path), Reason: "não foi lido: " + err.Error()})
			continue
		}
		o := safefile.Options{Guard: func(now safefile.Info) error {
			if !now.Same(before) {
				return errors.New("o arquivo mudou enquanto era copiado")
			}
			return nil
		}}
		if strings.HasPrefix(f.Path, classes.HooksDir+"/") {
			o.Mode = 0o755 // a script of a hook runs
		}
		if err := safefile.Write(file, f.Data, o); err != nil {
			rep.Problems = append(rep.Problems, Problem{Path: path.Join(rel, f.Path), Reason: "não foi gravado: " + err.Error()})
			continue
		}
		rep.Written = append(rep.Written, path.Join(rel, f.Path))
	}
	if !prune {
		return rep.Written, rep.Removed, rep.Problems
	}
	base := filepath.Join(dir, filepath.FromSlash(rel))
	for _, old := range listFiles(base) {
		if want[old] {
			continue
		}
		if top, _, nested := strings.Cut(old, "/"); skills && nested && strings.HasPrefix(top, "mestre-") && handmade(top) {
			rep.Problems = append(rep.Problems, Problem{Path: path.Join(rel, top, "SKILL.md"), Reason: "não tem o marcador de arquivo gerado: a pasta é de quem a fez e nada nela foi removido"})
			continue
		}
		if err := safefile.Remove(filepath.Join(base, filepath.FromSlash(old)), nil); err != nil {
			rep.Problems = append(rep.Problems, Problem{Path: path.Join(rel, old), Reason: "não foi removido: " + err.Error()})
			continue
		}
		rep.Removed = append(rep.Removed, path.Join(rel, old))
	}
	return rep.Written, rep.Removed, rep.Problems
}

// checkRel refuses a path of a copy that is not plainly inside its folder.
func checkRel(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\:\x00") {
		return fmt.Errorf("caminho inválido %q", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("caminho inválido %q", p)
		}
	}
	return nil
}

// listFiles lists the regular files under base, relative and with slashes. A
// link is not followed and not listed; a missing folder has none.
func listFiles(base string) []string {
	var out []string
	var walk func(dir, rel string)
	walk = func(dir, rel string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			r := path.Join(rel, e.Name())
			switch {
			case e.Type()&fs.ModeSymlink != 0:
			case e.IsDir():
				walk(filepath.Join(dir, e.Name()), r)
			case e.Type().IsRegular():
				out = append(out, r)
			}
		}
	}
	walk(base, "")
	slices.Sort(out)
	return out
}

// Sync runs sync, the update of the branch of a worktree (a rebase, an update
// from the main branch), without the Mestre's part in the files the project
// versions: before it, the flag skip-worktree is turned off and the file goes
// back to what the project has (the person's own changes stay); after it,
// even when it fails, Prepare merges the Mestre's part again over whatever the
// project has then and marks the file. So skip-worktree is never on during a
// rebase (docs/sessao-zero/mesa.md#mescla-gerenciada).
func (t *Table) Sync(ctx context.Context, req TableRequest, sync func(context.Context) error) (err error) {
	if req.Dir == "" || !filepath.IsAbs(req.Dir) || req.Repo == nil {
		return errors.New("sessionzero: a table needs an absolute worktree and its repository")
	}
	detached := false
	defer func() {
		if !detached {
			return
		}
		if _, perr := t.Prepare(context.WithoutCancel(ctx), req); perr != nil {
			err = errors.Join(err, fmt.Errorf("put the Mestre's part back: %w", perr))
		}
	}()
	if detached, err = t.detach(ctx, req); err != nil {
		return err
	}
	return sync(ctx)
}

// detach gives the versioned hook files back to the project. It returns
// whether it changed anything that Prepare must redo.
func (t *Table) detach(ctx context.Context, req TableRequest) (bool, error) {
	changed := false
	for _, f := range []struct {
		rel string
		id  providers.ID
	}{{claudeConfig, providers.ClaudeCode}, {agyHooks, providers.Antigravity}} {
		tracked, err := isTracked(ctx, req.Repo, f.rel)
		if err != nil {
			return changed, err
		}
		if !tracked {
			continue
		}
		changed = true
		if err := req.Repo.SetSkipWorktree(ctx, false, f.rel); err != nil {
			return changed, fmt.Errorf("skip-worktree %s: %w", f.rel, err)
		}
		if err := t.giveBack(ctx, req, f.rel, f.id); err != nil {
			return changed, err
		}
	}
	return changed, nil
}

// giveBack takes the Mestre's part out of a versioned file. When what is left
// is what the project has at HEAD (apart from the form), the file is the
// project's byte for byte, so that git sees no change.
func (t *Table) giveBack(ctx context.Context, req TableRequest, rel string, id providers.ID) error {
	ad, err := t.o.Worktree.Providers.For(id)
	if err != nil {
		return err
	}
	st, ok := ad.(providers.HookStripper)
	if !ok {
		return fmt.Errorf("%s: the adapter cannot take its hooks out", id)
	}
	file := filepath.Join(req.Dir, filepath.FromSlash(rel))
	cur, before, err := safefile.Read(file, maxManaged)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", rel, err)
	}
	next, err := st.StripHooks(file, cur)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	if head, err := req.Repo.ReadFile(ctx, "HEAD", rel); err == nil && sameJSON(head, next) {
		next = head
	}
	if bytes.Equal(cur, next) {
		return nil
	}
	guard := func(now safefile.Info) error {
		if !now.Same(before) {
			return errors.New("o arquivo mudou enquanto era devolvido ao projeto")
		}
		return nil
	}
	if err := safefile.Write(file, next, safefile.Options{Guard: guard}); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	return nil
}

// sameJSON reports whether two documents are the same JSON value, whatever
// their form (indentation, order of the keys).
func sameJSON(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}
