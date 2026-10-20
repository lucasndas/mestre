package tables

import (
	"context"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

// Git is the git the tables need: gitx.Real, or the fake of
// internal/testkit/fakegit. The worktrees of the tables are opened only by
// their record (OpenWorktree), taken with Record when they are created.
type Git interface {
	gitx.Git
	gitx.WorktreeOpener
	Record(ctx context.Context, dir string) (gitx.WorktreeRecord, error)
}

// Workspaces is what the tables need of the workspaces
// (*workspaces.Service).
type Workspaces interface {
	Get(ctx context.Context, slug string) (workspaces.Workspace, error)
	// CheckOpenTable is the gate of `table open`: only an active workspace.
	CheckOpenTable(ctx context.Context, slug string) error
	// Fetch fetches the mirror from GitHub.
	Fetch(ctx context.Context, slug string) error
	Layout() workspaces.Layout
	OpenMirror(ctx context.Context, slug string) (gitx.Repo, error)
	Pause(ctx context.Context, slug string) error
	Resume(ctx context.Context, slug string) error
}

// Epic is an issue of GitHub as the opening of a table needs it.
type Epic struct {
	Number int
	Title  string
	// Body is the text of the issue; the criteria are read from it (ParseCriteria).
	Body   string
	Labels []string
	Open   bool
	// NotPlanned: the epic was closed as "not planned" (false for an open one).
	NotPlanned bool
}

// HasLabel says whether the epic carries the label.
func (e Epic) HasLabel(label string) bool {
	for _, l := range e.Labels {
		if l == label {
			return true
		}
	}
	return false
}

// EpicLabel is the label of the epics (docs/decisoes/0015-epico-como-rotulo.md).
const EpicLabel = "epico"

// GitHub is GitHub's side of the tables, with the identity of the App
// "Grão-Mestre" (milestone 5; the tests use a fake). Epic returns
// ErrEpicNotFound for an issue that does not exist.
type GitHub interface {
	Epic(ctx context.Context, workspace string, number int) (Epic, error)
	// CloseEpic closes the epic as "not planned".
	CloseEpic(ctx context.Context, workspace string, number int) error
	// ClosePR closes a pull request without merging.
	ClosePR(ctx context.Context, workspace string, number int) error
}

// IssueRef is an issue of an epic.
type IssueRef struct {
	Number int
	Title  string
}

// PullStats is the size of a pull request and what its checks say.
type PullStats struct {
	Files, Lines int
	// CI is success, failure, pending or none.
	CI string
}

// Details is what `table status` reads from the repository host besides the epic
// (the GitHub of a run may implement it; without it those fields say they could
// not be read).
type Details interface {
	// OpenIssues are the open issues under the epic.
	OpenIssues(ctx context.Context, workspace string, epic int) ([]IssueRef, error)
	// PullStats are the files, the lines and the CI of a pull request.
	PullStats(ctx context.Context, workspace string, number int) (PullStats, error)
}

// Message is a message of a user, as the channel kept it.
type Message struct {
	ID string
	// Text is the original text, byte for byte.
	Text        string
	Attachments []sessionzero.File
	Origin      inbox.Origin
}

// Messages gives the messages of the users (milestone 5: the Telegram and
// the panel).
type Messages interface {
	Message(ctx context.Context, workspace, id string) (Message, error)
}

// Attachments keeps the attachments forwarded to a table, which the session
// zero copies into .mestre/anexos/ (sessionzero.AttachmentSource; milestone 5
// owns the attachments).
type Attachments interface {
	Forward(ctx context.Context, workspace, table string, files []sessionzero.File) error
}

// Characters is the Service of the characters.
type Characters interface {
	Create(ctx context.Context, req characters.CreateRequest) (characters.CreateResult, error)
	Dismiss(ctx context.Context, req characters.DismissRequest) (characters.DismissResult, error)
}

// Inbox is the inbox of the characters.
type Inbox interface {
	Enqueue(ctx context.Context, n inbox.NewItem) (inbox.Item, error)
	// PumpAll delivers what waits, after a resume.
	PumpAll(ctx context.Context) error
}

// Files is the session zero of the tables (*sessionzero.Table).
type Files interface {
	Prepare(ctx context.Context, req sessionzero.TableRequest) (sessionzero.TableReport, error)
	Release(req sessionzero.TableRequest) error
	Sync(ctx context.Context, req sessionzero.TableRequest, sync func(context.Context) error) error
}

// Profiler gives the account profiles of a machine that run characters in
// the worktrees (nil: none).
type Profiler interface {
	Profiles(ctx context.Context, machine string) []sessionzero.Profile
}

// Destructive says whether a destructive action may run now: there is an
// exception for exactly that action, or an approval of the type
// `acao_destrutiva` for it (docs/aprovacoes/politica.md, milestone 5). false
// means the flow of approval was started or is under way: the answer to the
// caller is "pending".
type Destructive interface {
	Allowed(ctx context.Context, a DestructiveAction) (bool, error)
}

// DestructiveAction is what is asked.
type DestructiveAction struct {
	Workspace, Table string
	// Action is "table.cancel".
	Action string
	Reason string
	// By is the Grand Master that asked.
	By string
}

// Rituals deactivates the rituals of a table (docs/orquestracao/rituais.md,
// stage 10; nil: there are none).
type Rituals interface {
	Deactivate(ctx context.Context, workspace, table string) error
}

// Requests cancels the open requests of the approval chain of a table that
// was cancelled (*chain.Service; nil: none).
type Requests interface {
	CancelTable(ctx context.Context, workspace, table string) error
}

// Oversight is what the supervision of the approval chain says about a table
// for `mestre table status` (docs/orquestracao/supervisao-da-cadeia.md; nil:
// nothing is said).
type Oversight interface {
	// Status says whether the autonomy of the table is paused (with the
	// reason) and gives the line of its queue of approvals ("" for none).
	Status(ctx context.Context, workspace, table string) (paused bool, reason, queue string, err error)
}

// ProofState is a criterion of a mission and what the engine knows of its
// proof (docs/orquestracao/prova-e-devolucao.md#prova-por-critério).
type ProofState struct {
	Quest     string `json:"quest"`
	Criterion string `json:"criterion"`
	Text      string `json:"text"`
	// Marked: the engine checked the item on the issue.
	Marked bool `json:"marked"`
	// Origin is "verificada pelo motor", "declarada por <personagem>" or "sem
	// prova"; Proof the text of the proof, which is the words of an agent.
	Origin string `json:"origin"`
	Proof  string `json:"proof,omitempty"`
}

// Proofs gives the proofs of the criteria of the missions of a table
// (quests.Store).
type Proofs interface {
	Of(ctx context.Context, workspace, table string) ([]ProofState, error)
}

// Inspection is the state of a worktree on a machine.
type Inspection struct {
	// Tip is the commit at the head of the branch there.
	Tip string
	// Dirty lists the files with changes that are not committed.
	Dirty []string
}

// SyncRequest asks a Tower to bring the worktree of a table to the tip the
// Citadel has, creating it when there is none (the message sync.request).
type SyncRequest struct {
	Workspace, Table, Branch string
	// Base is the last commit of the branch the Citadel knows the Tower has
	// ("": none), Tip the commit the Citadel has.
	Base, Tip string
}

// Peers is the Citadel's way to the Towers: the connection of the protocol
// (milestone 5 wires it; the tests use a fake that moves the bundles with
// the same receiver). A bundle is sent with protocol.Link.SendBundle, which
// goes again with the whole branch when the Tower lacks the prerequisite.
type Peers interface {
	// Connected says whether the Tower has a connection now.
	Connected(machine string) bool
	// SendBundle sends the bundle of the sources to the Tower; the Tower
	// verifies it, keeps the tip of the Citadel in refs/mestre/cidadela/<branch>
	// and brings its worktree to it, answering when it is done.
	SendBundle(ctx context.Context, machine string, meta protocol.Object, src protocol.BundleSource) error
	// WorktreeDir is where the Tower keeps the worktree of the table.
	WorktreeDir(ctx context.Context, machine, workspace, table string) (string, error)
	// Inspect asks the Tower for the state of its worktree of the table.
	Inspect(ctx context.Context, machine, workspace, table string) (Inspection, error)
	// RemoveWorktree asks the Tower to remove its worktree of the table.
	RemoveWorktree(ctx context.Context, machine, workspace, table string) error
}

// Dependencies is the dependency between epics
// (docs/orquestracao/dependencia-entre-mesas.md, *epicdeps.Service; nil: no table
// waits for another).
type Dependencies interface {
	// Opened is told that a table was made, before its first message is
	// queued; an error keeps the table from opening.
	Opened(ctx context.Context, workspace string, t Table) error
	// Held says whether the epic waits for another one.
	Held(ctx context.Context, workspace string, epic int) (bool, error)
	// Waiting gives the epics the epic waits for.
	Waiting(ctx context.Context, workspace string, epic int) ([]int, error)
	// Merged is told that the pull request of the table was merged and its
	// commit is in the base.
	Merged(ctx context.Context, workspace string, t Table) error
	// Dropped is told that a table is gone, because it did not open or was
	// cancelled: the waits made for it end.
	Dropped(ctx context.Context, workspace string, t Table) error
	// Cancelled is told that the table was cancelled.
	Cancelled(ctx context.Context, workspace string, t Table) error
}

// Actor is who asked for a pause or a resume.
type Actor struct {
	// ID is a user, a character, or "usage_limit" / "engine".
	ID string
}
