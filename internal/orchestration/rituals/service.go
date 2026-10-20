package rituals

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Options are the pieces of the Service.
type Options struct {
	Store    *Store
	Clock    clock.Clock
	Registry characters.Registry
	Inbox    Inbox
	// Quests and Pauses may be nil: no ritual aims at a quest, nothing is
	// paused.
	Quests Quests
	Pauses Pauses
	// Terminals types the line of a command; PreRunner runs a pre-run;
	// Permissions decides it. Nil: a command or a pre-run fails when it fires
	// (the machine cannot be reached, nothing is run without a decision).
	Terminals   Terminals
	PreRunner   PreRunner
	Permissions Permissions
	// Zones gives the time zone of a workspace (UTC by default).
	Zones     Zones
	Approvals Approvals
	// PreRunTimeout and PreRunOutput default to 60 s and 16 KiB.
	PreRunTimeout time.Duration
	PreRunOutput  int
	Logger        *slog.Logger
}

// Service is the rituals of the Citadel.
type Service struct {
	o   Options
	st  *Store
	clk clock.Clock
	log *slog.Logger

	// tick lets one look at the clock run at a time.
	tick sync.Mutex
	// kick asks the loop to look again at once (a target became free).
	kick chan struct{}
}

// NewService returns the Service.
func NewService(o Options) (*Service, error) {
	switch {
	case o.Store == nil || o.Store.DB == nil:
		return nil, errors.New("rituals: the service needs the store")
	case o.Registry.DB == nil:
		return nil, errors.New("rituals: the service needs the registry of characters")
	case o.Inbox == nil:
		return nil, errors.New("rituals: the service needs the inbox")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.PreRunTimeout <= 0 {
		o.PreRunTimeout = DefaultPreRunTimeout
	}
	if o.PreRunOutput <= 0 {
		o.PreRunOutput = DefaultPreRunOutput
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{o: o, st: o.Store, clk: o.Clock, log: o.Logger, kick: make(chan struct{}, 1)}, nil
}

// Store gives access to the tables.
func (s *Service) Store() *Store { return s.st }

func (s *Service) zone(ctx context.Context, workspace string) *time.Location {
	if s.o.Zones != nil {
		if z := s.o.Zones.Zone(ctx, workspace); z != nil {
			return z
		}
	}
	return time.UTC
}

// ParseSchedule reads the flags of a schedule in the time zone of the
// workspace, at the time of the engine.
func (s *Service) ParseSchedule(ctx context.Context, workspace string, sp Spec) (Schedule, error) {
	return ParseSchedule(sp, s.zone(ctx, workspace), s.clk.Now())
}

// CreateRequest is `ritual create`. The scope is the table, or the workspace
// when Table is empty.
type CreateRequest struct {
	Workspace, Table string
	Name             string
	// Target is the name of the character and TargetTable the table of the
	// table master a ritual of the workspace aims at.
	Target, TargetTable string
	// Quest is the name of the quest of the table to run.
	Quest string
	// Prompt goes to an agent, Command is typed in a simple terminal.
	Prompt, Command string
	Schedule        Schedule
	PreRun          string
	// Timeout defaults to 30 minutes, or the interval if that is shorter.
	Timeout time.Duration
	By      Actor
}

// MaxName is the longest name of a ritual.
const MaxName = 64

// authorize checks that who asks may act on the rituals of the scope: the
// Master of the table in its own table, the Grand Master in the workspace, a
// user who is an Administrator or above in any scope, the engine.
func (s *Service) authorize(by Actor, workspace, table string) error {
	switch by.Kind {
	case ByEngine:
		return nil
	case ByUser:
		if by.Level == characters.LevelOwner || by.Level == characters.LevelAdministrator {
			return nil
		}
	case ByCharacter:
		c := by.Character
		if c.Workspace != workspace {
			break
		}
		if table == "" && c.Role == characters.RoleGrandMaster {
			return nil
		}
		if table != "" && c.Role == characters.RoleTableMaster && c.Table == table {
			return nil
		}
	}
	return ErrLevelDenied
}

func creator(by Actor) string {
	switch by.Kind {
	case ByCharacter:
		return by.Character.ID
	case ByUser:
		return "user:" + by.ID
	}
	return "engine"
}

// Create makes a ritual: the target, the content and the schedule are checked
// (docs/orquestracao/rituais.md#regras-e-erros) and the first slot is set.
func (s *Service) Create(ctx context.Context, req CreateRequest) (Ritual, error) {
	if err := s.authorize(req.By, req.Workspace, req.Table); err != nil {
		return Ritual{}, err
	}
	r := Ritual{Workspace: req.Workspace, Table: req.Table, Name: strings.TrimSpace(req.Name), Target: req.Target, TargetTable: req.TargetTable,
		Quest: req.Quest, Schedule: req.Schedule, PreRun: strings.TrimSpace(req.PreRun), Timeout: req.Timeout, State: Enabled, CreatedBy: creator(req.By)}
	switch {
	case req.Prompt != "" && req.Command != "":
		return Ritual{}, fmt.Errorf("%w: use --prompt or --command, not both", ErrBadRequest)
	case req.Quest != "":
		r.Action = ActionQuest
	case req.Command != "":
		r.Action, r.Content = ActionCommand, req.Command
	default:
		r.Action, r.Content = ActionPrompt, req.Prompt
	}
	now := s.clk.Now()
	if err := s.validate(ctx, &r, now, true); err != nil {
		return Ritual{}, err
	}
	r.CreatedAt, r.UpdatedAt = now, now
	r.NextRun = r.Schedule.Next(now, s.zone(ctx, r.Workspace))
	if r.Schedule.Kind == Once {
		r.NextRun = r.Schedule.At
	}
	var out Ritual
	err := s.st.Do(ctx, func(t *Tx) (err error) {
		out, err = t.Insert(r)
		return err
	})
	return out, err
}

// validate checks a ritual, as made or as edited, and sets its timeout.
func (s *Service) validate(ctx context.Context, r *Ritual, now time.Time, checkSchedule bool) error {
	if r.Name == "" || utf8.RuneCountInString(r.Name) > MaxName {
		return fmt.Errorf("%w: the name has 1 to %d characters", ErrBadRequest, MaxName)
	}
	if !inbox.PrintableLine(r.Name) {
		// The name is the sender of the items the ritual puts in an inbox.
		return fmt.Errorf("%w: the name is one line of printable characters", ErrBadRequest)
	}
	if checkSchedule {
		if err := r.Schedule.Validate(now); err != nil {
			return err
		}
	}
	switch r.Action {
	case ActionPrompt, ActionCommand:
		if r.Target == "" || r.Quest != "" {
			return fmt.Errorf("%w: aim at one character (--target) or one quest (--quest)", ErrBadRequest)
		}
		if strings.TrimSpace(r.Content) == "" {
			return fmt.Errorf("%w: the %s is empty", ErrBadRequest, r.Action)
		}
		if r.Action == ActionCommand && strings.ContainsAny(r.Content, "\r\n") {
			return fmt.Errorf("%w: a command is a single line", ErrBadRequest)
		}
	case ActionQuest:
		if r.Target != "" || r.TargetTable != "" || r.Quest == "" || r.Content != "" {
			return fmt.Errorf("%w: a ritual of a quest has the quest and nothing else", ErrBadRequest)
		}
		if r.PreRun != "" {
			return fmt.Errorf("%w: a ritual of a quest has no pre-run", ErrBadRequest)
		}
		if r.Table == "" {
			return fmt.Errorf("%w: a quest belongs to a table; make the ritual in the table", ErrBadRequest)
		}
	default:
		return fmt.Errorf("%w: unknown action %q", ErrBadRequest, r.Action)
	}
	if strings.Contains(r.Content, Placeholder) && r.PreRun == "" {
		return fmt.Errorf("%w: %s needs --pre-run", ErrBadRequest, Placeholder)
	}
	if err := s.checkTarget(ctx, r); err != nil {
		return err
	}
	if iv := r.Schedule.Interval(); r.Timeout > 0 && iv > 0 && r.Timeout > iv {
		return fmt.Errorf("%w: the timeout (%s) is longer than the interval (%s)", ErrBadRequest, r.Timeout, iv)
	}
	if r.Timeout <= 0 {
		r.Timeout = defaultTimeout(r.Schedule)
	}
	return nil
}

// defaultTimeout is the timeout a ritual gets when none is given: 30 minutes,
// or the interval when it is shorter.
func defaultTimeout(s Schedule) time.Duration {
	if iv := s.Interval(); iv > 0 && iv < DefaultTimeout {
		return iv
	}
	return DefaultTimeout
}

// checkTarget checks that the target is something the ritual may aim at: a
// character of the table (of the Master, for a ritual of the workspace) or a
// quest of the table, never a sheet.
func (s *Service) checkTarget(ctx context.Context, r *Ritual) error {
	if r.Action == ActionQuest {
		if s.o.Quests == nil {
			return fmt.Errorf("%w: the quests are not available", ErrNotVisible)
		}
		if _, err := s.o.Quests.Get(ctx, r.Workspace, r.Table, r.Quest); err != nil {
			return fmt.Errorf("%w: quest %s", ErrNotVisible, r.Quest)
		}
		return nil
	}
	c, err := s.target(ctx, *r)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrNotVisible, r.Target)
	}
	if r.Table == "" {
		// The workspace: the Grand Master, or the Master of a table.
		okGM := r.TargetTable == "" && c.Role == characters.RoleGrandMaster
		okTM := r.TargetTable != "" && c.Role == characters.RoleTableMaster
		if !okGM && !okTM {
			return fmt.Errorf("%w: a ritual of the workspace aims at the Grand Master or at a table master", ErrNotVisible)
		}
	} else if r.TargetTable != "" || c.Role == characters.RoleGrandMaster {
		return fmt.Errorf("%w: %s", ErrNotVisible, r.Target)
	}
	switch {
	case r.Action == ActionCommand && c.Kind != characters.KindTerminal:
		return fmt.Errorf("%w: %s is an agent; a command goes to a simple terminal (spawn)", ErrBadRequest, c.Name)
	case r.Action == ActionPrompt && c.Kind != characters.KindCharacter:
		return fmt.Errorf("%w: %s is a simple terminal; a prompt goes to an agent", ErrBadRequest, c.Name)
	}
	return nil
}

// target finds the character a ritual aims at, now.
func (s *Service) target(ctx context.Context, r Ritual) (characters.Character, error) {
	table := r.Table
	if table == "" {
		table = r.TargetTable
	}
	return s.o.Registry.ByName(ctx, r.Workspace, table, r.Target)
}

// owner is the character the firings that do not go through come back to: the
// Master of the table, or the Grand Master for a ritual of the workspace.
func (s *Service) owner(ctx context.Context, r Ritual) (characters.Character, bool, error) {
	if r.Table == "" {
		return s.o.Registry.GrandMaster(ctx, r.Workspace)
	}
	return s.o.Registry.TableMaster(ctx, r.Workspace, r.Table)
}

// Get finds a ritual of the scope by name or id, for who asks.
func (s *Service) Get(ctx context.Context, by Actor, workspace, table, nameOrID string) (Ritual, error) {
	if err := s.authorize(by, workspace, table); err != nil {
		return Ritual{}, err
	}
	r, ok, err := s.st.ByName(ctx, workspace, table, nameOrID)
	if err != nil {
		return Ritual{}, err
	}
	if !ok {
		if r, err = s.st.Ritual(ctx, nameOrID); err != nil || r.Workspace != workspace || r.Table != table {
			return Ritual{}, fmt.Errorf("%w: %s", ErrNoRitual, nameOrID)
		}
	}
	return r, nil
}

// List lists the rituals of the scope.
func (s *Service) List(ctx context.Context, by Actor, workspace, table string) ([]Ritual, error) {
	if err := s.authorize(by, workspace, table); err != nil {
		return nil, err
	}
	return s.st.OfScope(ctx, workspace, table)
}

// History is the firings of a ritual, oldest first.
func (s *Service) History(ctx context.Context, by Actor, workspace, table, nameOrID string) ([]Firing, error) {
	r, err := s.Get(ctx, by, workspace, table, nameOrID)
	if err != nil {
		return nil, err
	}
	return s.st.Firings(ctx, r.ID)
}

// EditRequest is `ritual edit`: the fields that are set replace the ritual's.
type EditRequest struct {
	By Actor
	// Name and the like are pointers: nil keeps the field.
	NewName         *string
	Target          *string
	TargetTable     *string
	Quest           *string
	Prompt, Command *string
	Schedule        *Schedule
	PreRun          *string
	Timeout         *time.Duration
}

// Edit changes a ritual and checks it again as a whole. A new schedule sets
// the next slot again, and a --once that already fired is enabled again.
func (s *Service) Edit(ctx context.Context, id string, req EditRequest) (Ritual, error) {
	cur, err := s.st.Ritual(ctx, id)
	if err != nil {
		return Ritual{}, err
	}
	if err := s.authorize(req.By, cur.Workspace, cur.Table); err != nil {
		return Ritual{}, err
	}
	r := cur
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	set(&r.Name, req.NewName)
	set(&r.Target, req.Target)
	set(&r.TargetTable, req.TargetTable)
	set(&r.PreRun, req.PreRun)
	if req.Quest != nil {
		r.Quest = strings.TrimSpace(*req.Quest)
		if r.Quest != "" {
			r.Action, r.Target, r.TargetTable, r.Content = ActionQuest, "", "", ""
		}
	}
	if req.Prompt != nil && req.Command != nil {
		return Ritual{}, fmt.Errorf("%w: use --prompt or --command, not both", ErrBadRequest)
	}
	if req.Prompt != nil {
		r.Action, r.Content, r.Quest = ActionPrompt, *req.Prompt, ""
	}
	if req.Command != nil {
		r.Action, r.Content, r.Quest = ActionCommand, *req.Command, ""
	}
	if req.Timeout != nil {
		r.Timeout = *req.Timeout
	} else if req.Schedule != nil && cur.Timeout == defaultTimeout(cur.Schedule) {
		r.Timeout = 0 // the timeout was the default of the old schedule: the new one has its own
	}
	now := s.clk.Now()
	if req.Schedule != nil {
		r.Schedule = *req.Schedule
		r.NextRun = r.Schedule.Next(now, s.zone(ctx, r.Workspace))
		if r.Schedule.Kind == Once {
			r.NextRun = r.Schedule.At
		}
		if r.State == Finished {
			r.State = Enabled
		}
	}
	if err := s.validate(ctx, &r, now, req.Schedule != nil); err != nil {
		return Ritual{}, err
	}
	r.UpdatedAt = now
	if err := s.st.Do(ctx, func(t *Tx) error { return t.Update(r) }); err != nil {
		return Ritual{}, err
	}
	return r, nil
}

// SetEnabled turns a ritual on or off without deleting it. Turning it off
// skips the firings it has in the queue; turning it on sets the next slot from
// now (a --once whose time passed cannot be turned on: edit it).
func (s *Service) SetEnabled(ctx context.Context, id string, by Actor, enabled bool) (Ritual, error) {
	r, err := s.st.Ritual(ctx, id)
	if err != nil {
		return Ritual{}, err
	}
	if err := s.authorize(by, r.Workspace, r.Table); err != nil {
		return Ritual{}, err
	}
	now := s.clk.Now()
	if !enabled {
		err = s.st.Do(ctx, func(t *Tx) error {
			if r.State == Enabled {
				r.State = Disabled
			}
			r.UpdatedAt = now
			if err := t.Update(r); err != nil {
				return err
			}
			_, err := t.tx.ExecContext(ctx, `UPDATE ritual_firings SET state = 'skipped', reason = 'ritual desativado', updated_at = ? WHERE ritual = ? AND state IN ('queued', 'pre_running', 'delivering')`,
				ms(now), r.ID)
			return err
		})
		return r, err
	}
	if r.State == Enabled {
		return r, nil
	}
	if r.Schedule.Kind == Once && !r.Schedule.At.After(now) {
		return Ritual{}, fmt.Errorf("%w: --once is in the past; edit the ritual", ErrInvalidSchedule)
	}
	r.State = Enabled
	r.NextRun = r.Schedule.Next(now, s.zone(ctx, r.Workspace))
	if r.Schedule.Kind == Once {
		r.NextRun = r.Schedule.At
	}
	r.UpdatedAt = now
	return r, s.st.Do(ctx, func(t *Tx) error { return t.Update(r) })
}

// ApprovalPendingError: a character asked to delete a ritual and the request of
// approval is under way.
type ApprovalPendingError struct{ Request int64 }

func (e *ApprovalPendingError) Error() string {
	return fmt.Sprintf("deleting a ritual needs the explicit request of an authorized user (request %d)", e.Request)
}

// Unwrap makes errors.Is(err, ErrApproval) true.
func (e *ApprovalPendingError) Unwrap() error { return ErrApproval }

// Delete removes a ritual and its history. It is a destructive action
// (docs/cli/convencoes.md): a user who is an Administrator or above asks it
// explicitly by doing it; a character needs the approval, which Approved says
// was given (the flow of milestone 5 asks for it through Approvals).
func (s *Service) Delete(ctx context.Context, id string, by Actor, approved bool) error {
	r, err := s.st.Ritual(ctx, id)
	if err != nil {
		return err
	}
	if err := s.authorize(by, r.Workspace, r.Table); err != nil {
		return err
	}
	if by.Kind == ByCharacter && !approved {
		if s.o.Approvals == nil {
			return ErrApproval
		}
		if gate, ok := s.o.Approvals.(DeleteGate); ok {
			granted, n, err := gate.DeleteAllowed(ctx, by, r)
			if err != nil {
				return err
			}
			if !granted {
				return &ApprovalPendingError{Request: n}
			}
			return s.st.Do(ctx, func(t *Tx) error { return t.Delete(r.ID) })
		}
		n, err := s.o.Approvals.RequestDelete(ctx, by, r)
		if err != nil {
			return err
		}
		return &ApprovalPendingError{Request: n}
	}
	return s.st.Do(ctx, func(t *Tx) error { return t.Delete(r.ID) })
}

// Deactivate implements tables.Rituals: the rituals of a table that ended (and
// the ones of the workspace that aim at its master) are disabled, and what
// they had in the queue is skipped.
func (s *Service) Deactivate(ctx context.Context, workspace, table string) error {
	_, err := s.st.Disable(ctx, workspace, table, s.clk.Now(), "a mesa foi encerrada")
	return err
}
