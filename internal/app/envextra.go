package app

import (
	"context"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// envLookup bounds the read of the record of a worktree: the environment of a
// decision is built on the way of a hook, which does not wait for a database.
const envLookup = 2 * time.Second

// EnvExtraOptions are what only a machine knows of the environment a
// character's decisions run in (classes.Env): the way the adapters normalize
// a path, the edits of the turns in progress, the git state of the worktree
// and the risk policy (docs/classes/permissoes.md#pipeline-de-decisão).
type EnvExtraOptions struct {
	Clock clock.Clock
	// Providers gives Normalize: the one of the adapters (NormalizePath), so a
	// path of a command is judged as the one of a tool is.
	Providers providers.Options
	// Edits is the tracker of the machine, fed by FeedEdits; nil: the rule of
	// the edit in progress does not hold (the degraded hook does not know it
	// either), which a production engine does not want.
	Edits *classes.EditTracker
	// Git opens a worktree by its record (gitx.Real) and Worktrees finds what
	// the engine recorded of the worktree of a table on this machine; without
	// a record the git state is nil and a commit is denied (fail closed).
	// Moved hears a worktree an agent pointed elsewhere.
	Git       gitx.WorktreeOpener
	Worktrees func(ctx context.Context, workspace, table string) (WorktreeInfo, error)
	Moved     func(error)
	// Risk is the owner's part of the risk classification of a workspace (the
	// policy file); nil: the default table.
	Risk func(workspace string) *classes.RiskPolicy
}

// WorktreeInfo is what the engine recorded of the worktree of a table on a
// machine, before any agent ran there: the record that opens it, the commit
// the table branch was based on and the main branch the base is checked
// against ("main", "refs/remotes/origin/main").
type WorktreeInfo struct {
	Record     gitx.WorktreeRecord
	BaseCommit string
	Main       string
}

// NewEnvExtra is the EnvExtra of characters.RuntimeOptions: it completes the
// environment the runtime fills (the folders of the character) with what the
// decision of the permissions needs and the runtime does not know.
func NewEnvExtra(o EnvExtraOptions) func(l characters.Launch, env *classes.Env) {
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	return func(l characters.Launch, env *classes.Env) {
		env.Normalize = o.Providers.NormalizePath
		if o.Edits != nil {
			env.Edits = o.Edits
		}
		if o.Risk != nil {
			env.Risk = o.Risk(l.Workspace)
		}
		if o.Git == nil || o.Worktrees == nil || l.Table == "" {
			return
		}
		ctx, cancel := clock.WithTimeout(context.Background(), o.Clock, envLookup)
		defer cancel()
		wt, err := o.Worktrees(ctx, l.Workspace, l.Table)
		if err != nil {
			return // no record of the worktree: nothing opens it, and a commit is denied
		}
		env.Git = classes.GitxState{Git: o.Git, Worktree: wt.Record, Main: wt.Main, Base: wt.BaseCommit, Moved: o.Moved}
	}
}

// FeedEdits is the OnHookEvent that keeps the tracker: a write that went
// through (post_tool of a tool that writes or edits, with a result that is ok)
// records its paths for the character in its turn, and the end of the turn,
// the failure of the turn, the interruption and the end of the session forget
// them (the turn of the character is over). name is how the character is
// shown in the reason of a denial (its name; the id when it knows nobody).
func FeedEdits(t *classes.EditTracker, name func(agentID string) string) func(providers.Event, hook.Answer) {
	return func(ev providers.Event, _ hook.Answer) {
		if t == nil || ev.AgentID == "" {
			return
		}
		switch ev.Event {
		case providers.EventPostTool:
			if ev.Tool == nil || (ev.Tool.Kind != providers.KindWrite && ev.Tool.Kind != providers.KindEdit) {
				return
			}
			if ev.ToolResult != nil && !ev.ToolResult.OK {
				return // the write did not happen
			}
			who := ev.AgentID
			if name != nil {
				if n := name(ev.AgentID); n != "" {
					who = n
				}
			}
			t.Record(ev.TableID, ev.AgentID, who, ev.Tool.Paths)
		case providers.EventTurnEnd, providers.EventTurnFailure, providers.EventInterrupt, providers.EventSessionEnd:
			t.EndTurn(ev.TableID, ev.AgentID)
		}
	}
}

// UseEdits chains FeedEdits into the events the dispatcher answers.
func (d *Deps) UseEdits(t *classes.EditTracker, name func(agentID string) string) {
	prev, feed := d.OnHookEvent, FeedEdits(t, name)
	d.OnHookEvent = func(ev providers.Event, ans hook.Answer) {
		feed(ev, ans)
		if prev != nil {
			prev(ev, ans)
		}
	}
}
