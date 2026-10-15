package characters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Limits are the simultaneous characters allowed
// (docs/orquestracao/personagens.md#limites). The Grand Master and the table
// masters count nothing and are never held back; the simple terminals count.
type Limits struct {
	PerMachine, PerTable, PerWorkspace int
}

// DefaultLimits are the defaults of the document: 8, 6 and 16.
func DefaultLimits() Limits { return Limits{PerMachine: 8, PerTable: 6, PerWorkspace: 16} }

// DefaultSlotTimeout is how long a request waits for a vacancy
// (docs/orquestracao/esperas-e-caixa-de-entrada.md#tempos-limite).
const DefaultSlotTimeout = 30 * time.Minute

// DefaultEphemeralGrace is the wait of an ephemeral character after its done
// with an empty inbox and no new message.
const DefaultEphemeralGrace = 5 * time.Minute

// ServiceOptions are the pieces of the Service.
type ServiceOptions struct {
	Registry Registry
	Clock    clock.Clock
	Presets  Presets
	Tables   Tables
	Machines Machines
	Builder  Builder
	Notifier Notifier
	// Scrolls, Inbox and Usage may be nil: nothing is written, the inbox is
	// empty, the use is not told.
	Scrolls Scrolls
	Inbox   InboxProbe
	Usage   UsageProbe
	// Limits default to DefaultLimits; the zero of a field is its default.
	Limits Limits
	// SlotTimeout and EphemeralGrace default to the constants above.
	SlotTimeout, EphemeralGrace time.Duration
	// LogRetention defaults to 90 days.
	LogRetention time.Duration
	Logger       *slog.Logger
}

// Service is the Citadel's half of the characters.
type Service struct {
	o   ServiceOptions
	clk clock.Clock
	ids *protocol.IDGen
	log *slog.Logger

	mu sync.Mutex
	// seq is the last report applied of each character.
	seq map[string]uint64
	// satisfied maps a wait for a vacancy to the character it created.
	satisfied map[string]string
	// summarized are the characters whose summary was already written.
	summarized map[string]bool
	// refused are the refusals already recorded, by character and cause.
	refused    map[string]bool
	lastPruned time.Time
}

// NewService returns the Service.
func NewService(o ServiceOptions) (*Service, error) {
	switch {
	case o.Registry.DB == nil:
		return nil, errors.New("characters: the service needs the registry")
	case o.Presets == nil || o.Tables == nil || o.Machines == nil || o.Builder == nil || o.Notifier == nil:
		return nil, errors.New("characters: the service needs the presets, the tables, the machines, the builder and the notifier")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	d := DefaultLimits()
	if o.Limits.PerMachine <= 0 {
		o.Limits.PerMachine = d.PerMachine
	}
	if o.Limits.PerTable <= 0 {
		o.Limits.PerTable = d.PerTable
	}
	if o.Limits.PerWorkspace <= 0 {
		o.Limits.PerWorkspace = d.PerWorkspace
	}
	if o.SlotTimeout <= 0 {
		o.SlotTimeout = DefaultSlotTimeout
	}
	if o.EphemeralGrace <= 0 {
		o.EphemeralGrace = DefaultEphemeralGrace
	}
	if o.LogRetention <= 0 {
		o.LogRetention = DefaultLogRetention
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{o: o, clk: o.Clock, ids: protocol.NewIDGen(o.Clock), log: o.Logger,
		seq: map[string]uint64{}, satisfied: map[string]string{}, summarized: map[string]bool{}, refused: map[string]bool{}}, nil
}

// CreateRequest asks for a character (`recruit`) or a simple terminal
// (`spawn`), or is the step of a quest that needs one.
type CreateRequest struct {
	// Name defaults to `<sheet> <n>`.
	Name string
	// Sheet makes an agent; Base, with no Sheet, a simple terminal.
	Sheet, Base string
	// Class replaces the class of the sheet.
	Class string
	// Machine, when set, is the only machine considered (its id or name).
	Machine          string
	Workspace, Table string
	Role             Role
	Creator          Creator
	// Quest is the quest the character is for (an ephemeral of a quest ends
	// with it).
	Quest string
	// ReplaceOf is the character this one takes the place of
	// (`recruit --replace`): it waits for the end of the old one, and
	// Prefer is the machine of the old one, kept if it still fits.
	ReplaceOf, Prefer string
	// Inherit, when set, is the creator the new character is recorded with
	// (the replaced one's), not the one who asked.
	Inherit *Creator
}

// CreateResult is what Create answers. Pending is the id of the wait for a
// vacancy (waiting_slot) when the request could not run now: the answer is
// then "pending" and the character arrives later, through the inbox.
type CreateResult struct {
	Character Character
	Pending   string
}

// The limits that hold a request.
const (
	LimitMachine   = "machine"
	LimitTable     = "table"
	LimitWorkspace = "workspace"
	LimitSheet     = "sheet"
	// limitReplace is the wait for the end of the character being replaced.
	limitReplace = "replace"
)

// blocked is a limit that holds a request.
type blocked struct {
	limit   string
	max     int
	holders []string
}

// placement is a request that passed the checks and has a machine.
type placement struct {
	req   CreateRequest
	snap  presets.Snapshot
	kind  Kind
	table TableInfo
	// fit are the machines that meet the requirements, as ids; machine is
	// the one chosen among them (limitsHold).
	fit     []string
	machine string
	name    string
	class   string
	lifecyc Lifecycle
	maxIdle time.Duration
}

// Create runs a request now or, if a limit holds it, queues it as a wait for
// a vacancy.
func (s *Service) Create(ctx context.Context, req CreateRequest) (CreateResult, error) {
	if req.Role == "" {
		req.Role = RoleCharacter
	}
	if req.Workspace == "" {
		return CreateResult{}, fmt.Errorf("%w: no workspace", ErrLevelDenied)
	}
	waitID := ""
	ch, wait, err := s.attempt(ctx, req, "")
	if err != nil {
		return CreateResult{}, err
	}
	waitID = wait
	if waitID != "" {
		// Queued: a vacancy may already be there for a request that
		// arrived first than another; run the queue in order.
		s.mu.Lock()
		s.satisfied[waitID] = "" // this call wants to know the character the wait makes
		s.mu.Unlock()
		if err := s.Pump(ctx); err != nil {
			return CreateResult{}, err
		}
		w, err := s.o.Registry.SlotWaitByID(ctx, waitID)
		if err != nil {
			return CreateResult{}, err
		}
		if w.State == SlotSatisfied {
			if c, ok, err := s.createdBy(ctx, waitID); err == nil && ok {
				return CreateResult{Character: c}, nil
			}
		}
		s.mu.Lock()
		delete(s.satisfied, waitID)
		s.mu.Unlock()
		return CreateResult{Pending: waitID}, nil
	}
	return CreateResult{Character: ch}, nil
}

// createdBy finds the character a wait ended up creating.
func (s *Service) createdBy(ctx context.Context, waitID string) (Character, bool, error) {
	s.mu.Lock()
	id := s.satisfied[waitID]
	delete(s.satisfied, waitID)
	s.mu.Unlock()
	if id == "" {
		return Character{}, false, nil
	}
	c, err := s.o.Registry.Get(ctx, id)
	return c, err == nil, err
}

// attempt checks a request and, if no limit holds it, makes the character and
// starts it. If a limit holds it (or a request that arrived before it still
// waits), the request is queued, or stays queued when waitID names it; the id
// of the wait is then returned.
func (s *Service) attempt(ctx context.Context, req CreateRequest, waitID string) (Character, string, error) {
	p, err := s.check(ctx, req)
	if err != nil {
		return Character{}, "", s.failed(ctx, req, waitID, err)
	}
	s.mu.Lock()
	hold, err := s.limitsHold(ctx, &p)
	if err == nil && hold == nil && waitID == "" {
		// A request that arrives while others wait in the same workspace
		// goes behind them.
		var waits []SlotWait
		if waits, err = s.o.Registry.ActiveSlotWaits(ctx); err == nil {
			for _, w := range waits {
				var other CreateRequest
				if json.Unmarshal(w.Request, &other) == nil && other.Workspace == req.Workspace {
					hold = &blocked{limit: "fila", max: 0, holders: []string{"pedidos anteriores"}}
					break
				}
			}
		}
	}
	if err != nil {
		s.mu.Unlock()
		return Character{}, "", err
	}
	if hold != nil {
		defer s.mu.Unlock()
		if waitID == "" {
			waitID, err = s.queue(ctx, req, *hold)
			return Character{}, waitID, err
		}
		_ = s.o.Registry.SetSlotHold(ctx, waitID, hold.limit, strings.Join(hold.holders, ", "))
		return Character{}, waitID, nil
	}
	ch, err := s.insert(ctx, p)
	s.mu.Unlock()
	if err != nil {
		return Character{}, "", s.failed(ctx, req, waitID, err)
	}
	if waitID != "" {
		if _, err := s.o.Registry.EndSlotWait(ctx, waitID, SlotSatisfied, s.clk.Now()); err != nil {
			s.log.Warn("characters: the wait for a vacancy was not closed", "wait", waitID, "err", err)
		}
		s.mu.Lock()
		if _, ok := s.satisfied[waitID]; ok {
			s.satisfied[waitID] = ch.ID
		}
		s.mu.Unlock()
	}
	if err := s.launch(ctx, ch, p, false); err != nil {
		return Character{}, "", err
	}
	got, err := s.o.Registry.Get(ctx, ch.ID)
	return got, "", err
}

// failed turns a failure of a request into what is told about it: the owner
// when no machine fits, and the wait is closed when the request came from the
// queue.
func (s *Service) failed(ctx context.Context, req CreateRequest, waitID string, err error) error {
	if errors.Is(err, machinesheet.ErrNoMachine) {
		_ = s.o.Notifier.Notify(ctx, Notice{Kind: NoticeNoCapableMachine, To: Recipient{Kind: ToOwner}, Workspace: req.Workspace,
			Table: req.Table, Creator: req.Creator, Text: err.Error()})
	}
	if waitID != "" {
		_, _ = s.o.Registry.EndSlotWait(ctx, waitID, SlotCancelled, s.clk.Now())
		_ = s.o.Notifier.Notify(ctx, Notice{Kind: NoticeStartFailed, To: Recipient{Kind: ToCreator}, Workspace: req.Workspace,
			Table: req.Table, Creator: req.Creator, Text: "o pedido de personagem que esperava vaga falhou: " + err.Error()})
	}
	return err
}

// queue records a request that waits for a vacancy.
func (s *Service) queue(ctx context.Context, req CreateRequest, b blocked) (string, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	now := s.clk.Now()
	w := SlotWait{ID: s.ids.NewString(), Request: raw, Limit: b.limit, Holders: strings.Join(b.holders, ", "),
		CreatedAt: now, ExpiresAt: now.Add(s.o.SlotTimeout)}
	if err := s.o.Registry.AddSlotWait(ctx, w); err != nil {
		return "", err
	}
	return w.ID, nil
}

// check validates a request and chooses the machine, without keeping anything.
func (s *Service) check(ctx context.Context, req CreateRequest) (placement, error) {
	p := placement{req: req}
	if err := validNameFor(req.Role, req.Name); err != nil {
		return p, err
	}
	if err := s.authorize(ctx, req); err != nil {
		return p, err
	}
	if req.Table != "" {
		t, err := s.o.Tables.Table(ctx, req.Workspace, req.Table)
		if err != nil {
			return p, err
		}
		if !t.State.Accepts() {
			return p, fmt.Errorf("%w: %s está %s", ErrTableClosed, req.Table, t.State)
		}
		p.table = t
	}
	if err := s.snapshot(ctx, &p); err != nil {
		return p, err
	}
	if err := s.choose(ctx, &p); err != nil {
		return p, err
	}
	switch {
	case req.Name == "" && req.Role == RoleGrandMaster:
		p.name = NameGrandMaster
	case req.Name == "" && req.Role == RoleTableMaster:
		p.name = NameTableMaster
	case req.Name == "":
		cs, err := s.o.Registry.list(ctx, `state <> 'stopped' AND workspace = ? AND table_id = ?`, req.Workspace, req.Table)
		if err != nil {
			return p, err
		}
		keys := map[string]bool{}
		for _, c := range cs {
			keys[nameKey(c.Name)] = true
		}
		p.name = DefaultName(p.snap.Sheet.Name, func(k string) bool { return keys[k] })
	default:
		p.name = req.Name
	}
	return p, nil
}

// snapshot reads the sheet and the base of the request.
func (s *Service) snapshot(ctx context.Context, p *placement) error {
	req := p.req
	switch {
	case req.Sheet != "":
		snap, err := s.o.Presets.Snapshot(ctx, req.Sheet)
		if err != nil {
			return s.badPreset(ctx, err, req.Sheet, "")
		}
		p.snap, p.kind = snap, KindCharacter
		p.class = req.Class
		if p.class == "" {
			p.class = snap.Sheet.Class
		}
		p.lifecyc, p.maxIdle = Lifecycle(snap.Sheet.LifecycleOrDefault()), snap.Sheet.Idle()
		if snap.Sheet.Agent == presets.AgentShell {
			p.kind = KindTerminal
		}
	case req.Base != "":
		b, ok, err := s.o.Presets.Base(ctx, req.Base)
		if err != nil {
			return err
		}
		if !ok {
			return s.badPreset(ctx, errors.New("base"), "", req.Base)
		}
		p.snap = presets.Snapshot{Sheet: presets.Sheet{Name: b.Name, Base: b.Name, Agent: presets.AgentShell}, Base: b}
		p.kind = KindTerminal
	default:
		return fmt.Errorf("%w: no sheet and no base", ErrBadSheet)
	}
	if p.kind == KindTerminal {
		// A simple terminal has no lifecycle of its own: only dismiss, or a
		// user, ends it.
		p.lifecyc, p.maxIdle = Persistent, 0
	}
	return nil
}

// badPreset is the error of a sheet or base that does not exist, with the
// valid ones.
func (s *Service) badPreset(ctx context.Context, cause error, sheet, base string) error {
	var names []string
	what := "a ficha `" + sheet + "`"
	if base != "" {
		what = "a base `" + base + "`"
		bs, _ := s.o.Presets.Bases(ctx)
		for _, b := range bs {
			names = append(names, b.Name)
		}
	} else {
		ss, _ := s.o.Presets.Sheets(ctx)
		for _, x := range ss {
			names = append(names, x.Name)
		}
	}
	slices.Sort(names)
	return fmt.Errorf("%w: %s não existe ou não combina (%v); válidas: %s", ErrBadSheet, what, cause, strings.Join(names, ", "))
}

// choose picks the machine of the request: the machines that meet the
// requirements, narrowed by --machine, in the order of preference, and the
// first with a vacancy; none that meets them is a failure, all full is a wait
// (limitsHold).
func (s *Service) choose(ctx context.Context, p *placement) error {
	req := p.req
	var choice machinesheet.Choice
	var err error
	if req.Sheet != "" {
		choice, err = s.o.Presets.Choose(ctx, req.Sheet, p.class)
	} else {
		var machines []machinesheet.Sheet
		if machines, err = s.o.Presets.Machines(ctx); err == nil {
			var need machinesheet.Requirements
			if need, err = presets.Requirements(p.snap.Base, p.snap.Sheet, nil); err == nil {
				choice = presets.Choose(p.snap.Base, need, machines)
			}
		}
	}
	if err != nil {
		return err
	}
	if req.Machine != "" {
		var only []machinesheet.Verdict
		for _, v := range choice.Verdicts {
			if v.MachineID == req.Machine || v.Name == req.Machine {
				only = append(only, v)
			}
		}
		if len(only) == 0 {
			return &machinesheet.NoMachineError{Verdicts: []machinesheet.Verdict{{MachineID: req.Machine, Name: req.Machine, Unmet: "machine", Reason: "a máquina não existe na Guilda"}}}
		}
		choice = machinesheet.Choice{Verdicts: only}
	}
	if err := choice.Err(); err != nil {
		return err
	}
	fit := choice.Fit()
	ids := make([]string, len(fit))
	for i, v := range fit {
		ids[i] = v.MachineID
	}
	p.fit = ids
	return nil
}
