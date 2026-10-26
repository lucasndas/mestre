package epicdeps

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/internal/orchestration/waits"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Tables finds the table of an epic that is not over (tables.Store).
type Tables interface {
	ByEpic(ctx context.Context, workspace string, epic int) (tables.Table, bool, error)
}

// Epics reads an epic on GitHub (tables.GitHub).
type Epics interface {
	Epic(ctx context.Context, workspace string, number int) (tables.Epic, error)
}

// Characters finds the Grand Master of a workspace (characters.Registry).
type Characters interface {
	GrandMaster(ctx context.Context, workspace string) (characters.Character, bool, error)
}

// Waits is the part of the waits the dependencies use (*waits.Service).
type Waits interface {
	WaitEpic(ctx context.Context, waiter characters.Character, table, ref, name string) (waits.Wait, error)
	EpicDone(ctx context.Context, ref, text string)
	Cancel(ctx context.Context, id string) error
}

// Inbox is the inbox of the characters (*inbox.Service).
type Inbox interface {
	Enqueue(ctx context.Context, n inbox.NewItem) (inbox.Item, error)
	// PumpAll delivers what waited for the release of a table.
	PumpAll(ctx context.Context) error
}

// Options are the pieces of the Service.
type Options struct {
	DB         *store.DB
	Clock      clock.Clock
	Tables     Tables
	Epics      Epics
	Characters Characters
	Waits      Waits
	Inbox      Inbox
	// Released, when set, is told that the table of an epic is no longer held,
	// so the missions that waited for it go on.
	Released func(ctx context.Context, workspace string, table tables.Table)
	Log      *slog.Logger
}

// Errors of the dependencies.
var (
	// ErrNotGrandMaster: only the Grand Master of the workspace declares or
	// drops a dependency (the CLI answers level_denied).
	ErrNotGrandMaster = errors.New("epicdeps: only the Grand Master of the workspace does this")
	// ErrSelf: an epic that depends on itself.
	ErrSelf = errors.New("epicdeps: an epic cannot depend on itself")
	// ErrNoDependency: there is no such dependency to drop.
	ErrNoDependency = errors.New("epicdeps: there is no such dependency")
)

// CycleError is the refusal of a dependency that would close a ring; Text is
// the ring written, "#12 → #15 → #12".
type CycleError struct{ Text string }

func (e *CycleError) Error() string {
	return "a dependência fecharia um ciclo entre épicos: " + e.Text + "; pergunte aos usuários qual vem primeiro"
}

// Service holds the tables that depend on other tables.
type Service struct {
	o   Options
	st  Store
	clk clock.Clock
	log *slog.Logger
	mu  sync.Mutex
}

// New builds the Service.
func New(o Options) (*Service, error) {
	switch {
	case o.DB == nil, o.Tables == nil, o.Epics == nil, o.Characters == nil, o.Waits == nil, o.Inbox == nil:
		return nil, errors.New("epicdeps: DB, Tables, Epics, Characters, Waits and Inbox are required")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{o: o, st: Store{DB: o.DB}, clk: o.Clock, log: log}, nil
}

var _ tables.Dependencies = (*Service)(nil)

// Result is what Depend answers.
type Result struct {
	// State is pending while the table of the other epic is not merged,
	// satisfied when it already is (or its epic was closed), cancelled when
	// the other epic was closed as not planned.
	State State
	// Waiting says the table of the dependent epic is held now; without a
	// table the dependency waits for it to open.
	Waiting bool
	// New says the call made the dependency (it was not pending already): who
	// undoes its own work drops only these.
	New bool
}

func waitRef(workspace string, epic, on int) string {
	return fmt.Sprintf("%s/epic/%d/for/%d", workspace, on, epic)
}

func (s *Service) isGrandMaster(by characters.Character) error {
	if by.Role != characters.RoleGrandMaster || by.Workspace == "" {
		return ErrNotGrandMaster
	}
	return nil
}

// Depend declares that epic cannot go on before on (`gh epic update <epic>
// --depends-on <on>`). A dependency that would close a ring is refused with
// the ring written and leaves nothing behind. If the dependent epic has a
// table it is held now, otherwise when it opens.
func (s *Service) Depend(ctx context.Context, by characters.Character, epic, on int) (Result, error) {
	if err := s.isGrandMaster(by); err != nil {
		return Result{}, err
	}
	if epic <= 0 || on <= 0 {
		return Result{}, fmt.Errorf("epicdeps: %d and %d are not epics", epic, on)
	}
	if epic == on {
		return Result{}, ErrSelf
	}
	ws := by.Workspace
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, err := s.st.Pending(ctx, ws)
	if err != nil {
		return Result{}, err
	}
	if path := ring(pending, epic, on); path != "" {
		return Result{}, &CycleError{Text: path}
	}
	d := Dependency{Workspace: ws, Epic: epic, DependsOn: on, CreatedAt: s.clk.Now()}
	prev, had, err := s.st.Get(ctx, ws, epic, on)
	if err != nil {
		return Result{}, err
	}
	added, err := s.st.Add(ctx, d)
	if err != nil {
		return Result{}, err
	}
	back := func(cause error) error { return s.undo(ctx, d, added, prev, had, cause) }
	cur, _, err := s.st.Get(ctx, ws, epic, on)
	if err != nil {
		return Result{}, back(err)
	}
	t, hasTable, err := s.o.Tables.ByEpic(ctx, ws, epic)
	if err != nil {
		return Result{}, back(err)
	}
	if !hasTable || cur.State != Pending {
		return Result{State: cur.State, New: added && !had}, nil
	}
	if cur.Wait != "" {
		return Result{State: Pending, Waiting: true, New: added && !had}, nil
	}
	state, err := s.arm(ctx, cur, t)
	if err != nil {
		return Result{}, back(err)
	}
	return Result{State: state, Waiting: state == Pending, New: added && !had}, nil
}

// undo takes back a dependency that could not be armed, so no half-made one
// holds a table and one that was held before keeps holding it: a dependency
// that existed (cancelled, waiting for the decision of the Grand Master) is
// put back as it was; the error is the one that came.
func (s *Service) undo(ctx context.Context, d Dependency, added bool, prev Dependency, had bool, err error) error {
	if !added {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	if had {
		if rerr := s.st.Restore(ctx, prev); rerr != nil {
			s.log.Warn("epicdeps: a dependency was not put back", "epic", d.Epic, "on", d.DependsOn, "err", rerr)
		}
		return err
	}
	if derr := s.st.Delete(ctx, d); derr != nil {
		s.log.Warn("epicdeps: a dependency that was not armed was not removed", "epic", d.Epic, "on", d.DependsOn, "err", derr)
	}
	return err
}

// ring finds the ring the dependency of epic on on would close with the
// pending ones: "#on → … → #epic → #on", or "".
func ring(pending []Dependency, epic, on int) string {
	next := map[int][]int{}
	for _, d := range pending {
		next[d.Epic] = append(next[d.Epic], d.DependsOn)
	}
	// "on depends on …": walk what on depends on, looking for epic.
	seen := map[int]bool{on: true}
	var walk func(at int, path []int) []int
	walk = func(at int, path []int) []int {
		if at == epic {
			return path
		}
		for _, n := range next[at] {
			if seen[n] {
				continue
			}
			seen[n] = true
			if p := walk(n, append(path, n)); p != nil {
				return p
			}
		}
		return nil
	}
	p := walk(on, []int{on})
	if p == nil {
		return ""
	}
	var b strings.Builder
	for _, e := range p {
		fmt.Fprintf(&b, "#%d → ", e)
	}
	fmt.Fprintf(&b, "#%d", on)
	return b.String()
}

// arm decides what holds the table t of the dependent epic of d: nothing when
// the other epic is already merged or closed, otherwise a wait of the Grand
// Master.
func (s *Service) arm(ctx context.Context, d Dependency, t tables.Table) (State, error) {
	gm, ok, err := s.o.Characters.GrandMaster(ctx, d.Workspace)
	if err != nil {
		return Pending, err
	}
	if !ok {
		return Pending, errors.New("epicdeps: the workspace has no Grand Master")
	}
	other, hasTable, err := s.o.Tables.ByEpic(ctx, d.Workspace, d.DependsOn)
	if err != nil {
		return Pending, err
	}
	var notice string
	if hasTable {
		if other.Merged {
			return Satisfied, s.end(ctx, d, Satisfied)
		}
	} else {
		epic, err := s.o.Epics.Epic(ctx, d.Workspace, d.DependsOn)
		if err != nil {
			return Pending, fmt.Errorf("o épico #%d não foi lido: %w", d.DependsOn, err)
		}
		switch {
		case !epic.Open && epic.NotPlanned:
			if err := s.end(ctx, d, Cancelled); err != nil {
				return Pending, err
			}
			s.tell(ctx, gm, t.ID, fmt.Sprintf("O épico #%d foi fechado como não planejado: a mesa %s (épico #%d) continua parada. Decida o que fazer com ela.", d.DependsOn, t.ID, d.Epic))
			return Cancelled, nil
		case !epic.Open:
			return Satisfied, s.end(ctx, d, Satisfied)
		}
		notice = fmt.Sprintf("O épico #%d não tem mesa aberta: a mesa %s (épico #%d) espera até a #%d ser aberta e mesclada. Abra a mesa, ou decida outro caminho.", d.DependsOn, t.ID, d.Epic, d.DependsOn)
	}
	w, err := s.o.Waits.WaitEpic(ctx, gm, t.ID, waitRef(d.Workspace, d.Epic, d.DependsOn), fmt.Sprintf("a mesa #%d", d.DependsOn))
	if err != nil {
		return Pending, fmt.Errorf("a espera pela mesa #%d não foi registrada: %w", d.DependsOn, err)
	}
	if err := s.st.SetWait(ctx, d, w.ID); err != nil {
		_ = s.o.Waits.Cancel(context.WithoutCancel(ctx), w.ID)
		return Pending, err
	}
	if notice != "" {
		s.tell(ctx, gm, t.ID, notice)
	}
	return Pending, nil
}

func (s *Service) end(ctx context.Context, d Dependency, to State, from ...State) error {
	_, err := s.st.End(ctx, d, to, s.clk.Now(), from...)
	return err
}

// tell puts an item `system` in the inbox of the Grand Master.
func (s *Service) tell(ctx context.Context, gm characters.Character, table, text string) {
	if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: gm.Workspace, Target: gm.ID, Kind: inbox.KindSystem, Sender: inbox.SystemSender, Text: text,
		Refs: inbox.Refs{Table: table}}); err != nil {
		s.log.Warn("epicdeps: the Grand Master was not told", "err", err)
	}
}

// Release drops a dependency that holds a table (pending, or cancelled and not
// yet decided): the Grand Master decided to go on without it.
func (s *Service) Release(ctx context.Context, by characters.Character, epic, on int) error {
	if err := s.isGrandMaster(by); err != nil {
		return err
	}
	d, err := s.drop(ctx, by.Workspace, epic, on)
	if err != nil {
		return err
	}
	s.release(ctx, d)
	return nil
}

func (s *Service) drop(ctx context.Context, workspace string, epic, on int) (Dependency, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok, err := s.st.Get(ctx, workspace, epic, on)
	if err != nil {
		return d, err
	}
	if !ok || (d.State != Pending && d.State != Cancelled) {
		return d, fmt.Errorf("%w: #%d → #%d", ErrNoDependency, epic, on)
	}
	if _, err := s.st.End(ctx, d, Released, s.clk.Now(), Pending, Cancelled); err != nil {
		return d, err
	}
	if d.Wait != "" {
		if err := s.o.Waits.Cancel(ctx, d.Wait); err != nil {
			s.log.Warn("epicdeps: the wait of a dropped dependency was not cancelled", "wait", d.Wait, "err", err)
		}
	}
	return d, nil
}

// Opened implements tables.Dependencies: the table of an epic that depends on
// others opens held. An error keeps the table from opening and leaves no wait
// behind.
func (s *Service) Opened(ctx context.Context, workspace string, t tables.Table) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ds, err := s.st.PendingOf(ctx, workspace, t.Epic)
	if err != nil {
		return err
	}
	var armed []Dependency
	for _, d := range ds {
		if d.Wait != "" {
			// A wait of a table of this epic that is gone: arm again for this one.
			s.disarm(ctx, d)
		}
		if _, err := s.arm(ctx, d, t); err != nil {
			for _, a := range armed {
				s.disarm(ctx, a)
			}
			return err
		}
		armed = append(armed, d)
	}
	return nil
}

// Dropped implements tables.Dependencies: the table of the epic is gone (it did
// not open, or it was cancelled), so the waits made for it end; the
// dependencies stay, for the next table of the epic.
func (s *Service) Dropped(ctx context.Context, workspace string, t tables.Table) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ds, err := s.st.PendingOf(ctx, workspace, t.Epic)
	if err != nil {
		return err
	}
	for _, d := range ds {
		s.disarm(ctx, d)
	}
	return nil
}

// disarm ends the wait that holds a dependency and forgets it.
func (s *Service) disarm(ctx context.Context, d Dependency) {
	cur, ok, err := s.st.Get(ctx, d.Workspace, d.Epic, d.DependsOn)
	if err != nil || !ok || cur.Wait == "" {
		return
	}
	if err := s.o.Waits.Cancel(ctx, cur.Wait); err != nil {
		s.log.Warn("epicdeps: a wait was not cancelled", "wait", cur.Wait, "err", err)
	}
	if err := s.st.SetWait(ctx, cur, ""); err != nil {
		s.log.Warn("epicdeps: a wait was not forgotten", "wait", cur.Wait, "err", err)
	}
}

// Held implements tables.Dependencies: something the epic waits for is not
// merged, or was cancelled and nobody decided what to do.
func (s *Service) Held(ctx context.Context, workspace string, epic int) (bool, error) {
	ds, err := s.st.HeldOf(ctx, workspace, epic)
	return len(ds) > 0, err
}

// Waiting implements tables.Dependencies: the epics the epic is held by.
func (s *Service) Waiting(ctx context.Context, workspace string, epic int) ([]int, error) {
	ds, err := s.st.HeldOf(ctx, workspace, epic)
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.DependsOn)
	}
	return out, nil
}

// Merged implements tables.Dependencies: the pull request of the table was
// merged and its commit is in the base, so the tables that wait for it go on.
func (s *Service) Merged(ctx context.Context, workspace string, t tables.Table) error {
	return s.settle(ctx, workspace, t.Epic, Satisfied, fmt.Sprintf("o PR da mesa #%d foi mesclado e o commit está na base", t.Epic))
}

// Cancelled implements tables.Dependencies: the table was cancelled, so the
// tables that wait for it are told and stay held until the Grand Master
// decides.
func (s *Service) Cancelled(ctx context.Context, workspace string, t tables.Table) error {
	return s.settle(ctx, workspace, t.Epic, Cancelled, fmt.Sprintf("a mesa #%d foi cancelada", t.Epic))
}

// EpicClosed is told that an epic was closed on GitHub: as completed it is
// merged work, as not planned it will never be.
func (s *Service) EpicClosed(ctx context.Context, workspace string, epic int, notPlanned bool) error {
	if notPlanned {
		return s.settle(ctx, workspace, epic, Cancelled, fmt.Sprintf("o épico #%d foi fechado como não planejado", epic))
	}
	return s.settle(ctx, workspace, epic, Satisfied, fmt.Sprintf("o épico #%d foi fechado", epic))
}

// settle ends the pending dependencies on an epic. For Satisfied it also
// repeats, for the ones already satisfied, what may have failed before (the
// wake of the Grand Master and the delivery of what waited): telling the
// merge again repairs it. The tables are released after the lock of the
// service is let go, because releasing delivers messages and runs missions.
func (s *Service) settle(ctx context.Context, workspace string, on int, to State, what string) error {
	released, err := s.settleLocked(ctx, workspace, on, to, what)
	for _, d := range released {
		s.release(ctx, d)
	}
	return err
}

func (s *Service) settleLocked(ctx context.Context, workspace string, on int, to State, what string) ([]Dependency, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ds, err := s.st.PendingOn(ctx, workspace, on)
	if err != nil {
		return nil, err
	}
	var errs []error
	var released []Dependency
	if to == Satisfied {
		again, err := s.st.OnAs(ctx, workspace, on, Satisfied)
		if err != nil {
			return nil, err
		}
		for _, d := range again {
			if d.Wait != "" {
				s.o.Waits.EpicDone(ctx, waitRef(workspace, d.Epic, on), fmt.Sprintf("%s; a mesa #%d pode seguir.", what, d.Epic))
			}
			released = append(released, d)
		}
	}
	for _, d := range ds {
		ended, err := s.st.End(ctx, d, to, s.clk.Now())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ended {
			continue
		}
		switch to {
		case Satisfied:
			if d.Wait != "" {
				s.o.Waits.EpicDone(ctx, waitRef(workspace, d.Epic, on), fmt.Sprintf("%s; a mesa #%d pode seguir.", what, d.Epic))
			}
			released = append(released, d)
		case Cancelled:
			if d.Wait != "" {
				if err := s.o.Waits.Cancel(ctx, d.Wait); err != nil {
					s.log.Warn("epicdeps: a wait for a cancelled table was not cancelled", "wait", d.Wait, "err", err)
				}
			}
			if gm, ok, err := s.o.Characters.GrandMaster(ctx, workspace); err == nil && ok {
				s.tell(ctx, gm, "", fmt.Sprintf("%s: a mesa do épico #%d continua parada. Decida o que fazer com ela.", capital(what), d.Epic))
			}
		}
	}
	return released, errors.Join(errs...)
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// release tells the dependent table it can go on and delivers what waited.
func (s *Service) release(ctx context.Context, d Dependency) {
	if held, err := s.Held(ctx, d.Workspace, d.Epic); err != nil || held {
		return
	}
	if t, ok, err := s.o.Tables.ByEpic(ctx, d.Workspace, d.Epic); err == nil && ok && s.o.Released != nil {
		s.o.Released(ctx, d.Workspace, t)
	}
	if err := s.o.Inbox.PumpAll(ctx); err != nil {
		s.log.Warn("epicdeps: what waited for a released table was not delivered", "epic", d.Epic, "err", err)
	}
}
