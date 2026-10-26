package workspaces

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/gitx"
)

// Git is the git the workspaces need: gitx.Real, or the fake of
// internal/testkit/fakegit. The main clone is opened only by its record
// (OpenWorktree), taken with Record when the clone is created.
type Git interface {
	gitx.Git
	gitx.WorktreeOpener
	Record(ctx context.Context, dir string) (gitx.WorktreeRecord, error)
}

// RepoInfo is what GitHub says about a repository.
type RepoInfo struct {
	// FullName is the name as GitHub spells it ("lucas/Meu_App").
	FullName string
	// CloneURL is the https URL, without credentials.
	CloneURL string
	// DefaultBranch is the main branch, read when the workspace is created.
	DefaultBranch string
}

// GitHub looks a repository up with the identity of the Grand Master App
// (docs/github/identidades-e-apps.md).
type GitHub interface {
	Repository(ctx context.Context, repo Repo) (RepoInfo, error)
}

// Credentials gives the token of the installation of the App "Grão-Mestre"
// for the repository, as the Auth git takes in its environment. A nil Auth
// fetches without credentials (a local repository).
type Credentials interface {
	Auth(ctx context.Context, repo Repo) (*gitx.Auth, error)
}

// AlertKind says what an operational alert is about.
type AlertKind string

// The operational alerts of the workspaces.
const (
	// AlertFetchFailing: the mirror could not be fetched for AlertAfter.
	AlertFetchFailing AlertKind = "fetch_failing"
	// AlertWorktreeMoved: the main clone no longer points at its record
	// (an agent rewrote its .git file or commondir).
	AlertWorktreeMoved AlertKind = "worktree_moved"
	// AlertRefMoved: a ref of the mirror (the branch of a table, or the tip a
	// Tower sent) moved without passing through the engine.
	AlertRefMoved AlertKind = "ref_moved"
)

// Alert is an operational alert for the users of the workspace (Telegram and
// panel).
type Alert struct {
	Workspace string
	Kind      AlertKind
	// Detail is the text for the users, with the reason.
	Detail string
	// Since is when the problem began.
	Since time.Time
}

// Alerts sends operational alerts (docs/telegram/bot.md). An alert that
// returns an error is sent again later.
type Alerts interface {
	Operational(ctx context.Context, a Alert) error
}

// GrandMasterState is the state of the character of the Grand Master
// (docs/orquestracao/personagens.md#estados) as far as the main clone cares.
type GrandMasterState string

// The states the main clone cares about; any other value counts as working.
const (
	// GMIdle and GMWaiting leave the main clone still.
	GMIdle    GrandMasterState = "idle"
	GMWaiting GrandMasterState = "waiting"
	// GMBusy is a turn under way.
	GMBusy GrandMasterState = "busy"
	// GMStopped and GMNone (no Grand Master yet) mean nothing runs in the
	// main clone.
	GMStopped GrandMasterState = "stopped"
	GMNone    GrandMasterState = ""
)

// still says whether nothing of the Grand Master can be writing in the main
// clone.
func (s GrandMasterState) still() bool {
	switch s {
	case GMIdle, GMWaiting, GMStopped, GMNone:
		return true
	}
	return false
}

// GrandMaster is what the workspaces need of the Grand Master of a
// workspace (the character is milestone 5).
type GrandMaster interface {
	// State is the state of the character; GMNone if there is none.
	State(ctx context.Context, workspace string) (GrandMasterState, error)
	// NotifySystem puts an item of type system, with the text, in the inbox
	// of the Grand Master.
	NotifySystem(ctx context.Context, workspace, text string) error
}

// Layout is where the workspaces live on this machine: <Root>/<slug>/ holds
// espelho.git, principal/ and mesas/ (docs/orquestracao/workspaces.md).
type Layout struct{ Root string }

// Dir is the folder of the workspace.
func (l Layout) Dir(slug string) string { return filepath.Join(l.Root, slug) }

// Mirror is the bare mirror.
func (l Layout) Mirror(slug string) string { return filepath.Join(l.Dir(slug), "espelho.git") }

// Principal is the main clone.
func (l Layout) Principal(slug string) string { return filepath.Join(l.Dir(slug), "principal") }

// Tables is the folder of the worktrees of the tables.
func (l Layout) Tables(slug string) string { return filepath.Join(l.Dir(slug), "mesas") }

// Verifications is the folder of the ephemeral worktrees of the verification,
// next to mesas/ and not in it: they are not tables.
func (l Layout) Verifications(slug string) string { return filepath.Join(l.Dir(slug), "verificacoes") }

// principalName is the name git gives the administrative folder of the main
// clone: the last element of its path.
const principalName = "principal"

// record is the engine's record of the main clone, from the layout: the
// folder, its git folder in the mirror and the mirror. The layout is the
// engine's own (agents never choose it), and CreateMirror checks it against
// what git says before any agent runs in the clone.
func (l Layout) record(slug string) gitx.WorktreeRecord {
	mirror := l.Mirror(slug)
	return gitx.WorktreeRecord{Dir: l.Principal(slug), GitDir: filepath.Join(mirror, "worktrees", principalName), Common: mirror}
}

// SharedFile says which paths of the folder of the workspaces travel to the
// Towers (replication.FileRoot.Allow): the `.mestre/` of the main clone of
// each workspace, with what is only local in it. Nothing else of the folder
// does: the mirror and the worktrees of the tables have their own paths
// (bundles), and the rest of the clones is the project's.
func SharedFile(rel string, dir bool) bool {
	parts := strings.Split(rel, "/")
	n := len(parts)
	switch {
	case n == 0 || parts[0] == "" || parts[0] == "." || parts[0] == "..":
		return false
	case dir && n == 1:
		return true // the folder of a workspace
	case dir && n == 2:
		return parts[1] == principalName
	case n >= 3:
		if parts[1] != principalName || parts[2] != ".mestre" || (!dir && n < 4) {
			return false
		}
		// the execution folders (agents/, run/) are local and rebuilt at each
		// start: they have no value on a Tower and an agent writes to them
		return n < 4 || (parts[3] != "agents" && parts[3] != "run")
	}
	return false
}
