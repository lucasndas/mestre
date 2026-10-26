package panel

import (
	"net/http"
	"time"
)

// The payloads of the screens of the classes, the GitHub Apps, the bases and
// sheets, the account profiles and the usage (docs/painel/administracao.md).
// tygo turns them into web/src/gen/panel.ts; the tags are the contract.

// The codes of the errors of these screens.
const (
	CodeProposalStale  Code = "proposal_stale"
	CodeProposalBad    Code = "proposal_invalid"
	CodePresetInvalid  Code = "preset_invalid"
	CodePresetInUse    Code = "preset_in_use"
	CodeProfileInvalid Code = "profile_invalid"
	CodeAppState       Code = "app_state"
	CodeAppLimit       Code = "app_limit"
)

func init() {
	for c, st := range map[Code]int{
		CodeProposalStale:  http.StatusConflict,
		CodeProposalBad:    http.StatusUnprocessableEntity,
		CodePresetInvalid:  http.StatusUnprocessableEntity,
		CodePresetInUse:    http.StatusConflict,
		CodeProfileInvalid: http.StatusUnprocessableEntity,
		CodeAppState:       http.StatusConflict,
		CodeAppLimit:       http.StatusConflict,
	} {
		status[c] = st
	}
}

// AppCount is the "N de 100 Apps na conta" of the Apps screen and the band of the
// classes screen: the Apps of the Mestre, counted in the database, plus the others
// the Owner informed.
type AppCount struct {
	Mine   int `json:"mine"`
	Others int `json:"others"`
	Total  int `json:"total"`
	// Limit is the 100 of GitHub and Warn the count from which the band shows.
	Limit int  `json:"limit"`
	Warn  int  `json:"warn"`
	Near  bool `json:"near"`
}

// ClassRow is a class of a scope. State is the one of the library (approved,
// pending, new, missing, unreadable); AppState the label of the App of the class
// ("em criação", "pronto", "com erro", or "" when the class has none).
type ClassRow struct {
	Name      string   `json:"name"`
	Scope     string   `json:"scope"`
	Workspace string   `json:"workspace"`
	Table     string   `json:"table"`
	State     string   `json:"state"`
	Version   int64    `json:"version"`
	App       string   `json:"app"`
	AppState  string   `json:"app_state"`
	Problems  []string `json:"problems"`
}

// ProposalRow is a change of a class that waits for an Administrator or above:
// Origin "request" came through the Mestre (the Grand Master proposed it) and
// "disk" is a change made outside (manual edit, git pull). Tested is always false
// until the test of classes exists (M7): the screen marks "não testada".
type ProposalRow struct {
	ID        int64     `json:"id"`
	Workspace string    `json:"workspace"`
	Name      string    `json:"name"`
	Scope     string    `json:"scope"`
	Table     string    `json:"table"`
	Origin    string    `json:"origin"`
	By        string    `json:"by"`
	Hash      string    `json:"hash"`
	Problems  []string  `json:"problems"`
	Tested    bool      `json:"tested"`
	CreatedAt time.Time `json:"created_at"`
}

// ClassesPage is the classes screen.
type ClassesPage struct {
	Apps      AppCount      `json:"apps"`
	Classes   []ClassRow    `json:"classes"`
	Proposals []ProposalRow `json:"proposals"`
}

// ApproveProposalRequest is the "Aprovar" of a proposal: the hash is the one the
// approver saw, so a package that changed meanwhile is not approved.
type ApproveProposalRequest struct {
	Workspace string `json:"workspace"`
	Hash      string `json:"hash"`
}

// AppRow is a GitHub App of the Mestre. State is the label of the state; Repos the
// repositories it is installed on (the workspaces).
type AppRow struct {
	Name       string   `json:"name"`
	Slug       string   `json:"slug"`
	Role       string   `json:"role"`
	Class      string   `json:"class"`
	State      string   `json:"state"`
	StateLabel string   `json:"state_label"`
	Repos      []string `json:"repos"`
	// Request is the status of the open request of creation, "" when none.
	Request string `json:"request"`
	// CanLink says the Owner may ask for a link of creation now (the App waits for
	// its creation or its link expired, and the creation is available).
	CanLink bool `json:"can_link"`
}

// AppRequestRow is a request of creation that is open or expired, for the Owner.
// The link itself is shown once, when it is made: only its hash is kept.
type AppRequestRow struct {
	App       string    `json:"app"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
	Expired   bool      `json:"expired"`
}

// AppsPage is the Apps screen.
type AppsPage struct {
	Count    AppCount        `json:"count"`
	Apps     []AppRow        `json:"apps"`
	Requests []AppRequestRow `json:"requests"`
	// Creation says the link of creation is available in this period.
	Creation bool `json:"creation"`
}

// OthersRequest is the number of Apps of the account that are not the Mestre's.
type OthersRequest struct {
	Count int `json:"count"`
}

// AppLinkResponse is the link of creation of an App, shown once.
type AppLinkResponse struct {
	App       string    `json:"app"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// BaseRow is a base of terminals; YAML is the base alone in the format of the export.
type BaseRow struct {
	Name      string   `json:"name"`
	Command   string   `json:"command"`
	Ready     string   `json:"ready"`
	Platforms []string `json:"platforms"`
	Builtin   bool     `json:"builtin"`
	YAML      string   `json:"yaml"`
}

// SheetRow is a character sheet. Incomplete says what is missing (the sheet of the
// Operator of the PC without its class), "" when nothing is.
type SheetRow struct {
	Name       string `json:"name"`
	Base       string `json:"base"`
	Agent      string `json:"agent"`
	Class      string `json:"class"`
	Account    string `json:"account"`
	Lifecycle  string `json:"lifecycle"`
	Incomplete string `json:"incomplete"`
	YAML       string `json:"yaml"`
}

// PresetsPage is the screen of the bases and sheets.
type PresetsPage struct {
	Bases  []BaseRow  `json:"bases"`
	Sheets []SheetRow `json:"sheets"`
}

// PresetSave is a base or a sheet written as YAML (the same fields as the export).
type PresetSave struct {
	YAML string `json:"yaml"`
}

// PresetSaved is what a save answers: the warnings are what is kept but has no
// effect.
type PresetSaved struct {
	Name     string   `json:"name"`
	Warnings []string `json:"warnings"`
}

// ImportItem names a base or a sheet of a file.
type ImportItem struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// ImportChoice is the answer for a name that already exists: "replace", "rename"
// (with NewName) or "skip".
type ImportChoice struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Action  string `json:"action"`
	NewName string `json:"new_name"`
}

// ImportFile is a file to import: the text, YAML or JSON, and the choices for the
// conflicts (only on the import itself).
type ImportFile struct {
	Content string         `json:"content"`
	Choices []ImportChoice `json:"choices"`
}

// ImportPlan is the file checked as a whole, before anything is written.
type ImportPlan struct {
	Bases     int          `json:"bases"`
	Sheets    int          `json:"sheets"`
	Conflicts []ImportItem `json:"conflicts"`
	Warnings  []string     `json:"warnings"`
}

// ImportResult is what an import did, in numbers of bases and sheets.
type ImportResult struct {
	Added    int `json:"added"`
	Replaced int `json:"replaced"`
	Renamed  int `json:"renamed"`
	Skipped  int `json:"skipped"`
}

// ProfileRow is an account profile of a machine: Step is where the guided setup is
// (install, login, hooks, conformance, done) and Detail what it waits for.
type ProfileRow struct {
	Name        string     `json:"name"`
	Provider    string     `json:"provider"`
	Machine     string     `json:"machine"`
	Dir         string     `json:"dir"`
	Default     bool       `json:"default"`
	Step        string     `json:"step"`
	Detail      string     `json:"detail"`
	CreatedAt   time.Time  `json:"created_at"`
	SetupDoneAt *time.Time `json:"setup_done_at"`
}

// ProfilesPage is the profiles screen. Setup says the guided setup is available.
type ProfilesPage struct {
	Machine   string       `json:"machine"`
	Providers []string     `json:"providers"`
	Profiles  []ProfileRow `json:"profiles"`
	Setup     bool         `json:"setup"`
}

// ProfileAdd creates a profile.
type ProfileAdd struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

// ProfileSetupStatus is where the guided setup stands after a step.
type ProfileSetupStatus struct {
	Step       string `json:"step"`
	Done       bool   `json:"done"`
	Waiting    string `json:"waiting"`
	Terminal   string `json:"terminal"`
	Conformant bool   `json:"conformant"`
	Reason     string `json:"reason"`
}

// UsageTotals is the sum of some rows of use. Cost and Tokens are null when no row
// had the datum: the screen shows the gap, not a zero.
type UsageTotals struct {
	Key           string   `json:"key"`
	Label         string   `json:"label"`
	Workspace     string   `json:"workspace"`
	CostUSD       *float64 `json:"cost_usd"`
	Tokens        *int64   `json:"tokens"`
	ActiveSeconds int64    `json:"active_seconds"`
	Turns         int64    `json:"turns"`
}

// UsageLimit is a limit with what is used and what is left in its period.
type UsageLimit struct {
	Scope     string  `json:"scope"`
	Key       string  `json:"key"`
	Unit      string  `json:"unit"`
	Value     float64 `json:"value"`
	Effective float64 `json:"effective"`
	Used      float64 `json:"used"`
	Left      float64 `json:"left"`
	Percent   float64 `json:"percent"`
	Period    string  `json:"period"`
	HasData   bool    `json:"has_data"`
}

// UsagePage is the usage screen: per day, workspace, table, quest, character and
// account, and the limits. The Owner sees everything; the others, the workspaces
// they act in (and neither the accounts nor the total of the day).
type UsagePage struct {
	Today      string        `json:"today"`
	Days       []UsageTotals `json:"days"`
	Workspaces []UsageTotals `json:"workspaces"`
	Tables     []UsageTotals `json:"tables"`
	Quests     []UsageTotals `json:"quests"`
	Characters []UsageTotals `json:"characters"`
	Accounts   []UsageTotals `json:"accounts"`
	Limits     []UsageLimit  `json:"limits"`
}
