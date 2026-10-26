package inbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// DefaultMaxQueue is how many items an inbox may hold before the table master
// (or the Grand Master) is warned; items are never discarded.
const DefaultMaxQueue = 50

// Deliverer types an item into the terminal of a character on its machine
// (inbox.deliver, docs/conectividade/protocolo.md): characters.MachineSet for
// the machines of this process, the protocol to a Tower (milestone 5). A
// result of busy gives the item back to the queue.
type Deliverer interface {
	Deliver(ctx context.Context, machine string, d protocol.InboxDeliver) (protocol.InboxResult, error)
}

// Pauses says whether a table, or the whole workspace (table ""), is paused:
// nothing is delivered to a paused table except the messages of users to the
// Grand Master (docs/orquestracao/esperas-e-caixa-de-entrada.md#caixa-de-entrada).
type Pauses interface {
	Paused(ctx context.Context, workspace, table string) (bool, error)
}

// Holds says whether one character, and only it, gets nothing delivered: the
// pause the circuit breaker of the chain of approval puts on a character
// (docs/orquestracao/cadeia-de-aprovacao.md#salvaguardas).
type Holds interface {
	Held(ctx context.Context, characterID string) (bool, error)
}

// Channels takes an answer to a user to the channel it came from (Telegram,
// the panel: milestones 5 and 6). It returns ErrChannelDown when the channel
// cannot take it now; the answer is then kept and sent when ChannelUp is told.
type Channels interface {
	Send(ctx context.Context, o Outbound) error
}

// Users tells the people (milestone 5): the Owner, or one user.
type Users interface {
	NotifyOwner(ctx context.Context, workspace, text string) error
	NotifyUser(ctx context.Context, workspace, user, text string) error
}

// Options are the pieces of the Service.
type Options struct {
	DB       *Store
	Clock    clock.Clock
	Registry characters.Registry
	// Deliverer is required.
	Deliverer Deliverer
	// Pauses, Channels and Users may be nil: nothing is paused, an answer to
	// a user is kept until a channel is told, people are not told.
	Pauses   Pauses
	Holds    Holds
	Channels Channels
	Users    Users
	// MaxQueue defaults to DefaultMaxQueue.
	MaxQueue int
	Logger   *slog.Logger
}

// Service is the inbox of the characters
// (docs/orquestracao/esperas-e-caixa-de-entrada.md#caixa-de-entrada): the
// queue of each one, the delivery only when it is free, the answer to the
// users through the channel they came from, and the notices of the engine.
type Service struct {
	o   Options
	st  *Store
	clk clock.Clock
	log *slog.Logger

	mu     sync.Mutex
	pumps  map[string]*pumpState
	warned map[string]bool
	// unavail remembers who was told the Grand Master is unavailable, and why.
	unavail map[string]string
	// progress hears how far the items that carry an ask went.
	progress AskProgress
}

// AskProgress is told how far an item that carries an ask went: `delivered`
// when it was typed in the terminal of the target and `answering` when the turn
// that handles it began (the partial responses of the `ask`,
// docs/arquitetura/canal-local.md#respostas-parciais).
type AskProgress interface {
	AskProgress(ctx context.Context, ask, status string)
}

// SetAskProgress sets who hears the progress of the asks (*waits.Service).
func (s *Service) SetAskProgress(p AskProgress) {
	s.mu.Lock()
	s.progress = p
	s.mu.Unlock()
}

func (s *Service) tellProgress(ctx context.Context, ask, status string) {
	if ask == "" {
		return
	}
	s.mu.Lock()
	p := s.progress
	s.mu.Unlock()
	if p != nil {
		p.AskProgress(ctx, ask, status)
	}
}

var (
	_ characters.Observer   = (*Service)(nil)
	_ characters.Notifier   = (*Service)(nil)
	_ characters.InboxProbe = (*Service)(nil)
)

// NewService returns the Service.
func NewService(o Options) (*Service, error) {
	switch {
	case o.DB == nil || o.DB.DB == nil:
		return nil, errors.New("inbox: the service needs the store")
	case o.Registry.DB == nil:
		return nil, errors.New("inbox: the service needs the registry of characters")
	case o.Deliverer == nil:
		return nil, errors.New("inbox: the service needs the way to deliver to the machines")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.MaxQueue <= 0 {
		o.MaxQueue = DefaultMaxQueue
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{o: o, st: o.DB, clk: o.Clock, log: o.Logger, pumps: map[string]*pumpState{}, warned: map[string]bool{}, unavail: map[string]string{}}, nil
}

// ByKey finds the item that was queued with the idempotency key (Refs.Key).
func (s *Service) ByKey(ctx context.Context, key string) (Item, bool, error) {
	return s.st.ByKey(ctx, key)
}

// ForRun finds the item `quest` of a run of a step that is in the inbox of the
// target.
func (s *Service) ForRun(ctx context.Context, run int64, target string) (Item, bool, error) {
	return s.st.ForRun(ctx, run, target)
}

// Store gives access to the tables.
func (s *Service) Store() *Store { return s.st }

// pumpState says whether a character is being delivered to, and whether
// someone asked again meanwhile.
type pumpState struct{ running, again bool }

// Enqueue puts an item in the inbox of a character and, if the character is
// free, delivers it. A delivery that fails does not fail the enqueue: the item
// stays queued and the next change of state of the character tries again.
func (s *Service) Enqueue(ctx context.Context, n NewItem) (Item, error) {
	switch {
	case !n.Kind.Valid():
		return Item{}, fmt.Errorf("%w: unknown type %q", ErrBadItem, n.Kind)
	case n.Target == "" || n.Workspace == "":
		return Item{}, fmt.Errorf("%w: the item needs a target and a workspace", ErrBadItem)
	case n.Kind == KindUser && n.Origin == nil:
		return Item{}, fmt.Errorf("%w: a message of a user carries its origin", ErrBadItem)
	}
	c, err := s.o.Registry.Get(ctx, n.Target)
	if err != nil {
		return Item{}, err
	}
	if c.State == characters.Stopped {
		return Item{}, fmt.Errorf("%w: %s ended", ErrBadItem, c.Name)
	}
	if n.Refs.Key != "" {
		// A repeat of an item that was queued: it is not queued twice.
		if it, ok, err := s.st.ByKey(ctx, n.Refs.Key); err != nil {
			return Item{}, err
		} else if ok {
			return it, nil
		}
	}
	it, err := s.st.Insert(ctx, n, s.clk.Now())
	if err != nil {
		return Item{}, err
	}
	if err := s.pump(ctx, n.Target); err != nil {
		s.log.Warn("inbox: the delivery was not made, the item stays queued", "item", it.ID, "target", n.Target, "err", err)
	}
	s.checkLong(ctx, c)
	s.tellUnavailable(ctx, c, n)
	return it, nil
}

// tellUnavailable answers, as "Sistema", a user who writes to a Grand Master
// that cannot run (interrupted: it could not start, or its machine is away):
// the message stays in the inbox and is delivered when the Grand Master is
// back, and the user learns it is unavailable and why
// (docs/orquestracao/grao-mestre.md#regras-e-erros).
func (s *Service) tellUnavailable(ctx context.Context, c characters.Character, n NewItem) {
	if n.Kind != KindUser || c.Role != characters.RoleGrandMaster || n.Origin == nil {
		return
	}
	key := n.Workspace + "\x00" + n.Origin.User
	s.mu.Lock()
	if c.State != characters.Interrupted {
		delete(s.unavail, key)
		s.mu.Unlock()
		return
	}
	// once per user and reason, until the Grand Master runs again
	if s.unavail[key] == c.Reason {
		s.mu.Unlock()
		return
	}
	s.unavail[key] = c.Reason
	s.mu.Unlock()
	text := "O Grão-Mestre está indisponível: " + c.Reason + ". A sua mensagem ficou na fila e segue quando ele voltar."
	if err := s.tellUser(ctx, n.Workspace, n.Origin.User, text); err != nil {
		s.log.Warn("inbox: the user was not told the Grand Master is unavailable", "workspace", n.Workspace, "err", err)
	}
}

// checkLong warns the table master (the Grand Master, for a table master)
// when an inbox passes the limit; once, until it is below it again.
func (s *Service) checkLong(ctx context.Context, c characters.Character) {
	n, err := s.st.Count(ctx, c.ID)
	if err != nil {
		return
	}
	s.mu.Lock()
	warned := s.warned[c.ID]
	switch {
	case n <= s.o.MaxQueue:
		delete(s.warned, c.ID)
		s.mu.Unlock()
		return
	case warned:
		s.mu.Unlock()
		return
	}
	s.warned[c.ID] = true
	s.mu.Unlock()
	to := characters.Recipient{Kind: characters.ToTableMaster}
	if c.Role != characters.RoleCharacter {
		to = characters.Recipient{Kind: characters.ToGrandMaster}
	}
	if err := s.Notify(ctx, characters.Notice{Kind: characters.NoticeQueueLong, To: to, Workspace: c.Workspace, Table: c.Table, Character: c.ID, Name: c.Name,
		Text: fmt.Sprintf("A caixa de entrada de %s tem %d itens (o limite de aviso é %d). Nada é descartado; veja `check` e `waits`.", c.Name, n, s.o.MaxQueue)}); err != nil {
		s.log.Warn("inbox: the warning of the long queue was not given", "target", c.ID, "err", err)
	}
}

// Pump delivers the next item of a character if it is free, the table is not
// paused, and no item is being delivered to it: one item at a time, the next
// only after the end of the turn the first one provoked.
func (s *Service) Pump(ctx context.Context, target string) error { return s.pump(ctx, target) }

func (s *Service) pump(ctx context.Context, target string) error {
	// One delivery at a time per character. A request that comes while one is
	// under way (the delivery itself makes the machine report a change of
	// state, which asks again) does not wait for it: it leaves a note, and the
	// one that runs looks again before it ends.
	s.mu.Lock()
	ps := s.pumps[target]
	if ps == nil {
		ps = new(pumpState)
		s.pumps[target] = ps
	}
	if ps.running {
		ps.again = true
		s.mu.Unlock()
		return nil
	}
	ps.running = true
	s.mu.Unlock()
	for {
		err := s.pumpOnce(ctx, target)
		s.mu.Lock()
		if !ps.again {
			ps.running = false
			s.mu.Unlock()
			return err
		}
		ps.again = false
		s.mu.Unlock()
	}
}

func (s *Service) pumpOnce(ctx context.Context, target string) error {
	c, err := s.o.Registry.Get(ctx, target)
	switch {
	case errors.Is(err, characters.ErrNoCharacter):
		return nil
	case err != nil:
		return err
	case !c.State.Receives() || c.Kind == characters.KindTerminal || c.TerminalID == "":
		return nil
	}
	if _, busy, err := s.st.InFlight(ctx, target); err != nil || busy {
		return err
	}
	if s.o.Holds != nil {
		if held, err := s.o.Holds.Held(ctx, target); err != nil || held {
			return err
		}
	}
	onlyUsers := false
	if s.o.Pauses != nil {
		paused, err := s.o.Pauses.Paused(ctx, c.Workspace, c.Table)
		if err != nil {
			return err
		}
		if paused {
			if c.Role != characters.RoleGrandMaster {
				return nil
			}
			onlyUsers = true
		}
	}
	it, ok, err := s.st.Next(ctx, target, onlyUsers)
	if err != nil || !ok {
		return err
	}
	if typing, err := s.st.MarkTyping(ctx, it.ID, s.clk.Now()); err != nil || !typing {
		return err
	}
	res, err := s.o.Deliverer.Deliver(ctx, c.Machine, protocol.InboxDeliver{TerminalID: c.TerminalID, ItemID: it.ID, Text: it.Body(), Header: it.Header()})
	if err == nil && res.Status == protocol.InboxDelivered {
		// A message of another agent is a task: the `done` of the turn that
		// handles it answers it, and it is the active task of the character
		// until then (docs/cli/comunicacao.md#mestre-done). The steps of a
		// quest mark themselves; what a user says, an answer, a wake-up or a
		// notice is no task.
		if it.Kind == KindAgent {
			if terr := s.o.Registry.SetTaskOpen(ctx, c.ID, true); terr != nil {
				s.log.Warn("inbox: a task was not marked", "item", it.ID, "err", terr)
			}
		}
		s.tellProgress(ctx, it.Refs.Ask, "delivered")
		return nil
	}
	if rerr := s.st.Requeue(ctx, it.ID); rerr != nil {
		s.log.Warn("inbox: an item was not given back to the queue", "item", it.ID, "err", rerr)
	}
	return err // busy is not an error: the next change of state tries again
}

// PumpAll delivers to every character that has something queued: after a
// restart, or when a table is resumed.
func (s *Service) PumpAll(ctx context.Context) error {
	rows, err := s.st.DB.Reader().QueryContext(ctx, `SELECT DISTINCT target FROM inbox_messages WHERE state = 'queued'`)
	if err != nil {
		return err
	}
	var targets []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, t)
	}
	rows.Close()
	var errs []error
	for _, t := range targets {
		if err := s.pump(ctx, t); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Recover is what the engine does when it starts
// (docs/orquestracao/esperas-e-caixa-de-entrada.md#persistência-e-retomada):
// an item that was being delivered and whose turn was not recorded goes back to
// the queue, and everything that can be delivered is.
func (s *Service) Recover(ctx context.Context) error {
	if _, err := s.st.RequeueEverything(ctx); err != nil {
		return err
	}
	return s.PumpAll(ctx)
}

// StateChanged implements characters.Observer: a character that is free again
// ends the turn of the item it was delivered and gets the next one; one that
// was interrupted gets the item back in the queue; one that ended loses what
// was left.
func (s *Service) StateChanged(ctx context.Context, c characters.Character, _ characters.State) error {
	switch c.State {
	case characters.Idle, characters.Waiting:
		// A delivery that failed on the machine puts the character back to idle
		// with that reason: its turn did not happen, and the item stays in
		// flight for the failure to give it back.
		if c.Reason != characters.ReasonDeliveryFailed {
			if _, err := s.st.DeliverInFlight(ctx, c.ID, s.clk.Now()); err != nil {
				return err
			}
		}
		return s.pump(ctx, c.ID)
	case characters.Interrupted:
		_, err := s.st.RequeueAll(ctx, c.ID)
		return err
	case characters.Stopped:
		_, err := s.st.CancelFor(ctx, c.ID, s.clk.Now())
		return err
	}
	return nil
}

// Done implements characters.Observer; the inbox has nothing to do with it.
func (s *Service) Done(context.Context, characters.Character, string) error { return nil }

// TurnEnded implements characters.Observer; the end of the turn is seen as
// the change of state.
func (s *Service) TurnEnded(context.Context, characters.Character, string) error { return nil }

// Confirmed implements providers.Inbox: the turn of the delivery began.
func (s *Service) Confirmed(ctx context.Context, id string) error {
	_, err := s.st.Deliver(ctx, id, s.clk.Now(), false)
	if err == nil {
		if it, gerr := s.st.Get(ctx, id); gerr == nil && it.Kind == KindAgent {
			s.tellProgress(ctx, it.Refs.Ask, "answering")
		}
	}
	return err
}

// Unconfirmed implements providers.Inbox: no turn began within the time
// limit. The item is recorded as delivered without confirmation and is never
// typed again; the table master was told by the supervision.
func (s *Service) Unconfirmed(ctx context.Context, id string) error {
	_, err := s.st.Deliver(ctx, id, s.clk.Now(), true)
	return err
}

// Pending implements characters.InboxProbe: how many items wait for a
// character.
func (s *Service) Pending(ctx context.Context, characterID string) (int, error) {
	return s.st.Count(ctx, characterID)
}

// OnResult takes the result of a delivery that travelled the protocol
// (inbox.result, docs/conectividade/protocolo.md): busy gives an item that is
// still being delivered back to the queue; delivered changes nothing (the
// turn confirms it).
func (s *Service) OnResult(ctx context.Context, r protocol.InboxResult) error {
	if r.Status != protocol.InboxBusy {
		return nil
	}
	return s.st.Requeue(ctx, r.ItemID)
}

// TargetOf is the character an item is for: the Citadel checks that the machine
// that confirms a delivery is the one the character runs on.
func (s *Service) TargetOf(ctx context.Context, id string) (string, error) {
	it, err := s.st.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return it.Target, nil
}

// ResultHandler is the handler of inbox.result in the router of the Citadel.
func (s *Service) ResultHandler() protocol.Handler {
	return protocol.HandlerFunc(func(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
		r, ok := m.Payload.(*protocol.InboxResult)
		if !ok {
			return nil, fmt.Errorf("inbox: a payload %T is not an inbox.result", m.Payload)
		}
		return nil, s.OnResult(ctx, *r)
	})
}

// List gives the items that wait for a character, in the order of delivery
// (`mestre inbox`).
func (s *Service) List(ctx context.Context, characterID string) ([]Item, error) {
	return s.st.Pending(ctx, characterID)
}

// Cancel closes a queued item that stopped mattering (the approval was
// decided somewhere else, the ask ended).
func (s *Service) Cancel(ctx context.Context, id string, to State) error {
	_, err := s.st.End(ctx, id, to, s.clk.Now())
	return err
}
