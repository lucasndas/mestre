package usage

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Directory tells the use who a character is (the registry and the sheets).
type Directory interface {
	// Character finds a character; false when it is not known.
	Character(ctx context.Context, id string) (Subject, bool, error)
	// Alternatives lists the sheets of the same class as the character that
	// use another provider or another account.
	Alternatives(ctx context.Context, s Subject) ([]string, error)
	// PendingSteps counts the steps of the quests of the table that wait to
	// start.
	PendingSteps(ctx context.Context, workspace, table string) (int, error)
}

// Ceilings gives the cost ceiling of a workspace (classes.Library.Ceiling).
type Ceilings interface {
	Ceiling(ctx context.Context, workspace string) (*classes.Ceiling, error)
}

// Pauser pauses and resumes what a limit covers. Pausing never interrupts a
// turn; resuming something that is not paused is not an error.
type Pauser interface {
	PauseQuest(ctx context.Context, quest, reason string) error
	ResumeQuest(ctx context.Context, quest string) error
	PauseTable(ctx context.Context, workspace, table, reason string) error
	ResumeTable(ctx context.Context, workspace, table string) error
	// PauseWorkspace pauses it; it says whether the workspace was running
	// (the total of the day pauses every one that is).
	PauseWorkspace(ctx context.Context, workspace string) (bool, error)
	ResumeWorkspace(ctx context.Context, workspace string) error
	// Workspaces lists the slugs of the workspaces.
	Workspaces(ctx context.Context) ([]string, error)
}

// Characters changes the state of a character the provider cut.
type Characters interface {
	// Block puts the character in blocked with the reason quota.
	Block(ctx context.Context, id string) error
	// Unblock takes it out of blocked and delivers what waited for it.
	Unblock(ctx context.Context, id string) error
}

// Pump delivers what waits in the inbox (the inbox, after a quota is back).
type Pump interface {
	PumpAll(ctx context.Context) error
}

// Alerts tells the users of a workspace (the bot and the panel).
type Alerts interface {
	Notify(ctx context.Context, workspace string, msg i18n.Msg) error
}

// Extras opens the custo_extra request of a limit in the chain of approval
// (docs/aprovacoes/fluxo.md).
type Extras interface {
	OpenExtra(ctx context.Context, r ExtraRequest) (int64, error)
}

// ExtraRequest is a custo_extra request. Risk is the classification by the
// cost ceiling: alto without delegation when the extra passes it, so only an
// Administrator or above decides it.
type ExtraRequest struct {
	Workspace, Table string
	Scope            Scope
	Unit             classes.Unit
	Used, Limit      float64
	Extra            float64
	// About is the line the approver reads, as a message.
	About i18n.Msg
	Risk  classes.RiskResult
	// Target is the exact thing that is asked, as JSON.
	Target json.RawMessage
}

// Decision is what became of a custo_extra request.
type Decision struct {
	Request  int64
	Approved bool
	Reason   string
}

// Checklist opens the alerts of the checklist of the platform
// (docs/plataforma/checklist.md).
type Checklist interface {
	Raise(ctx context.Context, code, subject, detail string) error
}

// Options are the pieces of the Service. Store, Directory and Pauser are
// required; the rest may be nil: nothing is checked against the ceiling, the
// limits do not ask for extra, nobody is told.
type Options struct {
	Store     *Store
	Clock     clock.Clock
	Directory Directory
	Pauser    Pauser
	Ceilings  Ceilings
	// Characters and Pump act on a character that waits for the quota.
	Characters Characters
	Pump       Pump
	Notifier   characters.Notifier
	Alerts     Alerts
	Extras     Extras
	Checklist  Checklist
	// Zone gives the time zone of the Guild (UTC by default): the day turns
	// at its midnight.
	Zone func(ctx context.Context) *time.Location
	// ExtraShare is the extra asked at 100%, as a part of the limit (0.5).
	ExtraShare float64
	Logger     *slog.Logger
}
