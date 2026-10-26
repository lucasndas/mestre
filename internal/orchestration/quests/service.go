package quests

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/connections"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/orchestration/scrolls"
	"github.com/mestre-ia/mestre/internal/orchestration/waits"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// DefaultEscalateAfter is how long a stuck step waits for the Master of the
// table before the Grand Master and the users are told.
const DefaultEscalateAfter = 30 * time.Minute

// Options are the pieces of the Service.
type Options struct {
	Store    *Store
	Clock    clock.Clock
	Registry characters.Registry
	// Machine is the id of the Citadel: a character that runs there shares
	// its worktree; one that runs on another machine is on a Tower.
	Machine string

	Characters  Characters
	Inbox       Inbox
	Notifier    characters.Notifier
	Scrolls     Scrolls
	Connections Connections
	Tables      Tables
	Waits       Waits
	// Catalog checks sheets and classes when a quest is made (nil: not
	// checked).
	Catalog Catalog
	// Towers, GitHub and Approvals may be nil: there is no Tower, GitHub is
	// not mirrored, and a quest with a point of approval pauses when it gets
	// to it.
	Towers    Towers
	GitHub    GitHub
	Approvals Approvals
	// Issues checks that the `issue` of a new mission is one of the table
	// (nil: not checked); MaxCorrections is how many fix missions an epic may
	// have (zero: DefaultMaxCorrections).
	Issues         IssueOwner
	MaxCorrections int
	// Done gives the done_requires of the classes (nil: none asks for a proof).
	Done DoneRules
	// Verifier runs the verification of the workspace for a class with verify:
	// true (nil: nothing is confirmed, so the step ends unverified).
	Verifier Verifier
	// BaseMoved says whether the base of the table moved past its branch
	// (basesync): the step sync asks it again when its verification is green,
	// and does not end while it is true. Nil: no such gate.
	BaseMoved func(ctx context.Context, workspace, table string) (bool, error)
	// Held says whether the table waits for the merge of another one
	// (docs/orquestracao/dependencia-entre-mesas.md): no step begins until it is
	// merged. nil: no table waits.
	Held func(ctx context.Context, workspace, table string) (bool, error)
	// Completed is told when a mission ended with all its steps done (the
	// message of progress goes out); nil: nobody is.
	Completed func(ctx context.Context, workspace, table, quest string)
	// Delivered is told when a step ended with its result accepted: useful
	// work landed on the table (nil: nobody is).
	Delivered func(ctx context.Context, workspace, table string)
	// EscalateAfter defaults to DefaultEscalateAfter.
	EscalateAfter time.Duration
	// Gate is asked before the outside is touched (GitHub); an error means
	// this Citadel may not act there now.
	Gate   func() error
	Logger *slog.Logger
}

// Service conducts the quests of the tables
// (docs/orquestracao/missoes.md): the engine, never an agent that remembers,
// hands each step to its character, collects its done, passes the baton and
// ends the quest. Everything it decides is kept in the database, so it goes on
// where it was after a restart.
type Service struct {
	o   Options
	st  *Store
	clk clock.Clock
	log *slog.Logger

	// flights stops two advances of a quest from running at once; an advance
	// asked meanwhile runs again when the first ends.
	mu      sync.Mutex
	flights map[string]*flight
	// finishing serializes the done of a character: two dones of the same
	// character at once record one.
	finishing map[string]*sync.Mutex
	// verifs are the steps whose verification runs; vwg waits for them.
	verifs map[string]bool
	vwg    sync.WaitGroup
	// reds is the last verification that failed in each table, by
	// "<workspace>/<table>", until one passes: what the engine has to show as
	// the evidence of a notice about that table (LastRed).
	reds map[string]VerifyResult

	// work is the queue of the loop (Run); until it runs, an advance asked for
	// is made at once, in the call that asked for it.
	async atomic.Bool
	work  chan string
}

type flight struct{ again bool }

var (
	_ characters.Observer = (*Service)(nil)
	_ waits.Quests        = (*Service)(nil)
	_ scrolls.QuestNames  = (*Service)(nil)
)

// NewService returns the Service.
func NewService(o Options) (*Service, error) {
	switch {
	case o.Store == nil || o.Store.DB == nil:
		return nil, errors.New("quests: the service needs the store")
	case o.Registry.DB == nil:
		return nil, errors.New("quests: the service needs the registry of characters")
	case o.Characters == nil || o.Inbox == nil || o.Scrolls == nil || o.Tables == nil:
		return nil, errors.New("quests: the service needs the characters, the inbox, the scrolls and the tables")
	case o.Machine == "":
		return nil, errors.New("quests: the service needs the id of the Citadel")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.EscalateAfter <= 0 {
		o.EscalateAfter = DefaultEscalateAfter
	}
	return &Service{o: o, st: o.Store, clk: o.Clock, log: o.Logger, flights: map[string]*flight{}, finishing: map[string]*sync.Mutex{}, verifs: map[string]bool{}, reds: map[string]VerifyResult{}, work: make(chan string, 256)}, nil
}

// Store gives access to the tables.
func (s *Service) Store() *Store { return s.st }

// --- what the other parts ask ---

// QuestName implements scrolls.QuestNames.
func (s *Service) QuestName(ctx context.Context, id string) (string, bool) {
	q, err := s.st.Quest(ctx, id)
	if err != nil {
		return "", false
	}
	return q.Name, true
}

// Quest implements waits.Quests: the quest of a table by its name, with its
// steps.
func (s *Service) Quest(ctx context.Context, workspace, table, name string) (waits.QuestInfo, bool, error) {
	q, ok, err := s.st.ByName(ctx, workspace, table, name)
	if err != nil || !ok {
		return waits.QuestInfo{}, false, err
	}
	steps, err := s.st.Steps(ctx, q.ID)
	if err != nil {
		return waits.QuestInfo{}, false, err
	}
	info := waits.QuestInfo{ID: q.ID, Name: q.Name}
	for _, st := range steps {
		info.Steps = append(info.Steps, waits.StepInfo{ID: st.UID, Name: q.Def.Steps[st.Pos].Title()})
	}
	return info, true, nil
}

// Responsible implements waits.Quests: the character that has the step now,
// where an arrow of the graph of waits points to.
func (s *Service) Responsible(ctx context.Context, stepID string) (string, error) {
	st, err := s.st.Step(ctx, stepID)
	if err != nil {
		return "", nil
	}
	if st.State == StepPreparing || st.State.Active() {
		return st.Character, nil
	}
	return "", nil
}

// Get returns a quest of a table by its name or its id.
func (s *Service) Get(ctx context.Context, workspace, table, nameOrID string) (Quest, error) {
	if q, ok, err := s.st.ByName(ctx, workspace, table, nameOrID); err != nil || ok {
		return q, err
	}
	q, err := s.st.Quest(ctx, nameOrID)
	if err != nil || q.Workspace != workspace || q.Table != table {
		return Quest{}, fmt.Errorf("%w: %s", ErrNoQuest, nameOrID)
	}
	return q, nil
}

// StepView is a step with its definition and what it waits for.
type StepView struct {
	Step
	Def StepDef
	// Waiting is what holds a step that is being prepared, in words.
	Waiting string
}

// View is a quest with its steps.
type View struct {
	Quest
	Steps []StepView
}

// Status returns a quest with the state of its steps (`quest status`).
func (s *Service) Status(ctx context.Context, q Quest) (View, error) {
	steps, err := s.st.Steps(ctx, q.ID)
	if err != nil {
		return View{}, err
	}
	v := View{Quest: q}
	for _, st := range steps {
		sv := StepView{Step: st, Def: q.Def.Steps[st.Pos]}
		switch {
		case st.Blocker == blockBusy:
			sv.Waiting = "o personagem está em outra etapa"
		case strings.HasPrefix(st.Blocker, blockApproval):
			sv.Waiting = "aprovação " + strings.TrimPrefix(st.Blocker, blockApproval)
		case strings.HasPrefix(st.Blocker, blockSlot):
			sv.Waiting = "vaga para o personagem"
		case st.Blocker == blockSync:
			sv.Waiting = "sincronização da branch"
		case st.Blocker == blockResume:
			sv.Waiting = "retomada do personagem"
		case st.State == StepApproving:
			sv.Waiting = "aprovação " + fmt.Sprint(st.Approval)
		}
		v.Steps = append(v.Steps, sv)
	}
	return v, nil
}

// List returns the quests of a table.
func (s *Service) List(ctx context.Context, workspace, table string) ([]Quest, error) {
	return s.st.OfTable(ctx, workspace, table)
}

// --- making a quest ---

// CreateRequest is `mestre quest create`.
type CreateRequest struct {
	Workspace, Table string
	// Data is the definition in YAML or JSON; Def is it already read (Data is
	// ignored then).
	Data []byte
	Def  *Definition
	// Designer is the character that makes the quest (the Master of the
	// table); "" for a user on the canvas.
	Designer string
}

// Create validates a definition and makes the quest as a draft: the steps
// pending, the scroll of the quest, the connections of the characters that
// already exist, and the issue (and sub-issues) on GitHub. Nothing runs until
// Run.
func (s *Service) Create(ctx context.Context, r CreateRequest) (View, error) {
	var def Definition
	if r.Def != nil {
		def = *r.Def
	} else {
		var err error
		if def, err = Parse(r.Data); err != nil {
			return View{}, err
		}
	}
	if err := def.Validate(); err != nil {
		return View{}, err
	}
	if err := s.check(ctx, r.Workspace, r.Table, def); err != nil {
		return View{}, err
	}
	if err := s.checkIssue(ctx, r.Workspace, r.Table, def.Issue); err != nil {
		return View{}, err
	}
	now := s.clk.Now()
	q := Quest{Workspace: r.Workspace, Table: r.Table, Name: def.Name, State: Draft, Def: def, MaxReturns: def.Max(), Designer: r.Designer, Issue: def.Issue,
		Dirty: 1, CreatedAt: now, UpdatedAt: now}
	steps := make([]Step, len(def.Steps))
	for i, sd := range def.Steps {
		steps[i] = Step{ID: sd.ID, Pos: i, State: StepPending, UpdatedAt: now}
	}
	err := s.st.Do(ctx, func(tx *Tx) error {
		var err error
		if def.Issue != 0 {
			all, err := tx.OfTable(q.Workspace, q.Table)
			if err != nil {
				return err
			}
			if err := s.checkCorrections(all, def.Issue); err != nil {
				return err
			}
		}
		q, err = tx.Insert(q, steps)
		return err
	})
	if err != nil {
		return View{}, err
	}
	note, err := s.o.Scrolls.Ensure(ctx, q.Workspace, q.Table, scrolls.QuestScrollName(q.Name), "# Missão: "+q.Name+"\n")
	if err != nil {
		s.log.Warn("quests: the scroll of the quest was not made", "quest", q.ID, "err", err)
	} else {
		q.Scroll = note.Key
		if err := s.st.Do(ctx, func(tx *Tx) error { return tx.SaveQuest(q, now) }); err != nil {
			return View{}, err
		}
	}
	s.ensureReview(ctx, q)
	s.link(ctx, q)
	s.mirror(ctx, q.ID)
	q, _ = s.st.Quest(ctx, q.ID)
	return s.Status(ctx, q)
}

// check looks at what a definition names: the sheets, their classes against
// the ceiling, and the characters of the table.
func (s *Service) check(ctx context.Context, workspace, table string, def Definition) error {
	t, err := s.o.Tables.Table(ctx, workspace, table)
	if err != nil {
		return err
	}
	if !t.State.Accepts() {
		return fmt.Errorf("%w: a mesa %s está %s", characters.ErrTableClosed, table, t.State)
	}
	var ps []Problem
	for _, sd := range def.Steps {
		if sd.Character != "" {
			if c, err := s.o.Registry.ByName(ctx, workspace, table, sd.Character); err != nil || !c.State.Alive() || c.Role != characters.RoleCharacter {
				ps = append(ps, Problem{Step: sd.ID, Message: fmt.Sprintf("the character %q does not exist in the table", sd.Character)})
			}
			continue
		}
		if s.o.Catalog == nil {
			continue
		}
		sheet, ok, err := s.o.Catalog.Sheet(ctx, sd.Sheet)
		if err != nil {
			return err
		}
		if !ok {
			ps = append(ps, Problem{Step: sd.ID, Message: fmt.Sprintf("the sheet %q does not exist", sd.Sheet)})
			continue
		}
		class := sd.Class
		if class == "" {
			class = sheet.Class
		}
		if class == "" {
			continue
		}
		if err := s.o.Catalog.ClassAllowed(ctx, workspace, class); err != nil {
			ps = append(ps, Problem{Step: sd.ID, Message: fmt.Sprintf("the class %q is above the ceiling of the workspace: %v", class, err)})
		}
	}
	if len(ps) > 0 {
		return &ValidationError{Problems: ps}
	}
	return nil
}

// --- conducting it ---

// Run puts a quest in running: a draft starts its first round, a completed
// quest a new one (the steps go back to pending). input is what the quest was
// run with.
func (s *Service) Run(ctx context.Context, id, input string) (Quest, error) {
	q, err := s.st.Quest(ctx, id)
	if err != nil {
		return Quest{}, err
	}
	if q.State != Draft && q.State != Completed {
		return Quest{}, fmt.Errorf("%w: %s is %s", ErrInvalidState, q.Name, q.State)
	}
	// the criteria are read from the issue before the round starts and frozen
	// for it; an issue that cannot be read refuses the run, which is tried again
	crit, err := s.criteriaOf(ctx, q)
	if err != nil {
		return Quest{}, err
	}
	if _, err := s.o.Tables.MissionStarted(ctx, q.Workspace, q.Table); err != nil {
		return Quest{}, err
	}
	now := s.clk.Now()
	err = s.st.Do(ctx, func(tx *Tx) error {
		cur, err := tx.Quest(id)
		if err != nil {
			return err
		}
		if cur.State != Draft && cur.State != Completed {
			return fmt.Errorf("%w: %s is %s", ErrInvalidState, cur.Name, cur.State)
		}
		steps, err := tx.Steps(id)
		if err != nil {
			return err
		}
		for _, st := range steps {
			st.State, st.Character, st.Returns, st.Entry, st.Blocker, st.Approved, st.Approval = StepPending, "", 0, "", "", false, 0
			st.ResumeNote, st.DiscardResult, st.StuckAt, st.Escalated = "", false, time.Time{}, false
			if err := tx.SaveStep(st, now); err != nil {
				return err
			}
		}
		if err := tx.FreezeCriteria(id, crit); err != nil {
			return err
		}
		if err := tx.ForgetFailures(id); err != nil {
			return err
		}
		cur.State, cur.PausedReason, cur.Input = Running, "", input
		cur.Dirty++
		cur.Round++
		q = cur
		return tx.SaveQuest(cur, now)
	})
	if err != nil {
		return Quest{}, err
	}
	s.appendScroll(ctx, q, s.roundSection(q))
	if len(crit) == 0 && q.Def.Issue != 0 && q.Issue != 0 && s.o.GitHub != nil {
		// an issue with no item C<n>: the quest runs without criteria, and the
		// Master of the table has nothing to check at the acceptance, so he tells the Grand Master
		s.tellMaster(ctx, q, inbox.KindSystem, fmt.Sprintf("A issue #%d da missão %s não tem nenhum item `- [ ] C<n>`: a missão roda sem critérios, `proof: all` não exige nada e o aceite não terá o que conferir. Avise o Grão-Mestre.", q.Issue, q.Name))
	}
	s.link(ctx, q)
	s.kick(ctx, q.ID)
	return s.st.Quest(ctx, q.ID)
}

// Pause stops new steps from starting; the ones under way finish and their
// result is kept.
func (s *Service) Pause(ctx context.Context, id string) (Quest, error) {
	return s.setState(ctx, id, Running, Paused, ReasonPaused)
}

// Resume goes on from where the quest stopped.
func (s *Service) Resume(ctx context.Context, id string) (Quest, error) {
	q, err := s.setState(ctx, id, Paused, Running, "")
	if err != nil {
		return q, err
	}
	s.kick(ctx, id)
	return q, nil
}

func (s *Service) setState(ctx context.Context, id string, from, to State, reason string) (Quest, error) {
	var q Quest
	err := s.st.Do(ctx, func(tx *Tx) error {
		cur, err := tx.Quest(id)
		if err != nil {
			return err
		}
		if cur.State != from {
			return fmt.Errorf("%w: %s is %s", ErrInvalidState, cur.Name, cur.State)
		}
		cur.State, cur.PausedReason = to, reason
		q = cur
		return tx.SaveQuest(cur, s.clk.Now())
	})
	return q, err
}

// pause pauses a quest that is running because something needs a decision.
// The tellings are the caller's.
func (s *Service) pause(ctx context.Context, id, reason string) (bool, error) {
	paused := false
	err := s.st.Do(ctx, func(tx *Tx) error {
		cur, err := tx.Quest(id)
		if err != nil || cur.State != Running {
			return err
		}
		cur.State, cur.PausedReason = Paused, reason
		paused = true
		return tx.SaveQuest(cur, s.clk.Now())
	})
	return paused, err
}

// PauseFor pauses a running quest for a reason that something else lifts (the
// limit of use, docs/plataforma/limites-de-uso.md). It says whether the quest
// was running; the telling is the caller's.
func (s *Service) PauseFor(ctx context.Context, id, reason string) (bool, error) {
	return s.pause(ctx, id, reason)
}

// pauseAndTell pauses a running quest and tells the Master of the table why.
func (s *Service) pauseAndTell(ctx context.Context, q Quest, reason string) {
	paused, err := s.pause(ctx, q.ID, reason)
	if err != nil {
		s.log.Warn("quests: the quest was not paused", "quest", q.ID, "err", err)
		return
	}
	if paused {
		s.tellMaster(ctx, q, inbox.KindSystem, fmt.Sprintf("A missão %s foi pausada: %s.", q.Name, reason))
	}
}

// Cancel ends the quest: what is pending is cancelled at once, a step under
// way finishes first (nobody busy is interrupted), and then the characters
// that were made for it are ended.
func (s *Service) Cancel(ctx context.Context, id, reason string) (Quest, error) {
	var (
		q      Quest
		active bool
	)
	now := s.clk.Now()
	err := s.st.Do(ctx, func(tx *Tx) error {
		cur, err := tx.Quest(id)
		if err != nil {
			return err
		}
		if cur.State == Completed || cur.State == Cancelled {
			return fmt.Errorf("%w: %s is %s", ErrInvalidState, cur.Name, cur.State)
		}
		steps, err := tx.Steps(id)
		if err != nil {
			return err
		}
		for _, st := range steps {
			switch {
			case st.State == StepRunning || st.State == StepStuck:
				active = true // it finishes first: nobody busy is interrupted
			case !st.State.Finished():
				st.State, st.Blocker, st.Approval = StepCancelled, "", 0
				if err := tx.SaveStep(st, now); err != nil {
					return err
				}
			}
		}
		cur.State, cur.PausedReason = Cancelled, reason
		cur.Dirty++
		q = cur
		return tx.SaveQuest(cur, now)
	})
	if err != nil {
		return Quest{}, err
	}
	if o := s.o.Waits; o != nil {
		if err := o.CancelQuest(ctx, id); err != nil {
			s.log.Warn("quests: the waits of the quest were not cancelled", "quest", id, "err", err)
		}
	}
	if !active {
		s.finalizeCancel(ctx, q)
	}
	s.kick(ctx, id)
	return q, nil
}

// finalizeCancel ends what the quest made once no step is under way.
func (s *Service) finalizeCancel(ctx context.Context, q Quest) {
	text := "## Missão cancelada\n"
	if q.PausedReason != "" {
		text += inbox.Say("**Motivo:**", q.PausedReason, ">") + "\n"
	}
	s.appendScroll(ctx, q, text)
	s.endResources(ctx, q)
}

// endResources has the ephemeral characters of the quest ended and takes its
// reason off the connections.
func (s *Service) endResources(ctx context.Context, q Quest) {
	if err := s.o.Characters.EndQuest(ctx, q.ID); err != nil {
		s.log.Warn("quests: the characters of the quest were not ended", "quest", q.ID, "err", err)
	}
	if s.o.Connections != nil {
		if err := s.o.Connections.EndQuest(ctx, q.ID); err != nil {
			s.log.Warn("quests: the connections of the quest were not ended", "quest", q.ID, "err", err)
		}
	}
}

// --- telling people ---

// tellMaster puts an item in the inbox of the Master of the table of the quest;
// without one alive, the Grand Master is told.
func (s *Service) tellMaster(ctx context.Context, q Quest, kind inbox.Kind, text string) {
	m, ok, err := s.o.Registry.TableMaster(ctx, q.Workspace, q.Table)
	if err == nil && ok && m.State.Alive() {
		_, err = s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: q.Workspace, Target: m.ID, Kind: kind, Sender: inbox.SystemSender, Text: text,
			Refs: inbox.Refs{Quest: q.ID, Table: q.Table}})
		if err != nil {
			s.log.Warn("quests: the Master of the table was not told", "quest", q.ID, "err", err)
		}
		return
	}
	if s.o.Notifier != nil {
		if err := s.o.Notifier.Notify(ctx, characters.Notice{Kind: characters.NoticeSystem, To: characters.Recipient{Kind: characters.ToGrandMaster},
			Workspace: q.Workspace, Table: q.Table, Text: text}); err != nil {
			s.log.Warn("quests: the Grand Master was not told", "quest", q.ID, "err", err)
		}
	}
}

// escalate tells the Grand Master and the users.
func (s *Service) escalate(ctx context.Context, q Quest, text string) {
	if s.o.Notifier == nil {
		return
	}
	for _, to := range []characters.RecipientKind{characters.ToGrandMaster, characters.ToOwner} {
		if err := s.o.Notifier.Notify(ctx, characters.Notice{Kind: characters.NoticeSystem, To: characters.Recipient{Kind: to},
			Workspace: q.Workspace, Table: q.Table, Text: text}); err != nil {
			s.log.Warn("quests: an escalation was not delivered", "quest", q.ID, "to", to, "err", err)
		}
	}
}

// --- scroll and connections ---

// appendScroll adds a section to the scroll of the quest; a failure is only
// logged, the quest does not depend on it.
func (s *Service) appendScroll(ctx context.Context, q Quest, section string) {
	if _, err := s.o.Scrolls.AppendSection(ctx, q.Workspace, q.Table, scrolls.QuestScrollName(q.Name), section); err != nil {
		s.log.Warn("quests: the scroll of the quest was not written", "quest", q.ID, "err", err)
	}
}

// link makes the connections of the quest between the characters its steps
// have now: a character and the one of the next step, the one it may hand
// back to, and each of them with the scroll. It is safe to repeat.
func (s *Service) link(ctx context.Context, q Quest) {
	if s.o.Connections == nil {
		return
	}
	steps, err := s.st.Steps(ctx, q.ID)
	if err != nil {
		return
	}
	who := map[string]string{}
	for _, st := range steps {
		who[st.ID] = st.Character
		if sd := q.Def.Steps[st.Pos]; st.Character == "" && sd.Character != "" && st.State != StepCompleted {
			if c, err := s.o.Registry.ByName(ctx, q.Workspace, q.Table, sd.Character); err == nil && c.State.Alive() {
				who[st.ID] = c.ID
			}
		}
	}
	// the review scroll goes to the steps that return and to the ones they return to
	review := map[string]scrolls.Note{}
	if note, ok := s.ensureReview(ctx, q); ok {
		for _, sd := range q.Def.Steps {
			if sd.CanReturnTo != "" {
				review[sd.ID], review[sd.CanReturnTo] = note, note
			}
		}
	}
	var pairs [][2]connections.Ref
	for _, sd := range q.Def.Steps {
		cb := who[sd.ID]
		if cb == "" {
			continue
		}
		if q.Scroll != "" {
			pairs = append(pairs, [2]connections.Ref{connections.Char(cb), connections.Scroll(q.Scroll)})
		}
		if rev, ok := review[sd.ID]; ok {
			pairs = append(pairs, [2]connections.Ref{connections.Char(cb), connections.Scroll(rev.Key)})
		}
		prior := append([]string{}, sd.After...)
		if sd.CanReturnTo != "" {
			prior = append(prior, sd.CanReturnTo)
		}
		for _, a := range prior {
			if ca := who[a]; ca != "" && ca != cb {
				pairs = append(pairs, [2]connections.Ref{connections.Char(ca), connections.Char(cb)})
			}
		}
	}
	if len(pairs) == 0 {
		return
	}
	if err := s.o.Connections.LinkQuest(ctx, q.ID, q.Workspace, q.Table, pairs...); err != nil {
		s.log.Warn("quests: the connections of the quest were not made", "quest", q.ID, "err", err)
	}
}

// --- running the work of the engine ---

// kick has a quest looked at: its steps advanced and its mirror on GitHub
// brought up to date. With the loop running (Loop) it is queued; otherwise it
// is done at once, in the call that asked for it.
func (s *Service) kick(ctx context.Context, id string) {
	if s.async.Load() {
		select {
		case s.work <- id:
			return
		default:
		}
	}
	s.process(ctx, id)
}

func (s *Service) process(ctx context.Context, id string) {
	s.advance(ctx, id)
	s.mirror(ctx, id)
}

// Advance has the steps of a quest move as far as they can now.
func (s *Service) Advance(ctx context.Context, id string) { s.process(ctx, id) }

// finishLock takes the lock of the done of a character; the caller unlocks.
func (s *Service) finishLock(id string) *sync.Mutex {
	s.mu.Lock()
	m, ok := s.finishing[id]
	if !ok {
		m = new(sync.Mutex)
		s.finishing[id] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m
}

// LastRed is the last verification of the table that failed, if no later one
// passed: the command, the end of its output and the places in it. The engine
// builds the evidence of a notice about the table from it (it is kept in
// memory: after a restart there is none, and the notice says so).
func (s *Service) LastRed(workspace, table string) (VerifyResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reds[workspace+"/"+table]
	return r, ok
}

// noteVerification keeps the outcome of a verification of a table for LastRed.
func (s *Service) noteVerification(q Quest, res VerifyResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := q.Workspace + "/" + q.Table
	if res.OK {
		delete(s.reds, k)
		return
	}
	s.reds[k] = res
}
