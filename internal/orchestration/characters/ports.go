package characters

import (
	"context"
	"errors"
	"time"

	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/orchestration/presets"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/sessionzero"
)

// Presets are the sheets, the bases and the sheets of the machines the
// Citadel chooses from (presets.Catalog, see CatalogPresets).
type Presets interface {
	// Snapshot copies a sheet and its base as they are now.
	Snapshot(ctx context.Context, sheet string) (presets.Snapshot, error)
	Base(ctx context.Context, name string) (presets.Base, bool, error)
	// Choose matches the machines against a character of the sheet, with the
	// class it will have ("" is the sheet's).
	Choose(ctx context.Context, sheet, class string) (machinesheet.Choice, error)
	// Machines are the sheets of the machines of the Guild, with Online
	// set from the connection and the heartbeat.
	Machines(ctx context.Context) ([]machinesheet.Sheet, error)
	// Sheets and Bases list the valid ones, for the error of a name that
	// does not exist.
	Sheets(ctx context.Context) ([]presets.Sheet, error)
	Bases(ctx context.Context) ([]presets.Base, error)
}

// CatalogPresets is the Presets of a presets.Catalog.
type CatalogPresets struct{ *presets.Catalog }

// Machines implements Presets.
func (c CatalogPresets) Machines(ctx context.Context) ([]machinesheet.Sheet, error) {
	if c.Catalog.Machines == nil {
		return nil, nil
	}
	return c.Catalog.Machines.Sheets(ctx)
}

// TableState is the state of a table (docs/orquestracao/mesas.md).
type TableState string

// The states of a table that matter here.
const (
	TableOpen       TableState = "open"
	TableWorking    TableState = "working"
	TableInReview   TableState = "in_review"
	TableClosing    TableState = "closing"
	TableClosed     TableState = "closed"
	TableCancelling TableState = "cancelling"
	TableCancelled  TableState = "cancelled"
)

// Accepts says whether a table in the state takes new characters.
func (s TableState) Accepts() bool {
	switch s {
	case TableClosing, TableClosed, TableCancelling, TableCancelled:
		return false
	}
	return true
}

// TableInfo is what the characters need of a table (milestone 4, stage 8).
type TableInfo struct {
	ID, Workspace string
	State         TableState
	Epic          int
	Branch        string
	// Worktrees maps a machine to the worktree of the table there.
	Worktrees map[string]string
}

// Tables gives the tables.
type Tables interface {
	// Table returns the table, or an error if it does not exist.
	Table(ctx context.Context, workspace, id string) (TableInfo, error)
	// Worktree is the folder characters of the workspace work in on a
	// machine: the worktree of the table, creating it there first if the
	// table has none on that machine (docs/orquestracao/mesas.md); the main
	// clone for a table "" (the Grand Master).
	Worktree(ctx context.Context, workspace, table, machine string) (string, error)
}

// StartCommand is what the Citadel sends the engine of a machine to start a
// character: the request the session zero needs, rendered by the Citadel
// (docs/sessao-zero/personagem.md#o-que-o-motor-monta).
type StartCommand struct {
	Character string
	// Terminal is the id of the terminal to create.
	Terminal string
	Kind     Kind
	Request  sessionzero.CharacterRequest
	// Resumed is true for the restart of an interrupted character: the
	// request carries the saved session of the provider, and the files stay.
	Resumed bool
	// Note is the first line of what to tell it after a restart without a
	// session to resume (the resumption message, docs/orquestracao/personagens.md#retomada-após-reinício).
	ResumeMessage string
}

// StartResult is what the machine answers.
type StartResult struct {
	Terminal string
	// Session is the id of the session of the provider when it is known
	// before the first hook (Claude Code).
	Session  string
	Warnings []string
}

// StopCommand ends a character on its machine.
type StopCommand struct {
	Character string
	// Final is the end for good (the files of the character go away); false
	// is a restart that resumes.
	Final  bool
	Reason string
	// Abandon ends a character that is still starting: the Citadel gave up on the start.
	// Terminal, when given, is the one of that start; a character in another is not touched.
	Abandon  bool
	Terminal string
}

// Machines is the Citadel's way to the engine of each machine: in-process for
// the Citadel's own, the WSS connection for a Tower (milestone 5 wires it).
type Machines interface {
	Start(ctx context.Context, machine string, cmd StartCommand) (StartResult, error)
	Stop(ctx context.Context, machine string, cmd StopCommand) error
}

// MachineSet routes to the engine of each machine.
type MachineSet map[string]*Runtime

// Start implements Machines.
func (m MachineSet) Start(ctx context.Context, machine string, cmd StartCommand) (StartResult, error) {
	rt, ok := m[machine]
	if !ok {
		return StartResult{}, &MachineUnreachableError{Machine: machine}
	}
	return rt.Start(ctx, cmd)
}

// Stop implements Machines.
func (m MachineSet) Stop(ctx context.Context, machine string, cmd StopCommand) error {
	rt, ok := m[machine]
	if !ok {
		return &MachineUnreachableError{Machine: machine}
	}
	return rt.Stop(ctx, cmd)
}

// Deliver implements the delivery of the inbox to the engine of the machine
// (docs/conectividade/protocolo.md, inbox.deliver).
func (m MachineSet) Deliver(ctx context.Context, machine string, d protocol.InboxDeliver) (protocol.InboxResult, error) {
	rt, ok := m[machine]
	if !ok {
		return protocol.InboxResult{}, &MachineUnreachableError{Machine: machine}
	}
	return rt.DeliverInbox(ctx, d)
}

// Input types raw input into the terminal of a character of the machine
// (`ask --raw`).
func (m MachineSet) Input(ctx context.Context, machine, character string, data []byte) error {
	rt, ok := m[machine]
	if !ok {
		return &MachineUnreachableError{Machine: machine}
	}
	return rt.Input(ctx, character, data)
}

// Screen reads the screen of the terminal of a character of the machine
// (`check`).
func (m MachineSet) Screen(ctx context.Context, machine, character string) (Screen, error) {
	rt, ok := m[machine]
	if !ok {
		return Screen{}, &MachineUnreachableError{Machine: machine}
	}
	return rt.Screen(ctx, character)
}

// ScreenLines reads the screen with up to lines lines of the history
// (`check --lines`).
func (m MachineSet) ScreenLines(ctx context.Context, machine, character string, lines int) (Screen, error) {
	rt, ok := m[machine]
	if !ok {
		return Screen{}, &MachineUnreachableError{Machine: machine}
	}
	return rt.ScreenLines(ctx, character, lines)
}

// MachineUnreachableError: the engine of the machine cannot be reached.
type MachineUnreachableError struct{ Machine string }

func (e *MachineUnreachableError) Error() string {
	return "the machine " + e.Machine + " cannot be reached"
}

// BuildInput is what the Citadel knows when it renders the request of a
// character.
type BuildInput struct {
	Character Character
	Snapshot  presets.Snapshot
	Table     TableInfo
	// Dir is the worktree on the machine.
	Dir string
	// ResumeSessionID is the saved session of an interrupted character;
	// NewSessionID the id for a new Claude Code session.
	ResumeSessionID, NewSessionID string
	// Resumed is true for the restart of an interrupted character.
	Resumed bool
}

// Builder renders the request of the session zero of a character: the class
// and its rules, the identity of its commits, the decrees and the quest.
// Milestone 5 (decrees, identities) completes it; the tests use a fake.
type Builder interface {
	Build(ctx context.Context, in BuildInput) (sessionzero.CharacterRequest, error)
}

// Recipient says who a notice is for.
type Recipient struct {
	Kind RecipientKind
	// ID is the character or the user, when the kind names one.
	ID string
}

// RecipientKind is who gets a notice.
type RecipientKind string

// The recipients.
const (
	// ToCreator: whoever asked for the character (a system item in the inbox
	// of a character, a message to a user).
	ToCreator RecipientKind = "creator"
	// ToTableMaster: the table master of the table of the notice.
	ToTableMaster RecipientKind = "table_master"
	// ToGrandMaster: the Grand Master of the workspace.
	ToGrandMaster RecipientKind = "grand_master"
	// ToOwner: the Owner, by Telegram and the panel.
	ToOwner RecipientKind = "owner"
)

// NoticeKind is what a notice is about.
type NoticeKind string

// The notices.
const (
	NoticeNoCapableMachine NoticeKind = "no_capable_machine"
	NoticeSlotTimeout      NoticeKind = "slot_timeout"
	NoticeStuck            NoticeKind = "stuck"
	NoticeInterrupted      NoticeKind = "interrupted"
	NoticeMachineDown      NoticeKind = "machine_down"
	NoticeBlockedInput     NoticeKind = "blocked_input"
	NoticeUnconfirmed      NoticeKind = "delivery_unconfirmed"
	NoticeStartFailed      NoticeKind = "start_failed"
	// NoticeCreated: a request that waited for a vacancy was made (to who asked).
	NoticeCreated    NoticeKind = "character_created"
	NoticeDismissed  NoticeKind = "dismissed"
	NoticeNotResumed NoticeKind = "not_resumed"
	// NoticeDeadlock: a cycle of waits (docs/orquestracao/esperas-e-caixa-de-entrada.md#detecção-de-impasse).
	NoticeDeadlock NoticeKind = "deadlock"
	// NoticeQueueLong: more than 50 items wait in an inbox.
	NoticeQueueLong NoticeKind = "queue_long"
	// NoticeWaitTimeout: a wait or an ask ran out of time.
	NoticeWaitTimeout NoticeKind = "wait_timeout"
	// NoticeSystem is a system item for the recipient.
	NoticeSystem NoticeKind = "system"
	// NoticeGrandMasterFailing: the Grand Master keeps failing to stay up.
	NoticeGrandMasterFailing NoticeKind = "grand_master_failing"
	// NoticeTableMasterFailing: a table master keeps failing to stay up.
	NoticeTableMasterFailing NoticeKind = "table_master_failing"
)

// Briefer gives the state of a workspace for the resumption message of its
// Grand Master: the tables that are open, its waits, the size of its inbox, the
// approvals that wait and the last message of a user
// (docs/orquestracao/grao-mestre.md#retomada-de-contexto).
type Briefer interface {
	Brief(ctx context.Context, workspace string) (string, error)
}

// TableBriefer gives the state of a table for the resumption message of its
// table master: the missions with their state and current step, the
// characters of the table with their states, its waits and the size of its
// inbox (docs/orquestracao/mestre-da-mesa.md#fixo-enquanto-a-mesa-estiver-aberta).
type TableBriefer interface {
	BriefTable(ctx context.Context, master Character) (string, error)
}

// Notice is a message to someone about a character.
type Notice struct {
	Kind      NoticeKind
	To        Recipient
	Workspace string
	Table     string
	// Character is the id of the character the notice is about, Name its name.
	Character string
	Name      string
	// Creator is who asked for it (the recipient of ToCreator).
	Creator Creator
	// Text is the text, in Portuguese, for the people.
	Text string
	// Screen is the screen that waits for input (NoticeBlockedInput).
	Screen string
}

// Notifier tells people and characters (the inbox and the bot arrive with
// stage 7 and milestone 5).
type Notifier interface {
	Notify(ctx context.Context, n Notice) error
}

// Summary is what is written in the scroll of the quest, or in the history of
// the table, when a character ends.
type Summary struct {
	Character Character
	Dones     []Done
	// Usage is the use of the provider, when known.
	Usage string
}

// Scrolls writes the summaries (docs/orquestracao/pergaminhos-e-tomos.md,
// stage 7).
type Scrolls interface {
	// Record writes the summary in the scroll of the quest of the character
	// or, without a quest, in the history of its table.
	Record(ctx context.Context, s Summary) error
}

// UsageProbe gives the use of a character for its summary (milestone 5).
type UsageProbe interface {
	Usage(ctx context.Context, characterID string) (string, error)
}

// InboxProbe counts what waits in the inbox of a character
// (docs/orquestracao/esperas-e-caixa-de-entrada.md, stage 7).
type InboxProbe interface {
	Pending(ctx context.Context, characterID string) (int, error)
}

// WaitProbe tells whether a character has an active wait: its turn then ends
// in waiting, not idle (stage 7). reason is what the canvas shows ("Revisor",
// "approval #3").
type WaitProbe interface {
	Active(ctx context.Context, characterID string) (reason string, ok bool, err error)
}

// Connector keeps the connections of the characters
// (docs/orquestracao/conexoes.md, internal/orchestration/connections): the
// automatic ones are made when a character is born and go away when it ends.
type Connector interface {
	// Born makes the automatic connections of a character just created. It is
	// safe to call again.
	Born(ctx context.Context, c Character) error
	// Ended removes every connection of a character that ended.
	Ended(ctx context.Context, c Character) error
	// Inherit gives the connections of a character that was replaced to the
	// one that takes its place.
	Inherit(ctx context.Context, from, to Character) error
}

// Placer keeps the place of the characters on the canvas.
type Placer interface {
	// Replaced gives the character that replaces another its position, size
	// and group on the canvas (docs/cli/coordenacao.md, CA-CLI-COORD-04).
	Replaced(ctx context.Context, from, to Character) error
}

// Observer is told by the Service what the Citadel learns of a character, for
// the parts that act on it: the inbox delivers when the character is free, the
// waits are satisfied by a done (docs/orquestracao/esperas-e-caixa-de-entrada.md).
// An error is logged; it never undoes the transition.
type Observer interface {
	// StateChanged is called after every change of state the Service applied.
	StateChanged(ctx context.Context, c Character, from State) error
	// Done is called after the done of a character was registered.
	Done(ctx context.Context, c Character, text string) error
	// TurnEnded is called when a turn of a character ended, with the last
	// message of the agent in it (the answer of an ask that had no done).
	TurnEnded(ctx context.Context, c Character, lastMessage string) error
}

// TurnObserver is how the Runtime of a machine tells the Citadel that a turn
// ended: in process on the Citadel's own machine, over the protocol for a
// Tower (milestone 5).
type TurnObserver interface {
	TurnEnded(ctx context.Context, agentID, lastMessage string) error
}

// Reporter is the way back to the Citadel: the transitions of the states of
// the characters of a machine.
type Reporter interface {
	Report(ctx context.Context, r Report) error
}

// Report is a transition of a character, as its machine saw it. The machine is
// the truth about the process (docs/orquestracao/personagens.md#retomada-após-reinício).
type Report struct {
	Character string
	// Seq grows with every report of a character, so a late one is dropped.
	Seq    uint64
	State  State
	Reason string
	At     time.Time
	// Session is the id of the session of the provider, when it changed.
	Session string
	// Terminal is the terminal that runs the character, PTY its mestre-pty.
	Terminal string
	PTY      PTYInfo
	// Reduced, when set, is the reason of reduced monitoring ("" clears it).
	Reduced *string
	// Resync marks the report a machine makes when it reconnects: the
	// states of its terminals as they are, which the Citadel reconciles
	// (the machine is the truth about the process).
	Resync bool
}

// Preparer is the session zero of the characters of a machine
// (sessionzero.Character).
type Preparer interface {
	Prepare(ctx context.Context, req sessionzero.CharacterRequest) (sessionzero.CharacterPlan, error)
	End(ctx context.Context, req sessionzero.EndRequest) error
	Contexts() hook.Contexts
	TakeUpdate(agentID string) string
}

// TableChecker is Table.CheckStart for the worktree of a table on this
// machine (docs/sessao-zero/mesa.md): it verifies and corrects the files of
// the providers and refuses a provider whose files were not generated.
type TableChecker interface {
	CheckStart(ctx context.Context, workspace, table, dir string, provider providers.ID) error
}

// Retruster writes the trust of a worktree again in the account profile and
// is what providers.TrustRecovery needs (Table.Prepare does it).
type Retruster interface {
	Retrust(ctx context.Context, workspace, table, dir string) error
}

// Observers is several Observers, called in the order they are listed (the
// inbox first, then the waits); every one is called, and the errors are joined.
type Observers []Observer

var _ Observer = Observers(nil)

// StateChanged implements Observer.
func (os Observers) StateChanged(ctx context.Context, c Character, from State) error {
	var errs []error
	for _, o := range os {
		errs = append(errs, o.StateChanged(ctx, c, from))
	}
	return errors.Join(errs...)
}

// Done implements Observer.
func (os Observers) Done(ctx context.Context, c Character, text string) error {
	var errs []error
	for _, o := range os {
		errs = append(errs, o.Done(ctx, c, text))
	}
	return errors.Join(errs...)
}

// TurnEnded implements Observer.
func (os Observers) TurnEnded(ctx context.Context, c Character, lastMessage string) error {
	var errs []error
	for _, o := range os {
		errs = append(errs, o.TurnEnded(ctx, c, lastMessage))
	}
	return errors.Join(errs...)
}

// ScrollCopies writes again the read-only copies of the scrolls of a table in
// the worktrees where there are characters (*scrolls.Service), which a
// character that is born reads at once.
type ScrollCopies interface {
	Sync(ctx context.Context, workspace, table string) error
}

// ClassCheck says whether a class has an approved version for a workspace
// (and a table), so a request that names one that does not exist fails before
// anything is made.
type ClassCheck interface {
	Exists(ctx context.Context, workspace, table, class string) (bool, error)
}
