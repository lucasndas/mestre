package quests

import (
	"context"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
)

// Characters is the Service of the characters (*characters.Service): the
// quests make the characters of their steps, mark the task and register the
// done.
type Characters interface {
	Create(ctx context.Context, req characters.CreateRequest) (characters.CreateResult, error)
	Get(ctx context.Context, id string) (characters.Character, error)
	TaskAssigned(ctx context.Context, id string) error
	Done(ctx context.Context, id, text string) error
	// EndQuest has the ephemeral characters of the quest ended, each at its
	// safe point.
	EndQuest(ctx context.Context, quest string) error
}

// Inbox is the inbox of the characters (*inbox.Service).
type Inbox interface {
	Enqueue(ctx context.Context, n inbox.NewItem) (inbox.Item, error)
}

// Scrolls is the scroll of the quest (*scrolls.Service): made when the quest
// is, and only added to by the engine.
type Scrolls interface {
	Ensure(ctx context.Context, workspace, table, name, content string) (scrolls.Note, error)
	AppendSection(ctx context.Context, workspace, table, name, section string) (scrolls.Note, error)
}

// Connections are the connections of the quest (*connections.Service).
type Connections interface {
	LinkQuest(ctx context.Context, quest, workspace, table string, pairs ...[2]connections.Ref) error
	EndQuest(ctx context.Context, quest string) error
}

// Tables is what the quests need of the tables (*tables.Service).
type Tables interface {
	Table(ctx context.Context, workspace, id string) (characters.TableInfo, error)
	Paused(ctx context.Context, workspace, table string) (bool, error)
	// MissionStarted takes a table in review back to work.
	MissionStarted(ctx context.Context, workspace, id string) (tables.Table, error)
	// SyncTower brings the worktree of the table on a Tower to the tip of the
	// Citadel, before a step that runs there.
	SyncTower(ctx context.Context, workspace, table, machine string) error
	// Changes reads what the branch of the table did since a commit.
	Changes(ctx context.Context, workspace, table, machine, since string) (tables.Changes, error)
}

// Waits is what the quests tell the waits (*waits.Service).
type Waits interface {
	// StepDone satisfies the waits for a step that ended.
	StepDone(ctx context.Context, stepID, result string)
	// StepAssigned tells a step got its character: the waits for the step now
	// point to it (the deadlock detector looks again).
	StepAssigned(ctx context.Context, stepID string)
	// CancelQuest cancels the waits of a quest that was cancelled.
	CancelQuest(ctx context.Context, quest string) error
	// WaitApproval makes the character wait for a request of approval.
	WaitApproval(ctx context.Context, waiter characters.Character, request int64, timeout time.Duration) (waits.Wait, error)
	// ApprovalDecided satisfies the wait for a request that was decided.
	ApprovalDecided(ctx context.Context, request int64, text string)
}

// Towers reaches the half of the tables that runs on a Tower: after the done
// of a character there, its commits go to the Citadel (tables.Tower.Done). Nil
// means every character runs on the machine of the Citadel. Milestone 5 wires
// the connection; the tests use the Tower itself.
type Towers interface {
	Done(ctx context.Context, machine, workspace, table string) (tables.DoneReport, error)
}

// CloseReason says how an issue is closed on GitHub.
type CloseReason string

// The reasons.
const (
	ClosedCompleted  CloseReason = "completed"
	ClosedNotPlanned CloseReason = "not_planned"
)

// GitHub is GitHub's side of the quests (milestone 5 brings the real one; the
// tests use a fake). The issue of the quest and its sub-issues are made with
// the App "Mestre"; the summary of a step is commented with the App of the
// class of the character
// (docs/orquestracao/missoes.md#espelho-no-github).
type GitHub interface {
	// CreateIssue makes the issue of the quest under the epic.
	CreateIssue(ctx context.Context, workspace string, epic int, title, body string) (int, error)
	// CreateSubIssue makes the sub-issue of a step under the issue.
	CreateSubIssue(ctx context.Context, workspace string, parent int, title, body string) (int, error)
	// UpdateBody writes the body of the issue again (the checklist).
	UpdateBody(ctx context.Context, workspace string, number int, body string) error
	// Comment writes a comment as the App of the class ("": the App "Mestre").
	Comment(ctx context.Context, workspace string, number int, class, body string) error
	// Close closes an issue.
	Close(ctx context.Context, workspace string, number int, reason CloseReason) error
}

// MissionPoint is the request of an approval point of a quest: the type
// `ponto_de_missao`, risk `medio`
// (docs/orquestracao/missoes.md#pontos-de-aprovação).
type MissionPoint struct {
	Workspace, Table string
	Quest, QuestName string
	// Step is the uid of the step, StepID and StepName what the definition calls it.
	Step, StepID, StepName string
	// When is ApprovalBefore or ApprovalAfter.
	When string
	// Character is who asks: the character of the step, whose superior is
	// the first level of the chain ("" for an approval before, when the step
	// has no character yet).
	Character string
	// Designer is the character that made the quest ("" for a user): the
	// chain never gives it the decision.
	Designer string
	// Summary is what the approver sees: for an approval after, the summary
	// of the done.
	Summary string
}

// Approvals opens the request of approval of a point of a quest
// (docs/aprovacoes/fluxo.md, milestone 5). The flow answers with
// Service.Decided; it never gives the decision to the designer of the quest.
type Approvals interface {
	OpenMissionPoint(ctx context.Context, p MissionPoint) (request int64, err error)
}

// SheetInfo is a sheet as the validation needs it.
type SheetInfo struct{ Name, Class string }

// Catalog is what the validation of a definition asks about the sheets and
// the classes.
type Catalog interface {
	Sheet(ctx context.Context, name string) (SheetInfo, bool, error)
	// ClassAllowed says whether the class fits in the ceiling of the workspace
	// (docs/classes/teto-do-workspace.md); a nil error is yes.
	ClassAllowed(ctx context.Context, workspace, class string) error
}
