package panel

import "time"

// The canvas of a table (docs/painel/canvas.md). The topic is
// "canvas:<workspace>/<table>": its snapshot is a CanvasState and EventChanged
// says the state changed, so the browser asks for it again. tygo turns these
// types into web/src/gen/panel.ts.

// EventChanged is the event of a canvas topic: something of the table changed.
const EventChanged = "changed"

// CanvasState is GET /api/workspaces/{ws}/tables/{table}/canvas.
type CanvasState struct {
	Workspace string `json:"workspace"`
	Table     string `json:"table"`
	// TableState is the state of the table (open, working, in_review…).
	TableState  string             `json:"table_state"`
	Characters  []CanvasCharacter  `json:"characters"`
	Scrolls     []CanvasScroll     `json:"scrolls"`
	Tomes       []CanvasTome       `json:"tomes"`
	Connections []CanvasConnection `json:"connections"`
	Quests      []CanvasQuest      `json:"quests"`
	Waits       []CanvasWait       `json:"waits"`
	// Deadlocks are the cycles of waits, written "A → B → A".
	Deadlocks []CanvasDeadlock `json:"deadlocks"`
	// Layout is where each element stands and the free groups.
	Layout []CanvasItem `json:"layout"`
	// CanEdit says the user may act on the canvas (the server checks again).
	CanEdit bool `json:"can_edit"`
}

// CanvasItem is the place of an element: its key is "<kind>:<id>" (a group is
// "group:<id>" with its Label). Sizes of 0 are the default size of the kind.
// Parent is the key of the group it sits in; X and Y are then relative to it.
type CanvasItem struct {
	Key    string  `json:"key"`
	Label  string  `json:"label,omitempty"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	W      float64 `json:"w,omitempty"`
	H      float64 `json:"h,omitempty"`
	Parent string  `json:"parent,omitempty"`
}

// CanvasCharacter is a character (or a simple terminal, with no class) of the table.
type CanvasCharacter struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind is "character" or "terminal"; Role is the role in the table (character, table_master…).
	Kind  string `json:"kind"`
	Role  string `json:"role"`
	Class string `json:"class,omitempty"`
	Sheet string `json:"sheet,omitempty"`
	Base  string `json:"base,omitempty"`
	// Machine is the id of the machine and MachineName the name its badge shows.
	Machine     string `json:"machine"`
	MachineName string `json:"machine_name"`
	State       string `json:"state"`
	Reason      string `json:"reason,omitempty"`
	Pinned      bool   `json:"pinned"`
	// Reduced is why its monitoring is reduced ("" when it is not).
	Reduced string `json:"reduced,omitempty"`
	// Operator says it is the character of an Operator do PC session, and OperatorUntil when it ends.
	Operator      bool       `json:"operator,omitempty"`
	OperatorUntil *time.Time `json:"operator_until,omitempty"`
	// HasTerminal says there is a terminal to show.
	HasTerminal bool `json:"has_terminal"`
}

// CanvasScroll is a scroll with its content; Revision grows with each change.
type CanvasScroll struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Tome     string `json:"tome,omitempty"`
	Content  string `json:"content"`
	Revision int64  `json:"revision"`
	Author   string `json:"author,omitempty"`
}

// CanvasTome is a group of scrolls.
type CanvasTome struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Scrolls int    `json:"scrolls"`
}

// CanvasRef is the end of a connection: Kind is character, scroll or tome.
type CanvasRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// CanvasConnection is a line between two ends, with the reasons that keep it.
type CanvasConnection struct {
	A       CanvasRef `json:"a"`
	B       CanvasRef `json:"b"`
	Reasons []string  `json:"reasons"`
	// Manual says a user can remove it (it has the reason "manual").
	Manual bool `json:"manual"`
}

// CanvasQuest is a mission of the table with its steps, in order.
type CanvasQuest struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
	Round int    `json:"round"`
	// Editable says the mission is a draft the canvas can still change.
	Editable bool         `json:"editable"`
	Steps    []CanvasStep `json:"steps"`
}

// CanvasStep is a step of a mission. Character is the id of who does it ("" while
// no character has it); Name is the name the definition gives to who does it.
type CanvasStep struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	State     string   `json:"state"`
	Character string   `json:"character,omitempty"`
	Assignee  string   `json:"assignee,omitempty"`
	After     []string `json:"after,omitempty"`
	// ReturnTo is the step it may hand the work back to.
	ReturnTo string `json:"return_to,omitempty"`
	Returns  int    `json:"returns"`
	// Current says it is the step in progress.
	Current bool `json:"current"`
	// Waiting is what holds the step before it starts, in words.
	Waiting string `json:"waiting,omitempty"`
}

// CanvasWait is a dashed line from who waits to what it waits for. Target is the
// id of a character of the table, or "" when it waits for something else
// (a time, an approval): the Reason says what.
type CanvasWait struct {
	ID     string `json:"id"`
	Waiter string `json:"waiter"`
	Target string `json:"target,omitempty"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
	// Deadlock says the wait is part of a cycle.
	Deadlock bool `json:"deadlock"`
}

// CanvasDeadlock is a cycle of waits.
type CanvasDeadlock struct {
	Text string `json:"text"`
}

// LayoutRequest is PUT /api/workspaces/{ws}/tables/{table}/canvas/layout: the
// elements that moved, were resized or grouped, and the keys to forget.
type LayoutRequest struct {
	Items  []CanvasItem `json:"items"`
	Remove []string     `json:"remove,omitempty"`
}

// ConnectRequest is POST and DELETE .../canvas/connections.
type ConnectRequest struct {
	A CanvasRef `json:"a"`
	B CanvasRef `json:"b"`
}

// PinRequest is PUT .../canvas/characters/{id}/pin.
type PinRequest struct {
	Pinned bool `json:"pinned"`
}

// ScrollWriteRequest is PUT .../canvas/scrolls/{id}: the new content, written over
// the revision Base; a scroll that is no longer at Base is a conflict.
type ScrollWriteRequest struct {
	Content string `json:"content"`
	Base    int64  `json:"base"`
}

// QuestRequest is POST .../canvas/quests (a new mission, as a draft) and PUT
// .../canvas/quests/{id} (the steps of a draft, replaced).
type QuestRequest struct {
	Name  string           `json:"name"`
	Steps []QuestStepDrawn `json:"steps"`
	Run   bool             `json:"run,omitempty"`
}

// QuestStepDrawn is a step drawn on the canvas: the character that does it (its
// name) and the ids of the steps it comes after.
type QuestStepDrawn struct {
	ID           string   `json:"id"`
	Name         string   `json:"name,omitempty"`
	Character    string   `json:"character"`
	After        []string `json:"after,omitempty"`
	Instructions string   `json:"instructions,omitempty"`
	CanReturnTo  string   `json:"can_return_to,omitempty"`
}

// GrimoireSaveRequest is POST .../canvas/grimoires: the selection saved as a team.
type GrimoireSaveRequest struct {
	Name          string       `json:"name"`
	Selection     []CanvasItem `json:"selection"`
	Quests        []string     `json:"quests,omitempty"`
	StructureOnly bool         `json:"structure_only,omitempty"`
	Replace       bool         `json:"replace,omitempty"`
}

// GrimoireInfo is a saved grimoire as the list shows it.
type GrimoireInfo struct {
	Name        string    `json:"name"`
	Author      string    `json:"author"`
	Workspace   string    `json:"workspace"`
	Table       string    `json:"table"`
	SavedAt     time.Time `json:"saved_at"`
	Characters  int       `json:"characters"`
	Scrolls     int       `json:"scrolls"`
	Tomes       int       `json:"tomes"`
	Connections int       `json:"connections"`
	Quests      int       `json:"quests"`
}

// GrimoireRestoreRequest is POST .../canvas/grimoires/restore: Confirm false only
// plans, and the answer lists what would be made and renamed.
type GrimoireRestoreRequest struct {
	Name    string            `json:"name"`
	Confirm bool              `json:"confirm"`
	Choices map[string]string `json:"choices,omitempty"`
}

// GrimoirePlan is what restoring a grimoire makes (and, when Done, what it made).
type GrimoirePlan struct {
	Name    string   `json:"name"`
	Renamed []string `json:"renamed"`
	// Choose are the classes the destination does not have: the user decides
	// for each (copy, map or cancel).
	Choose []string `json:"choose"`
	// Failed is what could not be made, with the reason.
	Failed []string `json:"failed,omitempty"`
	Done   bool     `json:"done"`
}

// RitualInfo is a ritual of the table.
type RitualInfo struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Target   string     `json:"target"`
	Action   string     `json:"action"`
	Content  string     `json:"content"`
	Schedule string     `json:"schedule"`
	Enabled  bool       `json:"enabled"`
	NextRun  *time.Time `json:"next_run,omitempty"`
}

// RitualEditRequest is PUT .../canvas/rituals/{id}: nil keeps the field.
type RitualEditRequest struct {
	Enabled *bool   `json:"enabled,omitempty"`
	Content *string `json:"content,omitempty"`
}
