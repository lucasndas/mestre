package grimoires

import (
	"context"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/connections"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/orchestration/presets"
	"github.com/mestre-ia/mestre/internal/orchestration/quests"
	"github.com/mestre-ia/mestre/internal/orchestration/scrolls"
)

// Characters is the Service of the characters (*characters.Service).
type Characters interface {
	Create(ctx context.Context, req characters.CreateRequest) (characters.CreateResult, error)
	Pin(ctx context.Context, id string, pinned bool, by characters.Creator) error
	// Limits are the simultaneous characters allowed.
	Limits() characters.Limits
}

// Scrolls is the Service of the scrolls and tomes (*scrolls.Service).
type Scrolls interface {
	Create(ctx context.Context, r scrolls.CreateRequest) (scrolls.Note, error)
	Read(ctx context.Context, r scrolls.ReadRequest) (scrolls.Note, string, error)
	Store() *scrolls.Store
}

// Connections is the Service of the connections (*connections.Service).
type Connections interface {
	Link(ctx context.Context, workspace, table string, a, b connections.Ref, by string) error
	Store() *connections.Store
}

// Quests is the Service of the quests (*quests.Service).
type Quests interface {
	Get(ctx context.Context, workspace, table, nameOrID string) (quests.Quest, error)
	Create(ctx context.Context, r quests.CreateRequest) (quests.View, error)
}

// Tables gives the state of the table (*tables.Service).
type Tables interface {
	Table(ctx context.Context, workspace, id string) (characters.TableInfo, error)
}

// Inbox is the inbox of the characters (*inbox.Service).
type Inbox interface {
	Enqueue(ctx context.Context, n inbox.NewItem) (inbox.Item, error)
}

// Sheets tells whether a sheet exists, and what it is (characters.Presets).
type Sheets interface {
	Snapshot(ctx context.Context, sheet string) (presets.Snapshot, error)
}

// ClassInfo is an approved class as the grimoires need it.
type ClassInfo struct {
	Name  string
	Scope classes.ClassScope
	// App is the App linked to the class, as CLASS.md writes it.
	App string
	// Files is the package (nil for a global class, which is only referred to).
	Files classes.Files
}

// Classes is the classes of the workspaces (docs/classes/classes.md): the
// Library of each one, which LibraryClasses adapts.
type Classes interface {
	// Find returns the approved class visible from the table: global, of the
	// workspace or of the table.
	Find(ctx context.Context, workspace, table, name string) (ClassInfo, bool, error)
	// Excess lists what the package gives beyond the ceiling of the workspace
	// (docs/classes/teto-do-workspace.md); empty means it fits.
	Excess(ctx context.Context, workspace string, files classes.Files) ([]string, error)
	// Install puts the package in as a class of the table, with its App.
	Install(ctx context.Context, workspace, table string, c ClassInfo, by Actor) error
}

// Access says whether a user may use a workspace (milestone 5 brings the
// users and their workspaces). Nil: every Administrator and above may.
type Access interface {
	CanUse(ctx context.Context, user Actor, workspace string) bool
}
