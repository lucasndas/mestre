package classes

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// SaveRequest is a class created or edited through the Mestre
// (`mestre class create|edit`, the panel).
type SaveRequest struct {
	Name  string
	Scope ClassScope
	// Table is the table of a table class.
	Table string
	// App is the --app: required to create; to edit, empty keeps the App.
	App string
	// Files is the package. To edit, nil keeps the files and only changes
	// the App.
	Files Files
	Edit  bool
	By    Author
}

// ReservedOrchestrator is the name of the built-in class of the Grand
// Master and the Masters of table (docs/classes/classe-orquestrador.md):
// no class of any scope is created with it.
const ReservedOrchestrator = "orquestrador"

// SaveResult is what came of a SaveRequest.
type SaveResult struct {
	Key  ClassKey
	Path string
	App  AppReport
	// Applied is true when the class was saved and is in use; otherwise
	// Proposal waits for the approval of the scope.
	Applied  bool
	Proposal *Proposal
}

// canApplyAlone says whether the author puts a change of the scope in use
// by himself: the Owner and the Administrators in every scope (they are the
// ones who approve), the Mestre da mesa in its table's scope (inside the
// ceiling). The Grand Master only proposes in the workspace scope.
func canApplyAlone(scope ClassScope, table string, by Author) bool {
	switch scope {
	case ClassGlobal:
		return by.Level == LevelOwner || by.Level == LevelAdmin
	case ClassWorkspace:
		return by.Level == LevelOwner || by.Level == LevelAdmin
	case ClassTable:
		return by.Level == LevelOwner || by.Level == LevelAdmin || by.Level == LevelTableMaster && by.Table == table
	}
	return false
}

// mayPropose says whether the author may at least propose: the Grand Master
// in the workspace scope.
func mayPropose(scope ClassScope, by Author) bool {
	return scope == ClassWorkspace && by.Level == LevelGrandMaster
}

// ErrNeedsAdministrator: only an Administrator or above approves a class
// of the workspace; the Grand Master never approves what he proposed.
var ErrNeedsAdministrator = errors.New("forbidden: só um Administrador ou acima aprova uma classe do workspace; o Grão-Mestre nunca aprova a própria proposta")

// Save creates or edits a class: it validates the package (the App link, the
// structure, the permissions, the hooks), checks the ceiling, the name among
// the visible scopes and the right of the author in the scope. A change the
// author may apply alone is written and put in use at once; the others
// become a proposal for the Administrators.
func (l *Library) Save(ctx context.Context, req SaveRequest) (SaveResult, error) {
	k := ClassKey{Scope: req.Scope, Table: req.Table, Name: req.Name}
	switch {
	case !req.Scope.Valid():
		return SaveResult{}, fmt.Errorf("escopo %q inválido: global, workspace ou table", req.Scope)
	case !ValidName(req.Name):
		return SaveResult{}, fmt.Errorf("o nome %q não é um slug (`[a-z0-9-]`, até %d caracteres)", req.Name, MaxNameLen)
	case req.Scope == ClassTable && !ValidTable(req.Table):
		return SaveResult{}, fmt.Errorf("uma classe da mesa precisa da mesa (recebeu %q)", req.Table)
	case req.Scope != ClassTable && req.Table != "":
		return SaveResult{}, fmt.Errorf("só uma classe da mesa tem mesa")
	}
	if !canApplyAlone(req.Scope, req.Table, req.By) && !mayPropose(req.Scope, req.By) {
		return SaveResult{}, fmt.Errorf("%w (%s, escopo %s)", ErrNotAuthorized, req.By.Level, scopeLabel(req.Scope))
	}
	if !req.Edit && strings.TrimSpace(req.App) == "" {
		return SaveResult{}, ErrAppRequired
	}
	if !req.Edit && req.Name == ReservedOrchestrator {
		return SaveResult{}, fmt.Errorf("%w: %q é a classe embutida dos orquestradores", ErrNameTaken, req.Name)
	}

	cur, exists, err := l.Records.Approved(ctx, l.ws(k.Scope), k)
	if err != nil {
		return SaveResult{}, err
	}
	switch {
	case req.Edit && !exists:
		return SaveResult{}, fmt.Errorf("%w: %s", ErrNotVisible, k)
	case !req.Edit:
		if other, taken, err := l.taken(ctx, k); err != nil {
			return SaveResult{}, err
		} else if taken || exists {
			if !taken {
				other = k
			}
			return SaveResult{}, fmt.Errorf("%w: %q já existe como classe %s", ErrNameTaken, req.Name, scopeLabel(other.Scope))
		}
	}

	files := req.Files
	if req.Edit && files == nil {
		files = cur.Files
	}
	// The package as the caller built it is not trusted: sorted, its paths
	// and its sizes are checked here, before and after the App line.
	if files, err = NewFiles(files); err != nil {
		return SaveResult{}, err
	}
	files, err = withApp(files, req.App, req.Edit)
	if err == nil {
		files, err = NewFiles(files)
	}
	if err != nil {
		return SaveResult{}, err
	}
	ex := Examine(files, ExamineOptions{Scope: k.Scope, Dir: k.Name, Skill: l.Skill})
	if !ex.OK() {
		return SaveResult{}, ex.Err()
	}
	if err := l.beyond(ctx, ex.Class, k); err != nil {
		return SaveResult{}, err
	}

	dir, err := l.Roots.Dir(k)
	if err != nil {
		return SaveResult{}, err
	}
	res := SaveResult{Key: k, Path: dir, App: l.appReport(ctx, ex.Class, k.Table)}
	hash := files.Hash()
	if !canApplyAlone(req.Scope, req.Table, req.By) {
		p, _, err := l.Records.Propose(ctx, l.ws(k.Scope), Proposal{Key: k, Hash: hash, Files: files, Origin: OriginRequest, By: req.By.ID})
		if err != nil {
			return SaveResult{}, err
		}
		res.Proposal = &p
		a := ClassAlert{Key: k, Kind: ClassWaiting, Hash: hash, Proposal: p.ID}
		if exists {
			a.Approved = cur.Hash
		}
		return res, l.notify(ctx, a)
	}
	// The files first: if the record fails, the change is only a proposal
	// the next look at the folder finds.
	if err := WriteDir(dir, files); err != nil {
		return SaveResult{}, fmt.Errorf("class %s: %w", k, err)
	}
	if err := l.record(ctx, k, files, ex.Class, req.By, 0); err != nil {
		return SaveResult{}, err
	}
	res.Applied = true
	return res, nil
}

func (l *Library) record(ctx context.Context, k ClassKey, files Files, c *Class, by Author, closing int64) error {
	rec := ClassRecord{Key: k, Hash: files.Hash(), Files: files, App: c.App.String(), ApprovedAt: l.Clock.Now().UTC(), By: by}
	if err := l.Records.Approve(ctx, l.ws(k.Scope), rec, closing); err != nil {
		return fmt.Errorf("class %s: %w", k, err)
	}
	l.mu.Lock()
	delete(l.proposed, k)
	delete(l.alerted, k)
	l.mu.Unlock()
	return nil
}

// Approve is the "Aprovar" of a proposal: the author must be an
// Administrator or above in the workspace scope (the Mestre da mesa also in
// its table's), and hash is the one the approver saw. The package is checked
// again against the ceiling of today; a proposal with problems is refused.
// The files are written when the proposal came through the Mestre; one that
// came from disk is approved as the files are, if they still are.
func (l *Library) Approve(ctx context.Context, id int64, hash string, by Author) error {
	p, ok, err := l.proposal(ctx, id)
	if err != nil {
		return err
	}
	if !ok || p.State != ProposalPending {
		return fmt.Errorf("%w: a proposta %d não está pendente", ErrClassChanged, id)
	}
	if !canApplyAlone(p.Key.Scope, p.Key.Table, by) {
		if p.Key.Scope == ClassWorkspace {
			return fmt.Errorf("%w (%s)", ErrNeedsAdministrator, by.Level)
		}
		return fmt.Errorf("%w (%s, escopo %s)", ErrNotAuthorized, by.Level, scopeLabel(p.Key.Scope))
	}
	if p.Hash != hash {
		return fmt.Errorf("%w (a proposta é a %s)", ErrClassChanged, p.Hash)
	}
	if len(p.Problems) > 0 {
		return fmt.Errorf("%w: %s", ErrProposalInvalid, strings.Join(p.Problems, "; "))
	}
	if other, taken, err := l.taken(ctx, p.Key); err != nil {
		return err
	} else if taken {
		return fmt.Errorf("%w: %q já existe como classe %s", ErrNameTaken, p.Key.Name, scopeLabel(other.Scope))
	}
	ex := Examine(p.Files, ExamineOptions{Scope: p.Key.Scope, Dir: p.Key.Name, Skill: l.Skill})
	if !ex.OK() {
		return ex.Err()
	}
	if err := l.beyond(ctx, ex.Class, p.Key); err != nil {
		return err
	}
	dir, err := l.Roots.Dir(p.Key)
	if err != nil {
		return err
	}
	switch p.Origin {
	case OriginDisk:
		on, err := ReadDir(dir)
		if err != nil || on.Hash() != p.Hash {
			return fmt.Errorf("%w: os arquivos de %s mudaram de novo", ErrClassChanged, p.Key)
		}
	default:
		if err := WriteDir(dir, p.Files); err != nil {
			return fmt.Errorf("class %s: %w", p.Key, err)
		}
	}
	return l.record(ctx, p.Key, p.Files, ex.Class, by, p.ID)
}

func (l *Library) proposal(ctx context.Context, id int64) (Proposal, bool, error) {
	for _, ws := range []string{l.Workspace, ""} {
		if p, ok, err := l.Records.Proposal(ctx, ws, id); err != nil || ok {
			return p, ok, err
		}
	}
	return Proposal{}, false, nil
}

var appLineRe = regexp.MustCompile(`^app:[ \t]*(.*)$`)

// withApp puts the App link in the frontmatter of CLASS.md. When the file
// already has an `app` that differs, creating refuses (the --app and the
// file disagree) and editing replaces it. An empty app leaves the file as it
// is.
func withApp(files Files, app string, replace bool) (Files, error) {
	app = strings.TrimSpace(app)
	if app == "" {
		return files, nil
	}
	if _, err := ParseAppLink(app); err != nil {
		return nil, err
	}
	data, ok := files.Get(ClassFile)
	if !ok {
		return files, nil // Examine reports the missing file
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	rest, found := strings.CutPrefix(strings.TrimPrefix(text, "\ufeff"), "---\n")
	if !found {
		return files, nil // Examine reports the frontmatter
	}
	lines := strings.SplitAfter(rest, "\n")
	end := -1
	for i, ln := range lines {
		if strings.TrimRight(ln, "\n") == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return files, nil
	}
	set := false
	for i := 0; i < end; i++ {
		m := appLineRe.FindStringSubmatch(strings.TrimRight(lines[i], "\n"))
		if m == nil {
			continue
		}
		cur := strings.TrimSpace(strings.Split(m[1], "#")[0])
		cur = strings.Trim(cur, `"'`)
		if cur != app && !replace {
			return nil, fmt.Errorf("--app %q diverge do `app: %s` do CLASS.md; use um só", app, cur)
		}
		lines[i] = "app: " + app + "\n"
		set = true
	}
	if !set {
		lines = slices.Insert(lines, end, "app: "+app+"\n")
	}
	out := slices.Clone(files)
	for i := range out {
		if out[i].Path == ClassFile {
			out[i].Data = []byte("---\n" + strings.Join(lines, ""))
		}
	}
	return out, nil
}
