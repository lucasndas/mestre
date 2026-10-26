package panel

import "time"

// The Guild and the health of the machines (docs/painel/administracao.md). The
// topic is "guild:main": its snapshot is a GuildView and EventGuild carries the
// view again when it changed. tygo turns these types into web/src/gen/panel.ts.

// EventGuild is the event of the guild topic: the view changed.
const EventGuild = "view"

// GuildView is GET /api/guild.
type GuildView struct {
	// Epoch is the epoch of the Guild, at the top of the screen.
	Epoch    uint64 `json:"epoch"`
	Timezone string `json:"timezone"`
	// Single says only the Citadel is in the Guild: the screen shows the band of
	// the single-machine mode.
	Single   bool               `json:"single"`
	Machines []GuildMachineRow  `json:"machines"`
	Pending  []GuildPendingJoin `json:"pending"`
}

// GuildMachineRow is one machine. Every text in it is the machine's own word and
// the screen shows it as text, never as markup.
type GuildMachineRow struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Instance string `json:"instance,omitempty"`
	// Citadel is true for the machine that is the Citadel of the epoch; Self for
	// the machine that answers.
	Citadel bool `json:"citadel"`
	Self    bool `json:"self"`
	// Suspended: the Citadel does not act now (a fence or a handover under way).
	Suspended bool `json:"suspended,omitempty"`
	// GitHubDown and TelegramStopped are the signs of the lock at the GitHub and of
	// the bot ("GitHub fora", "Telegram parado"), filled when the engine knows them.
	GitHubDown      bool `json:"github_down,omitempty"`
	TelegramStopped bool `json:"telegram_stopped,omitempty"`
	// Online: the machine is connected to the Citadel. LastSeen is when the engine
	// last saw it online (nil: not since it started).
	Online   bool       `json:"online"`
	LastSeen *time.Time `json:"last_seen,omitempty"`
	// OutsideNetwork: no address of the machine is in the local network of the
	// Citadel (it entered by an address); it is never promoted.
	OutsideNetwork bool `json:"outside_network"`
	// EngineVersion is known for this machine only; Protocol is the protocol
	// negotiated on the connection (0: not connected).
	EngineVersion string     `json:"engine_version,omitempty"`
	Protocol      int        `json:"protocol,omitempty"`
	Sheet         GuildSheet `json:"sheet"`
	Excluded      bool       `json:"excluded"`
	// Replica is the state of the replica of the machine (nil: not connected).
	Replica  *GuildReplica `json:"replica,omitempty"`
	Load     GuildLoad     `json:"load"`
	Agents   []GuildAgent  `json:"agents"`
	Priority int           `json:"priority"`
}

// GuildSheet is the summary of the sheet of the machine.
type GuildSheet struct {
	OS        string   `json:"os,omitempty"`
	GUI       bool     `json:"gui"`
	Providers []string `json:"providers"`
	Browser   string   `json:"browser,omitempty"`
	// Operator is "off", "ready" or "not_ready" (OperatorReason says why).
	Operator       string   `json:"operator,omitempty"`
	OperatorReason string   `json:"operator_reason,omitempty"`
	CPUCores       int      `json:"cpu_cores"`
	MemBytes       uint64   `json:"mem_bytes"`
	GPUs           []string `json:"gpus"`
	Transcribes    bool     `json:"transcribes"`
}

// GuildReplica is the state of the copy of the database in a Tower: State is
// starting, snapshot or streaming; Behind the records it still lacks and LagMS
// how long the oldest has waited.
type GuildReplica struct {
	State  string `json:"state"`
	Behind uint64 `json:"behind"`
	LagMS  int64  `json:"lag_ms"`
}

// GuildLoad is what the sheet says of the machine and how many agents run on it.
type GuildLoad struct {
	DiskFreeBytes uint64 `json:"disk_free_bytes"`
	Running       int    `json:"running"`
}

// GuildAgent is a character that runs on the machine.
type GuildAgent struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Workspace string `json:"workspace"`
	Table     string `json:"table,omitempty"`
	State     string `json:"state"`
}

// GuildPendingJoin is a machine that asks to enter the Guild: the code is the
// one the installer screen shows.
type GuildPendingJoin struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Code      string    `json:"code,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// GuildRemoval is GET /api/guild/machines/{id}/removal: what a removal takes
// with it, to warn before the PIN.
type GuildRemoval struct {
	Machine string `json:"machine"`
	Name    string `json:"name"`
	// Online: the machine is told to erase its share of the Guild; offline, it
	// keeps the encrypted copy of the vault and the secrets should be replaced.
	Online bool         `json:"online"`
	Agents []GuildAgent `json:"agents"`
}

// GuildRemoved is the answer of the removal.
type GuildRemoved struct {
	Notified bool `json:"notified"`
}

// GuildDecideRequest is POST /api/guild/joins/{id}/decide.
type GuildDecideRequest struct {
	Accept bool `json:"accept"`
	// Excluded takes the machine in already excluded.
	Excluded bool `json:"excluded,omitempty"`
}

// GuildRemoveRequest is POST /api/guild/machines/{id}/remove: Confirm says the
// warning of the removal was read.
type GuildRemoveRequest struct {
	Confirm bool `json:"confirm"`
}

// GuildExcludeRequest is POST /api/guild/machines/{id}/exclusion.
type GuildExcludeRequest struct {
	Excluded bool `json:"excluded"`
}

// GuildPriorityRequest is PUT /api/guild/machines/{id}/priority.
type GuildPriorityRequest struct {
	Priority int `json:"priority"`
}

// GuildTimezoneRequest is PUT /api/guild/timezone.
type GuildTimezoneRequest struct {
	Timezone string `json:"timezone"`
}
