package panel

import "time"

// The workspace view (docs/painel/visao-do-workspace.md): GET
// /api/workspaces/{slug}/view. The screen follows the "workspaces:all" topic; each
// "changed" makes it read the view again.

// The kinds of wait a table block shows.
const (
	// WaitPaused: the table is paused; Reason says by whom or why.
	WaitPaused = "paused"
	// WaitQuota: the table waits for the quota of a provider (or the limit of use).
	WaitQuota = "quota"
	// WaitTable: the table waits for the merge of another table.
	WaitTable = "table"
	// WaitApproval: the current step waits for an approval.
	WaitApproval = "approval"
	// WaitSameFailure: the mission paused because the same failure came back.
	WaitSameFailure = "same_failure"
	// WaitOther: any other thing a step waits for.
	WaitOther = "other"
)

// ViewQuest is the mission of the table that is in progress.
type ViewQuest struct {
	ID string `json:"id"`
	// Name is the name of the mission; Issue is its issue on GitHub (0 until made).
	Name  string `json:"name"`
	Issue int    `json:"issue"`
	State string `json:"state"`
	// Step is the title of the step in progress ("" when none is).
	Step string `json:"step"`
}

// ViewMachine is the badge of a machine in the party of a table.
type ViewMachine struct {
	Machine     string `json:"machine"`
	MachineName string `json:"machine_name"`
	Characters  int    `json:"characters"`
}

// ViewWait is something a table waits for. Fingerprint and Where are set for the
// same failure again ("arquivo:linha" when the return brought `--where`).
type ViewWait struct {
	Kind        string `json:"kind"`
	Reason      string `json:"reason"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Where       string `json:"where,omitempty"`
}

// ViewCriteria is the proof of the criteria of the current mission: Total criteria,
// Proven with a proof, Verified among those verified by the engine.
type ViewCriteria struct {
	Total    int `json:"total"`
	Proven   int `json:"proven"`
	Verified int `json:"verified"`
}

// ViewUsage is the use of a table against its limit.
type ViewUsage struct {
	Used  float64 `json:"used"`
	Limit float64 `json:"limit"`
	// Unit is usd, tokens or hours.
	Unit    string  `json:"unit"`
	Percent float64 `json:"percent"`
	// HasData says the use has data in the unit of the limit (false: the gap is shown).
	HasData bool `json:"has_data"`
}

// TableBlock is a table of the workspace as its block shows it.
type TableBlock struct {
	ID        string `json:"id"`
	Epic      int    `json:"epic"`
	EpicTitle string `json:"epic_title"`
	Branch    string `json:"branch"`
	State     string `json:"state"`
	Paused    bool   `json:"paused"`
	// IssueWarning is why the table has no issue on GitHub ("" when it has).
	IssueWarning string        `json:"issue_warning,omitempty"`
	Quest        *ViewQuest    `json:"quest,omitempty"`
	Party        []ViewMachine `json:"party"`
	PartySize    int           `json:"party_size"`
	Waits        []ViewWait    `json:"waits"`
	Criteria     *ViewCriteria `json:"criteria,omitempty"`
	// LastDelivery is the last useful delivery (nil when there was none).
	LastDelivery *time.Time `json:"last_delivery,omitempty"`
	// Stalled says the table is at work and delivered nothing for longer than the
	// limit of the warning; QuietSeconds is for how long.
	Stalled      bool       `json:"stalled"`
	QuietSeconds int64      `json:"quiet_seconds"`
	PR           int        `json:"pr"`
	Usage        *ViewUsage `json:"usage,omitempty"`
}

// ViewGrandMaster is the Grand Master of the workspace, the center of the view.
type ViewGrandMaster struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	State       string `json:"state"`
	Machine     string `json:"machine"`
	MachineName string `json:"machine_name"`
	HasTerminal bool   `json:"has_terminal"`
}

// ViewData is what the engine gives of an active workspace.
type ViewData struct {
	GrandMaster *ViewGrandMaster `json:"grand_master,omitempty"`
	Tables      []TableBlock     `json:"tables"`
	// Layout is where the Grand Master ("gm") and each table ("table:<id>") stand;
	// the ones with no place are not in it.
	Layout []CanvasItem `json:"layout"`
}

// WorkspaceView is GET /api/workspaces/{slug}/view.
type WorkspaceView struct {
	// Workspace carries the state and the steps of the session zero (a preparing
	// workspace shows them and no table).
	Workspace   WorkspaceRow     `json:"workspace"`
	GrandMaster *ViewGrandMaster `json:"grand_master,omitempty"`
	Tables      []TableBlock     `json:"tables"`
	Layout      []CanvasItem     `json:"layout"`
	// CanPause says the user may pause a table from its block (the server checks again).
	CanPause bool `json:"can_pause"`
	// CanType says the user may type in the terminal of the Grand Master.
	CanType bool `json:"can_type"`
}

// ViewLayoutRequest is PUT /api/workspaces/{slug}/view/layout.
type ViewLayoutRequest struct {
	Items []CanvasItem `json:"items"`
}

// PauseTableRequest is the body of POST /api/workspaces/{slug}/tables/{table}/pause: the
// Reason is required (a sentence, at most MaxPauseReason characters), and goes to the audit
// and to the wait the block shows.
type PauseTableRequest struct {
	Reason string `json:"reason"`
}

// MaxPauseReason is the most characters of the reason to pause a table.
const MaxPauseReason = 300

// PauseTableResponse is POST /api/workspaces/{slug}/tables/{table}/pause: Changed
// is false when the table was paused already.
type PauseTableResponse struct {
	Changed bool `json:"changed"`
}
