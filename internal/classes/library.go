package classes

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/lucasndas/mestre/pkg/clock"
)

// Roots are the folders the classes of a workspace live in
// (docs/classes/classes.md#escopos): the global ones in the data of the
// Citadel, the others in `.mestre/` of the main clone of the workspace.
type Roots struct {
	// Global is classes-globais/ in the data of the Citadel.
	Global string
	// Workspace is .mestre/classes/ and Tables .mestre/mesas/ of the main
	// clone; the classes of a table are in <Tables>/<mesa>/classes/.
	Workspace, Tables string
}

// DefaultRoots are the Roots of a workspace whose main clone is at clone.
func DefaultRoots(global, clone string) Roots {
	return Roots{
		Global:    global,
		Workspace: filepath.Join(clone, ".mestre", "classes"),
		Tables:    filepath.Join(clone, ".mestre", "mesas"),
	}
}

var tableRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidTable reports whether s can name a table in a path (<number>-<slug>).
func ValidTable(s string) bool { return len(s) <= 80 && tableRe.MatchString(s) }

// ScopeDir is the folder that holds the classes of a scope.
func (r Roots) ScopeDir(scope ClassScope, table string) (string, error) {
	switch scope {
	case ClassGlobal:
		return r.Global, nil
	case ClassWorkspace:
		return r.Workspace, nil
	case ClassTable:
		if !ValidTable(table) {
			return "", fmt.Errorf("invalid table %q", table)
		}
		return filepath.Join(r.Tables, table, "classes"), nil
	}
	return "", fmt.Errorf("invalid scope %q", scope)
}

// Dir is the folder of a class.
func (r Roots) Dir(k ClassKey) (string, error) {
	if !ValidName(k.Name) {
		return "", fmt.Errorf("invalid class name %q", k.Name)
	}
	d, err := r.ScopeDir(k.Scope, k.Table)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, k.Name), nil
}

// CeilingProvider gives the ceiling that holds for the workspace
// (CeilingKeeper).
type CeilingProvider interface {
	Current(ctx context.Context) (*Ceiling, error)
}

// ClassAlertKind is what the engine noticed in the files of a class.
type ClassAlertKind string

// The alerts to the users.
const (
	// ClassProposed: the files differ from the approved version and are
	// valid; a proposal waits for the approval of the scope.
	ClassProposed ClassAlertKind = "proposed"
	// ClassInvalid: the files differ and are invalid; the last approved
	// version holds and the proposal cannot be approved.
	ClassInvalid ClassAlertKind = "invalid"
	// ClassMissing: the folder of an approved class is gone.
	ClassMissing ClassAlertKind = "missing"
	// ClassUnreadable: the folder cannot be read as a class package.
	ClassUnreadable ClassAlertKind = "unreadable"
	// ClassWaiting: a change made through the Mestre by someone who only
	// proposes waits for approval.
	ClassWaiting ClassAlertKind = "waiting"
)

// ClassAlert tells the users that a class changed outside the Mestre, or
// that a change waits for them (the panel and the Grand Master warn them).
type ClassAlert struct {
	Key  ClassKey
	Kind ClassAlertKind
	// Hash is the version on disk; Approved the one that holds.
	Hash, Approved string
	Problems       []string
	// Proposal is the number of the proposal, when there is one.
	Proposal int64
}

// ClassAlerts reaches the users (Telegram and panel, in their milestones).
type ClassAlerts interface {
	ClassChanged(ctx context.Context, a ClassAlert) error
}

// Library is the classes of one workspace as the Citadel keeps them: the
// files in their folders, the approved version of each in the database, and
// the proposals. The engine only ever uses the approved version: a class
// changed outside the Mestre is never used without approval.
type Library struct {
	// Workspace keys the records of the workspace and table classes; the
	// global ones are keyed by "".
	Workspace string
	Roots     Roots
	Records   ClassRecords
	// Ceiling gives the ceiling of the workspace; nil is the default one.
	Ceiling CeilingProvider
	// Alerts is told about each change found; nil drops them.
	Alerts ClassAlerts
	// Apps knows the Apps of GitHub (M5); nil does not.
	Apps AppDirectory
	// Skill says whether a skill exists in the project (.agents/skills/);
	// nil finds none.
	Skill func(name string) bool
	Clock clock.Clock
	// OS is the system `class check` translates the controls for ("" is
	// this one).
	OS string

	mu       sync.Mutex
	alerted  map[ClassKey]string   // the last change alerted, so one change gives one alert
	proposed map[ClassKey]Proposal // the pending proposal from disk of a class, so a look that finds the same files writes nothing
}

// Errors of the library. The CLI maps them to its codes
// (docs/cli/coordenacao-classes-e-rituais.md).
var (
	// ErrNameTaken: the name is in use in a scope visible from the class.
	ErrNameTaken = errors.New("name_taken: o nome já é usado num escopo visível")
	// ErrNotVisible: no class of that name is visible from the table.
	ErrNotVisible = errors.New("not_visible: nenhuma classe com esse nome é visível")
	// ErrNotAuthorized: the author may not do this in that scope.
	ErrNotAuthorized = errors.New("forbidden: quem pede não pode fazer isto neste escopo")
	// ErrClassChanged: the files or the proposal changed since the version
	// the approver saw.
	ErrClassChanged = errors.New("a classe mudou desde a versão mostrada")
	// ErrProposalInvalid: a proposal with problems cannot be approved.
	ErrProposalInvalid = errors.New("a proposta tem problemas e não pode ser aprovada")
	// ErrNoApproved: the class was never approved, so there is nothing to use.
	ErrNoApproved = errors.New("a classe ainda não tem versão aprovada")
)

func (l *Library) ws(scope ClassScope) string {
	if scope == ClassGlobal {
		return ""
	}
	return l.Workspace
}

func (l *Library) ceiling(ctx context.Context) (*Ceiling, error) {
	if l.Ceiling == nil {
		return DefaultCeiling(), nil
	}
	return l.Ceiling.Current(ctx)
}

// CheckCeiling says whether the approved version of a class still fits in the
// ceiling of the workspace, which may have narrowed since it was approved: nil
// when it fits (or has no approved version, which is not a matter of the
// ceiling), ErrCeilingDenied naming what passes otherwise.
func (l *Library) CheckCeiling(ctx context.Context, name, table string) error {
	c, err := l.Approved(ctx, name, table)
	switch {
	case errors.Is(err, ErrNoApproved):
		return nil
	case err != nil:
		return err
	case c.Permissions == nil:
		return nil
	}
	ceil, err := l.ceiling(ctx)
	if err != nil {
		return err
	}
	return ceil.Fits(c.Permissions)
}

// scopes are the scopes visible from a table, the most specific first.
func scopes(table string) []ClassKey {
	var out []ClassKey
	if table != "" {
		out = append(out, ClassKey{Scope: ClassTable, Table: table})
	}
	return append(out, ClassKey{Scope: ClassWorkspace}, ClassKey{Scope: ClassGlobal})
}

// approved finds the approved version of a name visible from the table:
// the most specific scope wins (names are unique among the visible scopes,
// so two only meet if an approval went wrong).
func (l *Library) approved(ctx context.Context, name, table string) (ClassRecord, bool, error) {
	for _, s := range scopes(table) {
		k := ClassKey{Scope: s.Scope, Table: s.Table, Name: name}
		rec, ok, err := l.Records.Approved(ctx, l.ws(k.Scope), k)
		if err != nil || ok {
			return rec, ok, err
		}
	}
	return ClassRecord{}, false, nil
}

// Use is the class a character is to use at the start of a turn: always the
// approved version. It also looks at the files on disk: if they differ from
// the approved version (a hand edit, a git pull, a merged PR), valid or not,
// that is a proposal that waits for the approval of the scope, the users are
// told once, and the last approved version keeps holding.
func (l *Library) Use(ctx context.Context, name, table string) (*Class, error) {
	if name == ReservedOrchestrator {
		return Orchestrator() // built into the binary: no files to look at, no approval
	}
	rec, ok, err := l.approved(ctx, name, table)
	if err != nil {
		return nil, err
	}
	if !ok {
		// Never approved: if the files are there, that is a proposal too.
		for _, s := range scopes(table) {
			if _, err := l.reconcile(ctx, ClassKey{Scope: s.Scope, Table: s.Table, Name: name}, nil); err != nil {
				return nil, err
			}
		}
		return nil, fmt.Errorf("%w: %s", ErrNoApproved, name)
	}
	if _, err := l.reconcile(ctx, rec.Key, &rec); err != nil {
		return nil, err
	}
	return l.load(rec)
}

// Approved is the approved version of a class, without looking at the files
// on disk (Use does that, at the start of a turn). The events of the turn
// use it: reading a folder at each tool call would cost what it does not
// need to.
func (l *Library) Approved(ctx context.Context, name, table string) (*Class, error) {
	if name == ReservedOrchestrator {
		return Orchestrator()
	}
	rec, ok, err := l.approved(ctx, name, table)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoApproved, name)
	}
	return l.load(rec)
}

// load reads the class of an approved version. The skills of the project are
// not looked for again: a skill removed later does not make the approved
// class unusable (`class check` warns).
func (l *Library) load(rec ClassRecord) (*Class, error) {
	ex := Examine(rec.Files, ExamineOptions{Scope: rec.Key.Scope, Dir: rec.Key.Name, Skill: func(string) bool { return true }})
	if !ex.OK() {
		return nil, fmt.Errorf("approved class %s: %w", rec.Key, ex.Err())
	}
	ex.Class.Table = rec.Key.Table
	return ex.Class, nil
}

// EntryState is where a class stands.
type EntryState string

// The states of a class in a listing.
const (
	EntryApproved   EntryState = "approved"
	EntryPending    EntryState = "pending"    // the files differ from the approved version
	EntryNew        EntryState = "new"        // never approved, only a proposal
	EntryMissing    EntryState = "missing"    // approved, but the folder is gone
	EntryUnreadable EntryState = "unreadable" // the folder is not a readable package
)

// Entry is one class of a listing.
type Entry struct {
	Key   ClassKey
	State EntryState
	// Hash is the version on disk, Approved the one that holds.
	Hash, Approved string
	Version        int64
	App            string
	Proposal       *Proposal
	Problems       []string
}

// Scan lists the classes visible from a table (the global ones, the
// workspace's and the table's), with the state of each against its approved
// version, and records a proposal for each difference found.
func (l *Library) Scan(ctx context.Context, table string) ([]Entry, error) {
	var out []Entry
	for _, s := range scopes(table) {
		dir, err := l.Roots.ScopeDir(s.Scope, s.Table)
		if err != nil {
			return nil, err
		}
		names, err := folders(dir)
		if err != nil {
			return nil, err
		}
		recs, err := l.Records.AllApproved(ctx, l.ws(s.Scope))
		if err != nil {
			return nil, err
		}
		byName := map[string]ClassRecord{}
		for _, r := range recs {
			if r.Key.Scope == s.Scope && r.Key.Table == s.Table {
				byName[r.Key.Name] = r
				if !slices.Contains(names, r.Key.Name) {
					names = append(names, r.Key.Name)
				}
			}
		}
		slices.Sort(names)
		for _, n := range names {
			k := ClassKey{Scope: s.Scope, Table: s.Table, Name: n}
			if n == ReservedOrchestrator {
				// Not an editable class: a folder with its name is not read
				// as a proposal, and the users are told once.
				if err := l.alert(ctx, ClassAlert{Key: k, Kind: ClassInvalid, Problems: []string{ErrNameTaken.Error()}}, "reserved"); err != nil {
					return nil, err
				}
				continue
			}
			var rec *ClassRecord
			if r, ok := byName[n]; ok {
				rec = &r
			}
			e, err := l.reconcile(ctx, k, rec)
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		}
	}
	return out, nil
}

// folders lists the class folders of a scope folder: the subfolders that
// are not hidden (the ones the engine itself leaves while it replaces a
// class start with a dot). A missing folder has none.
func folders(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && ValidName(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// reconcile compares the folder of a class with its approved version (nil:
// never approved) and records the difference as a proposal, alerting once.
func (l *Library) reconcile(ctx context.Context, k ClassKey, rec *ClassRecord) (Entry, error) {
	e := Entry{Key: k}
	if k.Name == ReservedOrchestrator {
		return Entry{}, nil // built in: never a proposal (docs/classes/classe-orquestrador.md)
	}
	if rec != nil {
		e.Hash, e.Approved, e.Version, e.App = rec.Hash, rec.Hash, rec.Version, rec.App
	}
	dir, err := l.Roots.Dir(k)
	if err != nil {
		return e, err
	}
	files, err := ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if rec == nil {
			return Entry{}, nil
		}
		e.State = EntryMissing
		e.Hash = ""
		return e, l.alert(ctx, ClassAlert{Key: k, Kind: ClassMissing, Approved: rec.Hash}, "missing")
	case err != nil:
		e.State, e.Problems = EntryUnreadable, []string{err.Error()}
		a := ClassAlert{Key: k, Kind: ClassUnreadable, Problems: e.Problems}
		if rec != nil {
			a.Approved = rec.Hash
		}
		return e, l.alert(ctx, a, "unreadable:"+err.Error())
	}
	e.Hash = files.Hash()
	if rec != nil && e.Hash == rec.Hash {
		e.State = EntryApproved
		return e, l.clean(ctx, k)
	}
	problems, err := l.problems(ctx, files, k)
	if err != nil {
		return e, err
	}
	l.mu.Lock()
	p, known := l.proposed[k]
	l.mu.Unlock()
	created := false
	if !known || p.Hash != e.Hash {
		if p, created, err = l.Records.Propose(ctx, l.ws(k.Scope), Proposal{
			Key: k, Hash: e.Hash, Files: files, Origin: OriginDisk, Problems: problems,
		}); err != nil {
			return e, err
		}
		l.mu.Lock()
		if l.proposed == nil {
			l.proposed = map[ClassKey]Proposal{}
		}
		l.proposed[k] = p
		l.mu.Unlock()
	}
	e.Proposal, e.Problems = &p, problems
	e.State = EntryPending
	if rec == nil {
		e.State = EntryNew
	}
	if created {
		a := ClassAlert{Key: k, Kind: ClassProposed, Hash: e.Hash, Problems: problems, Proposal: p.ID}
		if len(problems) > 0 {
			a.Kind = ClassInvalid
		}
		if rec != nil {
			a.Approved = rec.Hash
		}
		if err := l.notify(ctx, a); err != nil {
			return e, err
		}
	}
	return e, nil
}

// clean closes the proposals from disk of a class whose files went back to
// the approved version.
func (l *Library) clean(ctx context.Context, k ClassKey) error {
	l.mu.Lock()
	_, was := l.proposed[k]
	delete(l.proposed, k)
	delete(l.alerted, k)
	l.mu.Unlock()
	if !was {
		return nil
	}
	return l.Records.Supersede(ctx, l.ws(k.Scope), k, OriginDisk)
}

// alert sends an alert of a state that is not stored (a missing or
// unreadable folder) once per distinct state.
func (l *Library) alert(ctx context.Context, a ClassAlert, state string) error {
	l.mu.Lock()
	if l.alerted == nil {
		l.alerted = map[ClassKey]string{}
	}
	fresh := l.alerted[a.Key] != state
	l.alerted[a.Key] = state
	l.mu.Unlock()
	if !fresh {
		return nil
	}
	if err := l.notify(ctx, a); err != nil {
		l.mu.Lock()
		delete(l.alerted, a.Key) // try again at the next look
		l.mu.Unlock()
		return err
	}
	return nil
}

func (l *Library) notify(ctx context.Context, a ClassAlert) error {
	if l.Alerts == nil {
		return nil
	}
	if err := l.Alerts.ClassChanged(ctx, a); err != nil {
		return fmt.Errorf("class %s: alert: %w", a.Key, err)
	}
	return nil
}

// problems are what makes a package invalid for its class: its own problems,
// the ceiling (workspace and table classes; a global class is cut by the
// ceiling of each workspace when the rules are built) and the rules of the
// App link. The proposals from disk go through the same validations as a
// change made through the Mestre.
func (l *Library) problems(ctx context.Context, files Files, k ClassKey) ([]string, error) {
	ex := Examine(files, ExamineOptions{Scope: k.Scope, Dir: k.Name, Skill: l.Skill})
	var out []string
	for _, p := range ex.Problems {
		out = append(out, p.String())
	}
	if len(out) > 0 {
		return out, nil
	}
	if err := l.beyond(ctx, ex.Class, k); err != nil {
		if errors.Is(err, ErrCeilingDenied) {
			return []string{err.Error()}, nil
		}
		var inv *InvalidError
		if errors.As(err, &inv) {
			for _, p := range inv.Problems {
				out = append(out, p.String())
			}
			return out, nil
		}
		return nil, err
	}
	return nil, nil
}

// beyond runs the checks that need the world outside the package: the
// ceiling (ErrCeilingDenied) and the App link (an *InvalidError).
func (l *Library) beyond(ctx context.Context, c *Class, k ClassKey) error {
	if k.Scope != ClassGlobal && c.Permissions != nil {
		ceil, err := l.ceiling(ctx)
		if err != nil {
			return err
		}
		if err := ceil.Fits(c.Permissions); err != nil {
			return err
		}
	}
	if probs := l.appProblems(ctx, c, k); len(probs) > 0 {
		return &InvalidError{Problems: probs}
	}
	return nil
}

// taken finds a class that already has the name in a scope visible from k:
// a global or workspace class is visible from every table, so it meets the
// table classes of that name too; two table classes meet only in one table.
func (l *Library) taken(ctx context.Context, k ClassKey) (ClassKey, bool, error) {
	meets := func(o ClassKey) bool {
		if o.Name != k.Name || (o.Scope == k.Scope && o.Table == k.Table) {
			return false
		}
		return o.Scope != ClassTable || k.Scope != ClassTable || o.Table == k.Table
	}
	for _, ws := range []string{"", l.Workspace} {
		recs, err := l.Records.AllApproved(ctx, ws)
		if err != nil {
			return ClassKey{}, false, err
		}
		for _, r := range recs {
			if meets(r.Key) {
				return r.Key, true, nil
			}
		}
		pend, err := l.Records.Pending(ctx, ws)
		if err != nil {
			return ClassKey{}, false, err
		}
		for _, p := range pend {
			if meets(p.Key) {
				return p.Key, true, nil
			}
		}
	}
	// The folders: a class that came by git and was not looked at yet.
	var candidates []ClassKey
	for _, s := range []ClassScope{ClassGlobal, ClassWorkspace} {
		candidates = append(candidates, ClassKey{Scope: s, Name: k.Name})
	}
	if k.Scope == ClassTable {
		candidates = append(candidates, ClassKey{Scope: ClassTable, Table: k.Table, Name: k.Name})
	} else if ents, err := os.ReadDir(l.Roots.Tables); err == nil {
		for _, e := range ents {
			if e.IsDir() && ValidTable(e.Name()) {
				candidates = append(candidates, ClassKey{Scope: ClassTable, Table: e.Name(), Name: k.Name})
			}
		}
	}
	for _, c := range candidates {
		if !meets(c) {
			continue
		}
		dir, err := l.Roots.Dir(c)
		if err != nil {
			continue
		}
		if st, err := os.Lstat(dir); err == nil && st.IsDir() {
			return c, true, nil
		}
	}
	return ClassKey{}, false, nil
}
