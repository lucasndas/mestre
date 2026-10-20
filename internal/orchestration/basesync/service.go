package basesync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

// QuestName is the name of the mission of the step `sync`; a later one in the
// same table is "sync-2", and so on (a cancelled mission keeps its name).
const QuestName = "sync"

// Tables is what the base sync needs of the tables (*tables.Service).
type Tables interface {
	Get(ctx context.Context, workspace, id string) (tables.Table, error)
	List(ctx context.Context, workspace string) ([]tables.Table, error)
	Paused(ctx context.Context, workspace, table string) (bool, error)
}

// Mirrors opens the mirror of a workspace (*workspaces.Service).
type Mirrors interface {
	OpenMirror(ctx context.Context, slug string) (gitx.Repo, error)
}

// Quests is what it needs of the quests (*quests.Service).
type Quests interface {
	Create(ctx context.Context, r quests.CreateRequest) (quests.View, error)
	Run(ctx context.Context, id, input string) (quests.Quest, error)
	Store() *quests.Store
}

// Inbox is the inbox of the characters (*inbox.Service).
type Inbox interface {
	Enqueue(ctx context.Context, n inbox.NewItem) (inbox.Item, error)
}

// Options are the pieces of the Service.
type Options struct {
	Tables   Tables
	Mirrors  Mirrors
	Quests   Quests
	Registry characters.Registry
	Inbox    Inbox
	// Notifier reaches the Grand Master when the table has no Master alive.
	Notifier characters.Notifier
	// Requests is told the tip of the base of each table at every fetch, so a
	// request of a PR whose gate ran on another tip ends (*chain.Service). Nil:
	// nobody is told.
	Requests Requests
	Logger   *slog.Logger
}

// Service creates the step `sync`.
type Service struct {
	o   Options
	log *slog.Logger

	mu sync.Mutex
	// asked remembers, by table, the tip of the base the Master was already
	// asked about when the table had nobody to do the step.
	asked map[string]string
}

var _ github.BaseGuard = (*Service)(nil)

// New returns the Service.
func New(o Options) (*Service, error) {
	switch {
	case o.Tables == nil || o.Mirrors == nil || o.Quests == nil || o.Inbox == nil:
		return nil, errors.New("basesync: the service needs the tables, the mirrors, the quests and the inbox")
	case o.Registry.DB == nil:
		return nil, errors.New("basesync: the service needs the registry of characters")
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{o: o, log: o.Logger, asked: map[string]string{}}, nil
}

// Outcome is what Ensure did.
type Outcome string

// The outcomes.
const (
	// UpToDate: the branch has the tip of the base.
	UpToDate Outcome = "up_to_date"
	// Created: the mission `sync` was made (or run again) and the Dev got it.
	Created Outcome = "created"
	// Open: a `sync` is already under way; the signal was ignored.
	Open Outcome = "open"
	// NoDev: the table has nobody to do it; the Master of the table was told.
	NoDev Outcome = "no_dev"
	// Held: the table does not take a step now (paused, not at work).
	Held Outcome = "held"
)

// Result is the answer of Ensure.
type Result struct {
	Outcome Outcome
	// Base is the tip of the base the branch lacks (empty when up to date).
	Base string
	// Quest is the mission of the step, when there is one.
	Quest string
}

// Behind says whether the branch of the table lacks the tip of the base in the
// mirror, and what that tip is. A branch whose refs cannot be read is an error:
// the caller never takes it for up to date.
func (s *Service) Behind(ctx context.Context, t tables.Table) (bool, string, error) {
	mirror, err := s.o.Mirrors.OpenMirror(ctx, t.Workspace)
	if err != nil {
		return false, "", err
	}
	tip, err := mirror.ResolveRef(ctx, "refs/remotes/origin/"+t.BaseBranch)
	if err != nil {
		return false, "", fmt.Errorf("the base origin/%s: %w", t.BaseBranch, err)
	}
	has, err := mirror.IsAncestor(ctx, string(tip), "refs/heads/"+t.Branch)
	if err != nil {
		return false, "", fmt.Errorf("the branch %s: %w", t.Branch, err)
	}
	return !has, string(tip), nil
}

// Moved says whether the branch of the table still lacks the tip of the base:
// the gate the step sync passes again before it ends (quests.Options.BaseMoved).
func (s *Service) Moved(ctx context.Context, workspace, table string) (bool, error) {
	t, err := s.o.Tables.Get(ctx, workspace, table)
	if err != nil {
		return false, err
	}
	behind, _, err := s.Behind(ctx, t)
	return behind, err
}

// AfterFetch is told that the mirror of a workspace was fetched: every table at
// work or in review that lacks the base gets its step `sync`.
func (s *Service) AfterFetch(ctx context.Context, slug string) {
	list, err := s.o.Tables.List(ctx, slug)
	if err != nil {
		s.log.Warn("basesync: the tables were not listed", "workspace", slug, "err", err)
		return
	}
	for _, t := range list {
		if t.State != tables.StateWorking && t.State != tables.StateInReview {
			continue
		}
		s.baseMoved(ctx, t)
		if _, err := s.Ensure(ctx, t.Workspace, t.ID); err != nil {
			s.log.Warn("basesync: the base of a table was not checked", "workspace", slug, "table", t.ID, "err", err)
		}
	}
}

// baseMoved tells the requests of the chain the tip of the base of the table:
// a gate that ran on another tip proved something that is no longer the merge.
func (s *Service) baseMoved(ctx context.Context, t tables.Table) {
	if s.o.Requests == nil {
		return
	}
	mirror, err := s.o.Mirrors.OpenMirror(ctx, t.Workspace)
	if err != nil {
		s.log.Warn("basesync: the mirror was not opened", "table", t.ID, "err", err)
		return
	}
	tip, err := mirror.ResolveRef(ctx, "refs/remotes/origin/"+t.BaseBranch)
	if err != nil {
		s.log.Warn("basesync: the base of a table was not read", "table", t.ID, "err", err)
		return
	}
	if err := s.o.Requests.BaseMoved(ctx, t.Workspace, t.ID, string(tip)); err != nil {
		s.log.Warn("basesync: the requests of a table were not checked against the base", "table", t.ID, "err", err)
	}
}

// Check is the gate of `mestre pr open` (github.BaseGuard): when the base moved
// the step `sync` is made (or is already under way) and the sending is refused
// with a *github.BaseMovedError. The key is "<workspace>/<table>".
func (s *Service) Check(ctx context.Context, tableKey string) error {
	ws, id, ok := strings.Cut(tableKey, "/")
	if !ok {
		return fmt.Errorf("basesync: %q is not a table key", tableKey)
	}
	r, err := s.Ensure(ctx, ws, id)
	if err != nil {
		return err
	}
	if r.Outcome == UpToDate {
		return nil
	}
	return &github.BaseMovedError{Base: r.Base, Detail: refusal(r)}
}

func refusal(r Result) string {
	switch r.Outcome {
	case Created, Open:
		return "a branch principal andou desde a abertura da mesa; a etapa sync (missão " + r.Quest + ") integra a base na branch da mesa"
	case NoDev:
		return "a branch principal andou desde a abertura da mesa e a mesa não tem quem faça a etapa sync: o Mestre da mesa foi avisado"
	}
	return "a branch principal andou desde a abertura da mesa; a etapa sync nasce quando a mesa puder receber uma etapa"
}

// Ensure makes the step `sync` of the table if its branch lacks the base and
// there is none under way. It is safe to call from every signal that the base
// moved: only one step is open per table. Callers must not hold a lock of the
// tables or of the workspace that the quests take (MissionStarted).
func (s *Service) Ensure(ctx context.Context, workspace, table string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.o.Tables.Get(ctx, workspace, table)
	if err != nil {
		return Result{}, err
	}
	if tables.Over(t.State) {
		return Result{Outcome: UpToDate}, nil
	}
	behind, tip, err := s.Behind(ctx, t)
	if err != nil {
		return Result{}, err
	}
	if !behind {
		return Result{Outcome: UpToDate}, nil
	}
	res := Result{Base: tip}
	existing, err := s.syncs(ctx, t)
	if err != nil {
		return Result{}, err
	}
	for _, q := range existing {
		if q.State == quests.Draft || q.State == quests.Running || q.State == quests.Paused {
			res.Outcome, res.Quest = Open, q.Name
			return res, nil
		}
	}
	paused, err := s.o.Tables.Paused(ctx, workspace, table)
	if err != nil {
		return Result{}, err
	}
	if paused || (t.State != tables.StateWorking && t.State != tables.StateInReview) {
		res.Outcome = Held
		return res, nil
	}
	dev, ok, err := s.dev(ctx, t)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		res.Outcome = NoDev
		s.askMaster(ctx, t, tip)
		return res, nil
	}
	res.Outcome = Created
	if n := len(existing); n > 0 && existing[n-1].State == quests.Completed {
		// the mission of the last time runs again, in a new round
		last := existing[n-1]
		if _, err := s.o.Quests.Run(ctx, last.ID, ""); err != nil {
			return Result{}, err
		}
		res.Quest = last.Name
	} else {
		name := QuestName
		if len(existing) > 0 {
			name = fmt.Sprintf("%s-%d", QuestName, len(existing)+1)
		}
		def := quests.Definition{Name: name, Steps: []quests.StepDef{{ID: "sync", Name: "sync", Character: dev.Name, Instructions: instructions(t)}}}
		v, err := s.o.Quests.Create(ctx, quests.CreateRequest{Workspace: workspace, Table: table, Def: &def})
		if err != nil {
			return Result{}, err
		}
		if _, err := s.o.Quests.Run(ctx, v.ID, ""); err != nil {
			return Result{}, err
		}
		res.Quest = name
	}
	s.tellMaster(ctx, t, createdText(t, res.Quest, dev.Name))
	return res, nil
}

// syncs lists the missions of the step sync of the table, oldest first.
func (s *Service) syncs(ctx context.Context, t tables.Table) ([]quests.Quest, error) {
	all, err := s.o.Quests.Store().OfTable(ctx, t.Workspace, t.ID)
	if err != nil {
		return nil, err
	}
	var out []quests.Quest
	for _, q := range all {
		if isSyncName(q.Name) {
			out = append(out, q)
		}
	}
	slices.SortFunc(out, func(a, b quests.Quest) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func isSyncName(name string) bool { return quests.IsSyncName(name) }

// dev finds the Dev of the table: the character that did the last step that
// changed code and is still alive.
func (s *Service) dev(ctx context.Context, t tables.Table) (characters.Character, bool, error) {
	all, err := s.o.Quests.Store().OfTable(ctx, t.Workspace, t.ID)
	if err != nil {
		return characters.Character{}, false, err
	}
	var runs []quests.Run
	for _, q := range all {
		steps, err := s.o.Quests.Store().Steps(ctx, q.ID)
		if err != nil {
			return characters.Character{}, false, err
		}
		for _, st := range steps {
			rs, err := s.o.Quests.Store().Runs(ctx, st.UID)
			if err != nil {
				return characters.Character{}, false, err
			}
			for _, r := range rs {
				if len(r.Files) > 0 && r.Character != "" && !r.EndedAt.IsZero() {
					runs = append(runs, r)
				}
			}
		}
	}
	slices.SortFunc(runs, func(a, b quests.Run) int { return b.EndedAt.Compare(a.EndedAt) })
	for _, r := range runs {
		c, err := s.o.Registry.Get(ctx, r.Character)
		if err != nil {
			if errors.Is(err, characters.ErrNoCharacter) {
				continue
			}
			return characters.Character{}, false, err
		}
		if c.Role == characters.RoleCharacter && c.State.Alive() && c.Table == t.ID {
			return c, true, nil
		}
	}
	return characters.Character{}, false, nil
}

// askMaster tells the Master of the table, once per tip of the base, that the
// base moved and nobody can do the step.
func (s *Service) askMaster(ctx context.Context, t tables.Table, tip string) {
	key := t.Workspace + "/" + t.ID
	if s.asked[key] == tip {
		return
	}
	s.asked[key] = tip
	s.tellMaster(ctx, t, noDevText(t))
}

// tellMaster puts a system item in the inbox of the Master of the table;
// without one alive, the Grand Master is told.
func (s *Service) tellMaster(ctx context.Context, t tables.Table, text string) {
	m, ok, err := s.o.Registry.TableMaster(ctx, t.Workspace, t.ID)
	if err == nil && ok && m.State.Alive() {
		if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: t.Workspace, Target: m.ID, Kind: inbox.KindSystem, Sender: inbox.SystemSender, Text: text,
			Refs: inbox.Refs{Table: t.ID}}); err != nil {
			s.log.Warn("basesync: the Master of the table was not told", "table", t.ID, "err", err)
		}
		return
	}
	if s.o.Notifier != nil {
		if err := s.o.Notifier.Notify(ctx, characters.Notice{Kind: characters.NoticeSystem, To: characters.Recipient{Kind: characters.ToGrandMaster},
			Workspace: t.Workspace, Table: t.ID, Text: text}); err != nil {
			s.log.Warn("basesync: the Grand Master was not told", "table", t.ID, "err", err)
		}
	}
}
