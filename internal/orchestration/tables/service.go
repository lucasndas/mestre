package tables

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Options are the pieces of the Service.
type Options struct {
	DB         *store.DB
	Git        Git
	Workspaces Workspaces
	Clock      clock.Clock
	// Machine is the id of this machine, the Citadel: its worktrees are the
	// ones made at the opening.
	Machine    string
	GitHub     GitHub
	Characters Characters
	Registry   characters.Registry
	Inbox      Inbox
	// Notifier tells the table master and the Grand Master
	// (inbox.Service).
	Notifier characters.Notifier
	Alerts   workspaces.Alerts
	Files    Files
	// Settings default to DefaultSettings.
	Settings Settings
	// The pieces below may be nil: no Messages, a `table open` has no origin
	// message; no Attachments, the attachments are not forwarded; no
	// Profiles, no profile trusts the worktree; no Destructive, a
	// cancellation is refused; no Rituals, none to deactivate; no Peers, no
	// Tower takes part.
	Messages    Messages
	Attachments Attachments
	Profiles    Profiler
	Destructive Destructive
	Rituals     Rituals
	Requests    Requests
	Oversight   Oversight
	// Proofs says which criteria of the missions of a table are proved (nil: none).
	Proofs Proofs
	Peers  Peers
	// Audit writes a line of the audit trail of the Guild; nil writes it in the
	// Guild database (Registry.Audit).
	Audit func(ctx context.Context, id string, at time.Time, machine, action string, detail []byte) error
	// Dependencies holds a table that waits for another (nil: none does).
	Dependencies Dependencies
	// TempDir is a private folder for the bundles while they are sent
	// (os.TempDir when empty).
	TempDir string
	Log     *slog.Logger
	// Gate, when set, is asked before the Service touches the outside: the
	// fence of the Citadel.
	Gate func() error
}

// Service is the tables of the Citadel (docs/orquestracao/mesas.md).
type Service struct {
	o     Options
	store Store
	clk   clock.Clock
	log   *slog.Logger
	// tmp is the private folder of the bundles being sent.
	tmp string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	// untrusted are the tables whose branch moved without the engine, by
	// workspace/table, with the commit it moved to: nothing of them is sent
	// or synchronised until someone trusts them again.
	untrusted map[string]string
}

// New builds the Service.
func New(o Options) (*Service, error) {
	switch {
	case o.DB == nil, o.Git == nil, o.Workspaces == nil, o.GitHub == nil, o.Characters == nil, o.Inbox == nil, o.Notifier == nil, o.Alerts == nil, o.Files == nil:
		return nil, errors.New("tables: DB, Git, Workspaces, GitHub, Characters, Inbox, Notifier, Alerts and Files are required")
	case o.Registry.DB == nil:
		return nil, errors.New("tables: the registry of characters is required")
	case o.Machine == "":
		return nil, errors.New("tables: the id of the machine is required")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Settings == nil {
		o.Settings = DefaultSettings{}
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	tmp, err := privateTemp(o.TempDir)
	if err != nil {
		return nil, fmt.Errorf("tables: %w", err)
	}
	s := &Service{o: o, store: Store{DB: o.DB}, clk: o.Clock, log: log, tmp: tmp, locks: map[string]*sync.Mutex{}, untrusted: map[string]string{}}
	// The branches of the tables do not go to the Towers with the mirror.
	if w, ok := o.Workspaces.(interface{ WithholdRefs(workspaces.RefFilter) }); ok {
		w.WithholdRefs(s.Withheld)
	}
	return s, nil
}

// Withheld says, for the replication of the mirror of a workspace, which refs
// stay on the Citadel: the branches of the tables (the ones in the database
// and anything under the prefix of the branches of the tables). They travel
// to the Towers only as the bundles of the tables, which are not sent for a
// table whose refs are not trusted, and which a Tower applies to its own
// branch, checked out in a worktree where the mirror could not write.
func (s *Service) Withheld(ctx context.Context, workspace string) (func(ref string) bool, error) {
	ts, err := s.store.List(ctx, workspace)
	if err != nil {
		return nil, err
	}
	branches := make(map[string]bool, len(ts))
	for _, t := range ts {
		branches["refs/heads/"+t.Branch] = true
	}
	prefix := s.o.Settings.BranchPrefix(workspace)
	return func(ref string) bool {
		return branches[ref] || (prefix != "" && strings.HasPrefix(ref, "refs/heads/"+prefix))
	}, nil
}

// Store gives access to the tables for the panel.
func (s *Service) Store() Store { return s.store }

var (
	_ characters.Tables   = (*Service)(nil)
	_ inbox.Pauses        = (*Service)(nil)
	_ characters.Observer = (*Service)(nil)
)

// lock takes the lock of a table (or of a workspace, with table "").
func (s *Service) lock(workspace, table string) func() {
	key := workspace + "/" + table
	s.mu.Lock()
	l, ok := s.locks[key]
	if !ok {
		l = &sync.Mutex{}
		s.locks[key] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (s *Service) gate(what string) error {
	if s.o.Gate == nil {
		return nil
	}
	if err := s.o.Gate(); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// Get returns a table by its id.
func (s *Service) Get(ctx context.Context, workspace, id string) (Table, error) {
	return s.store.Get(ctx, workspace, id)
}

// Find returns a table by its id or by the number of its epic ("42").
func (s *Service) Find(ctx context.Context, workspace, name string) (Table, error) {
	if t, err := s.store.Get(ctx, workspace, name); err == nil {
		return t, nil
	} else if !errors.Is(err, ErrNoTable) {
		return Table{}, err
	}
	var epic int
	if _, err := fmt.Sscanf(name, "%d", &epic); err == nil && fmt.Sprint(epic) == name {
		if t, ok, err := s.store.ByEpic(ctx, workspace, epic); err != nil || ok {
			return t, err
		}
	}
	return Table{}, fmt.Errorf("%w: %s", ErrNoTable, name)
}

// List returns the tables of a workspace.
func (s *Service) List(ctx context.Context, workspace string) ([]Table, error) {
	return s.store.List(ctx, workspace)
}

// Table implements characters.Tables.
func (s *Service) Table(ctx context.Context, workspace, id string) (characters.TableInfo, error) {
	t, err := s.store.Get(ctx, workspace, id)
	if err != nil {
		return characters.TableInfo{}, err
	}
	wts, err := s.store.Worktrees(ctx, workspace, id)
	if err != nil {
		return characters.TableInfo{}, err
	}
	info := characters.TableInfo{ID: t.ID, Workspace: workspace, State: t.State, Epic: t.Epic, Branch: t.Branch, Worktrees: map[string]string{}}
	for _, w := range wts {
		info.Worktrees[w.Machine] = w.Dir
	}
	return info, nil
}

// Worktree implements characters.Tables: the folder the characters of a table
// work in on a machine. On a Tower that has none yet it is made first, with a
// bundle of the branch of the table; the table "" is the main clone.
func (s *Service) Worktree(ctx context.Context, workspace, table, machine string) (string, error) {
	if table == "" {
		if machine != s.o.Machine {
			return "", fmt.Errorf("o clone principal só existe na Cidadela, e %s não é a Cidadela", machine)
		}
		return s.o.Workspaces.Layout().Principal(workspace), nil
	}
	if machine == s.o.Machine {
		w, err := s.store.Worktree(ctx, workspace, table, machine)
		if err != nil {
			return "", err
		}
		return w.Dir, nil
	}
	// A Tower gets the tip of the Citadel before each character starts there.
	return s.ensureTower(ctx, workspace, table, machine)
}

// OpenRequest is `mestre table open`.
type OpenRequest struct {
	// By is the Grand Master that asked.
	By    characters.Character
	Epic  int
	Sheet string
	// BaseBranch defaults to the main branch of the workspace.
	BaseBranch string
	// FromMessage is the message of the user the request comes from: the
	// engine copies its text and attachments to the first message of the
	// table master.
	FromMessage string
	// Notes are the observations of the Grand Master, after the original.
	Notes string
}

// OpenResult is what Open answers.
type OpenResult struct {
	Table    Table
	Worktree string
	Master   characters.Character
}

// Open opens the table of an epic (docs/orquestracao/mesas.md#abertura): it
// checks the workspace and the epic, makes the branch from origin/<main> and
// the worktree, runs the session zero of the table, recruits the table
// master and hands it the request. If a step fails the ones before it are
// undone and the error goes to the Grand Master; the epic is not touched.
func (s *Service) Open(ctx context.Context, req OpenRequest) (res OpenResult, err error) {
	gm, err := s.grandMaster(ctx, req.By)
	if err != nil {
		return OpenResult{}, err
	}
	ws := gm.Workspace
	if err := s.gate("table open"); err != nil {
		return OpenResult{}, err
	}
	if err := s.o.Workspaces.CheckOpenTable(ctx, ws); err != nil {
		return OpenResult{}, err
	}
	defer s.lock(ws, "")()

	epic, err := s.o.GitHub.Epic(ctx, ws, req.Epic)
	switch {
	case err != nil:
		return OpenResult{}, err
	case !epic.HasLabel(EpicLabel):
		return OpenResult{}, fmt.Errorf("%w: #%d não tem o rótulo `%s`", ErrEpicNotFound, req.Epic, EpicLabel)
	case !epic.Open:
		return OpenResult{}, fmt.Errorf("%w: #%d", ErrEpicClosed, req.Epic)
	}
	if t, ok, err := s.store.ByEpic(ctx, ws, req.Epic); err != nil {
		return OpenResult{}, err
	} else if ok {
		return OpenResult{}, fmt.Errorf("%w: #%d é da mesa %s", ErrTableExists, req.Epic, t.ID)
	}
	w, err := s.o.Workspaces.Get(ctx, ws)
	if err != nil {
		return OpenResult{}, err
	}
	base := req.BaseBranch
	if base == "" {
		base = w.DefaultBranch
	}
	if !validBranch(base) {
		return OpenResult{}, fmt.Errorf("a branch base %q não é válida", base)
	}
	origin, hasOrigin, err := s.origin(ctx, ws, req.FromMessage)
	if err != nil {
		return OpenResult{}, err
	}

	if err := s.o.Workspaces.Fetch(ctx, ws); err != nil {
		return OpenResult{}, fmt.Errorf("o espelho não foi atualizado antes de abrir a mesa: %w", err)
	}
	mirror, err := s.o.Workspaces.OpenMirror(ctx, ws)
	if err != nil {
		return OpenResult{}, err
	}
	start, err := mirror.ResolveRef(ctx, "refs/remotes/origin/"+base)
	if err != nil {
		if errors.Is(err, gitx.ErrRefNotFound) {
			return OpenResult{}, fmt.Errorf("a branch base origin/%s não existe no espelho: %w", base, err)
		}
		return OpenResult{}, err
	}
	// Nobody moves a ref by itself: the opening also looks at the others.
	if _, err := s.CheckRefs(ctx, ws); err != nil {
		s.log.Warn("tables: the check of the refs failed", "workspace", ws, "err", err)
	}

	now := s.clk.Now()
	id, err := s.freeID(ctx, ws, ID(epic.Number, epic.Title))
	if err != nil {
		return OpenResult{}, err
	}
	prefix := s.o.Settings.BranchPrefix(ws)
	branch := prefix + id
	if !validBranch(branch) || strings.HasPrefix(branch, "refs/") || strings.HasPrefix(branch, "mestre/") {
		return OpenResult{}, fmt.Errorf("o prefixo de branch %q do workspace não gera um nome de branch válido (%q)", prefix, branch)
	}
	t := Table{Workspace: ws, ID: id, Epic: epic.Number, EpicTitle: epic.Title, Branch: branch, BaseBranch: base, BaseCommit: string(start),
		State: StateOpen, OpenedBy: gm.ID, CreatedAt: now, UpdatedAt: now}
	if err := s.store.Insert(ctx, t); err != nil {
		return OpenResult{}, err
	}
	undo := &undoList{}
	defer func() {
		if err != nil {
			undo.run(context.WithoutCancel(ctx), s.log)
			if derr := s.store.Delete(context.WithoutCancel(ctx), ws, id); derr != nil {
				s.log.Warn("tables: a table that did not open was not removed", "table", id, "err", derr)
			}
			res = OpenResult{}
		}
	}()

	if err = mirror.CreateBranch(ctx, branch, string(start)); err != nil {
		return OpenResult{}, fmt.Errorf("a branch %s não foi criada: %w", branch, err)
	}
	undo.add("branch", func(ctx context.Context) error { return mirror.DeleteBranch(ctx, branch) })

	dir := filepath.Join(s.o.Workspaces.Layout().Tables(ws), id)
	if _, err = mirror.AddWorktree(ctx, dir, gitx.WorktreeOptions{Branch: branch}); err != nil {
		return OpenResult{}, fmt.Errorf("o worktree da mesa não foi criado: %w", err)
	}
	undo.add("worktree", func(ctx context.Context) error { return mirror.RemoveWorktree(ctx, dir, true) })
	rec, err := s.o.Git.Record(ctx, dir)
	if err != nil {
		return OpenResult{}, fmt.Errorf("o registro do worktree da mesa: %w", err)
	}
	if err = s.store.PutWorktree(ctx, Worktree{Workspace: ws, Table: id, Machine: s.o.Machine, Dir: rec.Dir, GitDir: rec.GitDir, Common: rec.Common,
		LastCommit: string(start), CreatedAt: now, UpdatedAt: now}); err != nil {
		return OpenResult{}, err
	}
	tr, err := s.tableRequest(ctx, t, rec)
	if err != nil {
		return OpenResult{}, err
	}
	if _, err = s.o.Files.Prepare(ctx, tr); err != nil {
		return OpenResult{}, fmt.Errorf("a sessão zero da mesa: %w", err)
	}
	undo.add("session zero", func(context.Context) error { return s.o.Files.Release(tr) })

	var files []string
	if hasOrigin && len(origin.Attachments) > 0 && s.o.Attachments != nil {
		if err = s.o.Attachments.Forward(ctx, ws, id, origin.Attachments); err != nil {
			return OpenResult{}, fmt.Errorf("os anexos não foram repassados: %w", err)
		}
		// The session zero copies them into the worktree.
		if _, err = s.o.Files.Prepare(ctx, tr); err != nil {
			return OpenResult{}, fmt.Errorf("a sessão zero da mesa: %w", err)
		}
		for _, f := range origin.Attachments {
			files = append(files, ".mestre/anexos/"+f.Path)
		}
	}

	sheet := req.Sheet
	if sheet == "" {
		sheet = s.o.Settings.TableMasterSheet(ws)
	}
	cr, err := s.o.Characters.Create(ctx, characters.CreateRequest{Sheet: sheet, Workspace: ws, Table: id, Role: characters.RoleTableMaster,
		Creator: characters.Creator{Kind: characters.CreatedByCharacter, ID: gm.ID}})
	if err != nil {
		return OpenResult{}, fmt.Errorf("o Mestre da mesa não foi recrutado: %w", err)
	}
	master := cr.Character
	if master.ID == "" {
		return OpenResult{}, fmt.Errorf("o Mestre da mesa ficou esperando uma vaga (%s); uma mesa não abre sem ele", cr.Pending)
	}
	undo.add("table master", func(ctx context.Context) error {
		_ = s.o.Registry.SetPinned(ctx, master.ID, false)
		_, err := s.o.Characters.Dismiss(ctx, characters.DismissRequest{ID: master.ID, By: characters.Creator{Kind: characters.CreatedByEngine}, Reason: "table did not open"})
		return err
	})
	if err = s.o.Registry.SetPinned(ctx, master.ID, true); err != nil {
		return OpenResult{}, err
	}

	// A table whose epic waits for another has its request held in the queue
	// (Paused) until the other is merged.
	if s.o.Dependencies != nil {
		if err = s.o.Dependencies.Opened(ctx, ws, t); err != nil {
			return OpenResult{}, fmt.Errorf("a espera da mesa por outro épico: %w", err)
		}
		undo.add("dependencies", func(ctx context.Context) error { return s.o.Dependencies.Dropped(ctx, ws, t) })
	}
	item := inbox.NewItem{Workspace: ws, Target: master.ID, Kind: inbox.KindAgent, Sender: gm.Name, SenderID: gm.ID,
		Text: firstMessage(epic, origin, hasOrigin, req.Notes), Attachments: files, Refs: inbox.Refs{Table: id}}
	if hasOrigin {
		o := origin.Origin
		o.Table = id
		item.Kind, item.Origin, item.Sender, item.SenderID = inbox.KindUser, &o, o.UserName, ""
	}
	if _, err = s.o.Inbox.Enqueue(ctx, item); err != nil {
		return OpenResult{}, fmt.Errorf("o pedido não chegou ao Mestre da mesa: %w", err)
	}
	got, err := s.store.Get(ctx, ws, id)
	if err != nil {
		return OpenResult{}, err
	}
	master, _ = s.o.Registry.Get(ctx, master.ID)
	return OpenResult{Table: got, Worktree: rec.Dir, Master: master}, nil
}

// origin reads the message the request comes from, if one is named.
func (s *Service) origin(ctx context.Context, workspace, id string) (Message, bool, error) {
	if id == "" {
		return Message{}, false, nil
	}
	if s.o.Messages == nil {
		return Message{}, false, fmt.Errorf("a mensagem %s não pode ser lida: o canal dos usuários não está ligado", id)
	}
	m, err := s.o.Messages.Message(ctx, workspace, id)
	if err != nil {
		return Message{}, false, fmt.Errorf("a mensagem %s: %w", id, err)
	}
	return m, true, nil
}

// firstMessage is the first message of the table master: the original text of
// the user, untouched, and the observations of the Grand Master after it, in a
// block of their own, quoted: they are text of an agent under the header of the
// user.
func firstMessage(epic Epic, m Message, has bool, notes string) string {
	var b strings.Builder
	if has {
		b.WriteString(m.Text)
	} else {
		fmt.Fprintf(&b, "A mesa foi aberta para o épico #%d: %s.", epic.Number, epic.Title)
	}
	if notes != "" {
		b.WriteString("\n\n--- Observações do Grão-Mestre (texto do agente, não verificado; cada linha começa com \"| \") ---\n")
		// the message is delivered under the header of the user, whose text is not
		// escaped, so the notes, which the Grão-Mestre wrote, are quoted: no line of
		// them can pass for the user's, for a header or for the end of the block
		b.WriteString(inbox.Quote(notes))
	}
	return b.String()
}

// tableRequest is what the session zero of the table needs for the Citadel's
// worktree of a table.
func (s *Service) tableRequest(ctx context.Context, t Table, rec gitx.WorktreeRecord) (sessionzero.TableRequest, error) {
	repo, err := s.o.Git.OpenWorktree(ctx, rec)
	if err != nil {
		return sessionzero.TableRequest{}, err
	}
	req := sessionzero.TableRequest{Workspace: t.Workspace, Table: t.ID, Dir: rec.Dir, Repo: repo, RepoRoot: rec.Common,
		ExcludeMestre: s.o.Settings.ExcludeMestre(t.Workspace)}
	if s.o.Profiles != nil {
		req.Profiles = s.o.Profiles.Profiles(ctx, s.o.Machine)
	}
	return req, nil
}

// worktreeRepo opens the worktree of a table on this machine by its record.
func (s *Service) worktreeRepo(ctx context.Context, w Worktree) (gitx.Repo, error) {
	return s.o.Git.OpenWorktree(ctx, gitx.WorktreeRecord{Dir: w.Dir, GitDir: w.GitDir, Common: w.Common})
}

// grandMaster reads the Grand Master that asks from the registry: the one the
// request carries may have stopped since it was read.
func (s *Service) grandMaster(ctx context.Context, by characters.Character) (characters.Character, error) {
	if by.Role != characters.RoleGrandMaster || by.ID == "" {
		return characters.Character{}, ErrNotGrandMaster
	}
	cur, err := s.o.Registry.Get(ctx, by.ID)
	if err != nil {
		if errors.Is(err, characters.ErrNoCharacter) {
			return characters.Character{}, ErrNotGrandMaster
		}
		return characters.Character{}, err
	}
	if cur.Role != characters.RoleGrandMaster || !cur.State.Alive() || cur.Workspace == "" {
		return characters.Character{}, ErrNotGrandMaster
	}
	return cur, nil
}

// freeID is the id for the table of an epic: the one the epic and its title
// make, or, when a table that is over already has it (the epic was reopened
// after a cancellation or a closing), the same with a number after it
// ("42-login-2"), so the old table and its branch are left as they are.
func (s *Service) freeID(ctx context.Context, workspace, id string) (string, error) {
	for n := 1; n < 1000; n++ {
		cand := id
		if n > 1 {
			cand = fmt.Sprintf("%s-%d", id, n)
		}
		switch _, err := s.store.Get(ctx, workspace, cand); {
		case errors.Is(err, ErrNoTable):
			return cand, nil
		case err != nil:
			return "", err
		}
	}
	return "", fmt.Errorf("%w: o épico já foi aberto vezes demais", ErrTableExists)
}

// validBranch is a conservative branch name: no option, no revision syntax.
func validBranch(b string) bool {
	if b == "" || b[0] == '-' || b[0] == '/' || b[len(b)-1] == '/' || b[len(b)-1] == '.' {
		return false
	}
	for i := range len(b) {
		c := b[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c == '/'
		if !ok || (i > 0 && c == '.' && b[i-1] == '.') || (i > 0 && c == '/' && b[i-1] == '/') {
			return false
		}
	}
	return true
}

// undoList undoes the steps of an opening, the last one first.
type undoList struct{ steps []undoStep }

type undoStep struct {
	what string
	fn   func(ctx context.Context) error
}

func (u *undoList) add(what string, fn func(ctx context.Context) error) {
	u.steps = append(u.steps, undoStep{what, fn})
}

func (u *undoList) run(ctx context.Context, log *slog.Logger) {
	for i := len(u.steps) - 1; i >= 0; i-- {
		if err := u.steps[i].fn(ctx); err != nil {
			log.Warn("tables: a step of the opening was not undone", "step", u.steps[i].what, "err", err)
		}
	}
}
