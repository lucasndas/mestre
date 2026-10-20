package protocol

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/lucasndas/mestre/internal/logging"
)

// The payload of every message type of docs/conectividade/mensagens.md. The
// struct is the source of the fields; the comment names the row of the
// table. A field tagged `proto:"req"` must be present (and not null) in the
// JSON; the others are optional, and unknown fields are ignored.

// Object is a JSON object whose fields are defined by another document
// (the machine sheet, the effective rules, an adapter event, a local-channel
// request).
type Object map[string]json.RawMessage

// Empty is the payload of a message that has no fields.
type Empty struct{}

// Secret is a value that must never reach a log: it prints as redacted and
// travels in clear only inside the JSON of the message.
type Secret string

func (Secret) String() string   { return logging.Redacted }
func (Secret) GoString() string { return `"` + logging.Redacted + `"` }

// Reveal returns the value.
func (s Secret) Reveal() string { return string(s) }

// Certificate is the certificate of epoch (docs/guilda/replicacao.md#certidão-de-época),
// the payload of the answer to guild.epoch.
type Certificate struct {
	GuildID          string    `json:"guild_id" proto:"req"`
	Epoch            uint64    `json:"epoch" proto:"req"`
	CitadelMachineID string    `json:"citadel_machine_id" proto:"req"`
	ApprovedBy       string    `json:"approved_by,omitempty"`
	ApprovalID       string    `json:"approval_id,omitempty"`
	IssuedAt         Timestamp `json:"issued_at" proto:"req"`
	Signature        string    `json:"signature" proto:"req"`
}

// Validate checks the epoch.
func (c *Certificate) Validate() error {
	if c.Epoch == 0 {
		return fmt.Errorf("certificate: epoch starts at 1")
	}
	return nil
}

// TerminalState is the state of a terminal that is already running, as the
// machine announces it in the opening and when it hands terminals over.
type TerminalState struct {
	TerminalID string `json:"terminal_id" proto:"req"`
	AgentID    string `json:"agent_id" proto:"req"`
	State      string `json:"state" proto:"req"`
	SessionID  string `json:"session_id,omitempty"`
}

// Hello is `hello`, Tower to Citadel (mensagens.md, row hello).
type Hello struct {
	Protocol     Range        `json:"protocol" proto:"req"`
	Capabilities Capabilities `json:"capabilities,omitempty"`
	Version      string       `json:"version" proto:"req"`
	Sheet        Object       `json:"sheet" proto:"req"`
	Certificate  *Certificate `json:"certificate" proto:"req"`
	ReplicaLSN   uint64       `json:"replica_lsn" proto:"req"`
	// ReplicaEpoch is the epoch of the replica: the one of the Citadel that
	// wrote the last record the Tower applied. The Citadel resumes the stream
	// only if it is its own epoch (docs/guilda/replicacao.md#banco-cópia-completa-e-envio-contínuo).
	ReplicaEpoch uint64          `json:"replica_epoch,omitempty"`
	Terminals    []TerminalState `json:"terminals,omitempty"`
}

// Validate checks the range.
func (h *Hello) Validate() error {
	if !h.Protocol.Valid() {
		return fmt.Errorf("protocol range %s is not valid", h.Protocol)
	}
	return nil
}

// Welcome is the answer to hello (mensagens.md, row hello).
type Welcome struct {
	Protocol     int          `json:"protocol" proto:"req"`
	Capabilities Capabilities `json:"capabilities,omitempty"`
	CitadelVer   string       `json:"citadel_version" proto:"req"`
	CitadelTime  Timestamp    `json:"citadel_time" proto:"req"`
	Certificate  *Certificate `json:"certificate" proto:"req"`
}

// Validate checks the version.
func (w *Welcome) Validate() error {
	if w.Protocol < 1 {
		return fmt.Errorf("protocol version %d is not valid", w.Protocol)
	}
	return nil
}

// MachineHeartbeat is `machine.heartbeat`.
type MachineHeartbeat struct {
	CPUPct        float64  `json:"cpu_pct" proto:"req"`
	MemPct        float64  `json:"mem_pct" proto:"req"`
	DiskFreeBytes uint64   `json:"disk_free_bytes" proto:"req"`
	Terminals     int      `json:"terminals" proto:"req"`
	ReplicaLagS   *float64 `json:"replica_lag_s,omitempty"`
}

// GuildConfigMachine is one machine of guild.config.
type GuildConfigMachine struct {
	MachineID string   `json:"machine_id" proto:"req"`
	Name      string   `json:"name"`
	Instance  string   `json:"instance,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
	// Port is the engine port of the machine (0 when unknown).
	Port     int  `json:"port,omitempty"`
	Priority int  `json:"priority"`
	Excluded bool `json:"excluded"`
}

// GuildConfig is `guild.config`.
type GuildConfig struct {
	Machines []GuildConfigMachine `json:"machines"`
	Revoked  []string             `json:"revoked,omitempty"`
	// Retired are the machines removed or gone: all their certificates are
	// refused, even one the registry never knew.
	Retired  []string `json:"retired,omitempty"`
	Timezone string   `json:"timezone" proto:"req"`
}

// GuildLeave is `guild.leave`.
type GuildLeave struct {
	Reason string `json:"reason,omitempty"`
}

// ReplicaAck is `replica.ack`.
type ReplicaAck struct {
	LSN uint64 `json:"lsn" proto:"req"`
}

// ReplicaResync is `replica.resync`; without FromLSN it asks for a full copy.
type ReplicaResync struct {
	FromLSN *uint64 `json:"from_lsn,omitempty"`
}

// VaultReplica is `vault.replica`, for every machine that is not excluded:
// the key ring of the vault data key (base64 of the ring, a secret that
// never reaches a log or the outbox: the type is volatile) and the recovery
// envelope. The secrets themselves travel in the database replica, already
// encrypted.
type VaultReplica struct {
	Keyring  Secret `json:"keyring" proto:"req"`
	Envelope []byte `json:"envelope" proto:"req"`
}

// VaultRequest is `vault.request`.
type VaultRequest struct {
	AgentID string `json:"agent_id" proto:"req"`
	Secret  string `json:"secret" proto:"req"`
	Purpose string `json:"purpose" proto:"req"`
}

// VaultCredential answers vault.request; the value lives only in memory.
type VaultCredential struct {
	Value Secret `json:"value" proto:"req"`
}

// GuildDiscarded is `guild.discarded`: the old Citadel, demoted to a Tower,
// lists the transactions it had committed that the promoted machine never
// applied (docs/guilda/replicacao.md#perda-na-troca), for the audit of the
// Guild. Total counts them all; Transactions may carry fewer, to fit a
// message.
type GuildDiscarded struct {
	Epoch        uint64        `json:"epoch" proto:"req"`
	FromLSN      uint64        `json:"from_lsn"`
	Total        int           `json:"total" proto:"req"`
	Transactions []DiscardedTx `json:"transactions"`
	// Dropped names the external actions that waited in the queue of the
	// fence when the old Citadel stepped down, and never went out.
	Dropped []string `json:"dropped,omitempty"`
}

// DiscardedTx is one discarded transaction: its lsn, when it committed and
// the rows it changed, by table and key, never the values.
type DiscardedTx struct {
	LSN     uint64            `json:"lsn"`
	At      Timestamp         `json:"at"`
	Changes []DiscardedChange `json:"changes"`
}

// DiscardedChange is a row of a discarded transaction.
type DiscardedChange struct {
	Table string `json:"table"`
	Op    string `json:"op"`
	Key   []any  `json:"key,omitempty"`
}

// CitadelHandover is `citadel.handover`.
type CitadelHandover struct {
	TargetMachineID string `json:"target_machine_id" proto:"req"`
	LastLSN         uint64 `json:"last_lsn" proto:"req"`
	ApprovalID      string `json:"approval_id" proto:"req"`
	// ApprovedBy is the user that approved the handover with the PIN, which
	// the certificate of the new epoch carries.
	ApprovedBy string `json:"approved_by,omitempty"`
}

// TerminalAdopt is `terminal.adopt`.
type TerminalAdopt struct {
	Terminals []TerminalState `json:"terminals" proto:"req"`
}

// The statuses of an ack.
const (
	AckOK        = "ok"
	AckDuplicate = "duplicate"
	AckError     = "error"
)

// OutboxFloor is `outbox.floor`: no message the sender may still send again
// on this link has an id lower than ID, so the receiver may forget the ids
// below it (docs/conectividade/protocolo.md#ack-e-idempotência).
type OutboxFloor struct {
	ID string `json:"id" proto:"req"`
}

// Validate checks the id.
func (f *OutboxFloor) Validate() error {
	if _, err := ParseULID(f.ID); err != nil {
		return fmt.Errorf("id: %w", err)
	}
	return nil
}

// Ack is `ack`: "received and accepted", not "done".
type Ack struct {
	Status string `json:"status" proto:"req"`
	// Error says why the status is "error".
	Error *Error `json:"error,omitempty"`
	// Result is the answer of a command that has one (the result of a
	// delivery, the screen of a terminal): it goes in the ack, which does not
	// wait behind the messages the receiver is still running.
	Result json.RawMessage `json:"result,omitempty"`
}

// Validate checks the status.
func (a *Ack) Validate() error {
	if !slices.Contains([]string{AckOK, AckDuplicate, AckError}, a.Status) {
		return fmt.Errorf("ack status %q is not ok, duplicate or error", a.Status)
	}
	return nil
}

// OperatorRef is the operator field of terminal.open.
type OperatorRef struct {
	SessionID string    `json:"session_id" proto:"req"`
	EndsAt    Timestamp `json:"ends_at" proto:"req"`
}

// TerminalOpen is `terminal.open`.
type TerminalOpen struct {
	TerminalID string            `json:"terminal_id" proto:"req"`
	AgentID    string            `json:"agent_id" proto:"req"`
	Command    string            `json:"command" proto:"req"`
	Args       []string          `json:"args,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Cwd        string            `json:"cwd,omitempty"`
	Adapter    string            `json:"adapter,omitempty"`
	Operator   *OperatorRef      `json:"operator,omitempty"`
}

// TerminalOpened is `terminal.opened`.
type TerminalOpened struct {
	TerminalID string `json:"terminal_id" proto:"req"`
	PID        int    `json:"pid" proto:"req"`
}

// The modes of terminal.input and terminal.close.
const (
	InputMessage  = "message"
	InputRaw      = "raw"
	CloseGraceful = "graceful"
	CloseKill     = "kill"
)

// TerminalInput is `terminal.input`.
type TerminalInput struct {
	TerminalID string `json:"terminal_id" proto:"req"`
	Mode       string `json:"mode" proto:"req"`
	Data       string `json:"data" proto:"req"`
}

// Validate checks the mode.
func (t *TerminalInput) Validate() error {
	if t.Mode != InputMessage && t.Mode != InputRaw {
		return fmt.Errorf("mode %q is not message or raw", t.Mode)
	}
	return nil
}

// TerminalClose is `terminal.close`.
type TerminalClose struct {
	TerminalID string `json:"terminal_id" proto:"req"`
	Mode       string `json:"mode" proto:"req"`
}

// Validate checks the mode.
func (t *TerminalClose) Validate() error {
	if t.Mode != CloseGraceful && t.Mode != CloseKill {
		return fmt.Errorf("mode %q is not graceful or kill", t.Mode)
	}
	return nil
}

// TerminalStatus is `terminal.status`. The states are owned by the
// characters (docs/orquestracao/personagens.md), so they are not checked. The
// last fields are the report of a character a Tower makes to the Citadel
// (characters.Report): the Citadel takes it only from the machine the
// registry puts the character on.
type TerminalStatus struct {
	TerminalID string `json:"terminal_id" proto:"req"`
	State      string `json:"state" proto:"req"`
	Reason     string `json:"reason,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	// AgentID is the character; Seq grows with every report of it, so a late
	// one is dropped; At is when the Tower saw the change.
	AgentID string     `json:"agent_id,omitempty"`
	Seq     uint64     `json:"seq,omitempty"`
	At      *Timestamp `json:"at,omitempty"`
	// PTYPid, PTYStarted and PTYVersion identify the mestre-pty of the terminal.
	PTYPid     int    `json:"pty_pid,omitempty"`
	PTYStarted string `json:"pty_started,omitempty"`
	PTYVersion string `json:"pty_version,omitempty"`
	// Reduced is why the monitoring is reduced ("" clears it); nil leaves it.
	Reduced *string `json:"reduced,omitempty"`
	// Resync marks the states a machine reports when it reconnects.
	Resync bool `json:"resync,omitempty"`
}

// CharacterStart is `character.start`: the Citadel renders the request of the
// session zero of a character and the Tower prepares it and starts the
// terminal. Request is the JSON of sessionzero.CharacterRequest; it can carry
// the API key of the provider, so the message is volatile.
type CharacterStart struct {
	Character     string `json:"character" proto:"req"`
	TerminalID    string `json:"terminal_id" proto:"req"`
	Kind          string `json:"kind" proto:"req"`
	Resumed       bool   `json:"resumed,omitempty"`
	ResumeMessage string `json:"resume_message,omitempty"`
	Request       Object `json:"request" proto:"req"`
}

// CharacterStop is `character.stop`: the end of a character on its machine.
type CharacterStop struct {
	Character string `json:"character" proto:"req"`
	// Final ends for good (the files go away); false is a restart that resumes.
	Final  bool   `json:"final,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Abandon ends a character that is still starting (the Citadel gave up on the start).
	// TerminalID, when given, is the terminal of that start: a character that runs in
	// another one (a restart that came after) is not touched.
	Abandon    bool   `json:"abandon,omitempty"`
	TerminalID string `json:"terminal_id,omitempty"`
}

// CharacterNotice is `character.notice`: a notice about a character of the
// Tower, to the master of its table. The Citadel takes the workspace, the
// table and the name from its registry, never from the message.
type CharacterNotice struct {
	Kind      string `json:"kind" proto:"req"`
	Character string `json:"character" proto:"req"`
	Text      string `json:"text,omitempty"`
	// Screen is the screen that waits for input (kind blocked_input).
	Screen string `json:"screen,omitempty"`
}

// CharacterResync is `character.resync`: every terminal a Tower has, as it is,
// at each connection. The Citadel takes it as the truth about the processes:
// a character its registry has alive on that machine and the list does not
// have is interrupted.
type CharacterResync struct {
	Terminals []TerminalState `json:"terminals"`
	// At is when the Tower took the picture: a character that changed on the
	// Citadel after it is not judged by it.
	At *Timestamp `json:"at,omitempty"`
}

// CharacterResyncResult is the answer to `character.resync`, in the ack: the
// characters the Tower has a terminal for that the Citadel does not have (any
// more) on that machine, which the Tower ends.
type CharacterResyncResult struct {
	End []string `json:"end,omitempty"`
}

// TableRef names the worktree of a table on a Tower (`table.where`,
// `table.remove`, `table.done`).
type TableRef struct {
	Workspace string `json:"workspace" proto:"req"`
	Table     string `json:"table" proto:"req"`
}

// TableWhere is the answer to `table.where`, in the ack: where the Tower keeps
// (or would keep) the worktree of the table and, when it exists, its state.
type TableWhere struct {
	Dir    string   `json:"dir" proto:"req"`
	Exists bool     `json:"exists"`
	Tip    string   `json:"tip,omitempty"`
	Dirty  []string `json:"dirty,omitempty"`
}

// TableSent is `table.sent`, the end of a `table.done` (its reply_to): the
// Tower sent its commits to the Citadel (Sent), up to Tip; Warnings lists what
// did not travel; Error says why it could not send.
type TableSent struct {
	Workspace string   `json:"workspace" proto:"req"`
	Table     string   `json:"table" proto:"req"`
	Sent      bool     `json:"sent"`
	Tip       string   `json:"tip,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// RitualPreRun is `ritual.prerun`: run Command as the character, in its
// worktree, without interaction. The answer is RitualPreRunResult, in the ack.
type RitualPreRun struct {
	Character string `json:"character" proto:"req"`
	Command   string `json:"command" proto:"req"`
	// TimeoutSeconds bounds the run; MaxOutput is the most that is kept of the output.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	MaxOutput      int `json:"max_output,omitempty"`
}

// RitualPreRunResult is what came of the pre-run.
type RitualPreRunResult struct {
	Output   string `json:"output,omitempty"`
	ExitCode int    `json:"exit_code"`
	Stderr   string `json:"stderr,omitempty"`
	TimedOut bool   `json:"timed_out,omitempty"`
}

// ScrollPut is `scroll.put`: write the read-only copy of a scroll at Rel, in the
// worktree Dir of a table on this machine.
type ScrollPut struct {
	Dir     string `json:"dir" proto:"req"`
	Rel     string `json:"rel" proto:"req"`
	Content string `json:"content"`
}

// ScrollRemove is `scroll.remove`: take the copy of a scroll that was deleted.
type ScrollRemove struct {
	Dir string `json:"dir" proto:"req"`
	Rel string `json:"rel" proto:"req"`
}

// TableNotice is `table.notice`: a Tower tells the master of a table something
// about its worktree (a branch that diverged, a worktree that conflicts).
type TableNotice struct {
	Workspace string `json:"workspace" proto:"req"`
	Table     string `json:"table" proto:"req"`
	Text      string `json:"text" proto:"req"`
}

// CharacterTurn is `character.turn`: a turn of a character of the Tower ended,
// with the last message of the agent in it.
type CharacterTurn struct {
	AgentID     string `json:"agent_id" proto:"req"`
	LastMessage string `json:"last_message,omitempty"`
}

// TerminalReplay is `terminal.replay`.
type TerminalReplay struct {
	TerminalID string `json:"terminal_id" proto:"req"`
	From       uint64 `json:"from" proto:"req"`
}

// TerminalReplayed is `terminal.replayed`; Lost marks a stretch that already
// left the buffer.
type TerminalReplayed struct {
	TerminalID string `json:"terminal_id,omitempty"`
	From       uint64 `json:"from" proto:"req"`
	To         uint64 `json:"to" proto:"req"`
	Lost       bool   `json:"lost"`
}

// TerminalScreen is `terminal.screen`.
type TerminalScreen struct {
	TerminalID string `json:"terminal_id" proto:"req"`
	Lines      int    `json:"lines"`
}

// TerminalScreenResult is `terminal.screen_result`.
type TerminalScreenResult struct {
	Text       string    `json:"text" proto:"req"`
	Cursor     Object    `json:"cursor,omitempty"`
	CapturedAt Timestamp `json:"captured_at" proto:"req"`
	// History is the lines that scrolled off, when `lines` asked for them.
	History string `json:"history,omitempty"`
}

// InboxDeliver is `inbox.deliver`.
type InboxDeliver struct {
	TerminalID string `json:"terminal_id" proto:"req"`
	ItemID     string `json:"item_id" proto:"req"`
	Text       string `json:"text" proto:"req"`
	Header     string `json:"header,omitempty"`
}

// The statuses of inbox.result.
const (
	InboxDelivered = "delivered"
	InboxBusy      = "busy"
	// InboxConfirmed and InboxUnconfirmed come later, in a message of their
	// own: the turn that the delivery began was seen (or was not, within the
	// time the supervision waits).
	InboxConfirmed   = "confirmed"
	InboxUnconfirmed = "unconfirmed"
)

// InboxResult is `inbox.result`; busy gives the item back to the queue of
// the Citadel.
type InboxResult struct {
	ItemID string `json:"item_id" proto:"req"`
	Status string `json:"status" proto:"req"`
}

// Validate checks the status.
func (r *InboxResult) Validate() error {
	switch r.Status {
	case InboxDelivered, InboxBusy, InboxConfirmed, InboxUnconfirmed:
		return nil
	}
	return fmt.Errorf("status %q is not delivered, busy, confirmed or unconfirmed", r.Status)
}

// AgentSiteLogin is `agent.site_login`; the machine keeps the secret only in
// memory.
type AgentSiteLogin struct {
	AttemptID    string   `json:"attempt_id" proto:"req"`
	AgentID      string   `json:"agent_id" proto:"req"`
	Domains      []string `json:"domains" proto:"req"`
	LoginURL     string   `json:"login_url" proto:"req"`
	Username     string   `json:"username" proto:"req"`
	Secret       Secret   `json:"secret" proto:"req"`
	Selectors    Object   `json:"selectors,omitempty"`
	SuccessCheck Object   `json:"success_check,omitempty"`
}

// The states of agent.site_login_status.
const (
	SiteLoginNeedsCode     = "needs_code"
	SiteLoginNeedsPush     = "needs_push"
	SiteLoginNeedsCaptcha  = "needs_captcha"
	SiteLoginOK            = "ok"
	SiteLoginRequiresHuman = "requires_human"
	SiteLoginFailed        = "failed"
)

// AgentSiteLoginStatus is `agent.site_login_status`.
type AgentSiteLoginStatus struct {
	AttemptID string `json:"attempt_id" proto:"req"`
	State     string `json:"state" proto:"req"`
	XferID    string `json:"xfer_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Validate checks the state and what each one needs.
func (s *AgentSiteLoginStatus) Validate() error {
	switch s.State {
	case SiteLoginNeedsCode, SiteLoginNeedsPush, SiteLoginOK, SiteLoginRequiresHuman:
	case SiteLoginNeedsCaptcha:
		if s.XferID == "" {
			return fmt.Errorf("needs_captcha carries the xfer_id of the image")
		}
	case SiteLoginFailed:
		if s.Reason == "" {
			return fmt.Errorf("failed carries a reason")
		}
	default:
		return fmt.Errorf("state %q is unknown", s.State)
	}
	return nil
}

// AgentSiteLoginInput is `agent.site_login_input`; the value never goes to a
// log.
type AgentSiteLoginInput struct {
	AttemptID string `json:"attempt_id" proto:"req"`
	Value     Secret `json:"value" proto:"req"`
}

// OperatorSession is `operator.session`.
type OperatorSession struct {
	SessionID string    `json:"session_id" proto:"req"`
	State     string    `json:"state" proto:"req"`
	At        Timestamp `json:"at" proto:"req"`
}

// Validate checks the state.
func (o *OperatorSession) Validate() error {
	if !slices.Contains([]string{"started", "locked", "stopped_on_pc", "session_lost"}, o.State) {
		return fmt.Errorf("state %q is unknown", o.State)
	}
	return nil
}

// OperatorCapture is `operator.capture`.
type OperatorCapture struct {
	SessionID  string    `json:"session_id" proto:"req"`
	CapturedAt Timestamp `json:"captured_at" proto:"req"`
	Trigger    string    `json:"trigger" proto:"req"`
	EventID    string    `json:"event_id" proto:"req"`
	XferID     string    `json:"xfer_id" proto:"req"`
}

// Validate checks the trigger.
func (o *OperatorCapture) Validate() error {
	if o.Trigger != "intervalo" && o.Trigger != "acao" {
		return fmt.Errorf("trigger %q is not intervalo or acao", o.Trigger)
	}
	return nil
}

// CLICall is `cli.call`: the request of the local channel exactly as it is,
// plus the verified caller. CLIResult is `cli.result`: the response of the
// local channel exactly as it is.
type (
	CLICall   Object
	CLIResult Object
)

// Validate checks the caller.
func (c CLICall) Validate() error {
	var caller struct {
		AgentID    string `json:"agent_id"`
		TerminalID string `json:"terminal_id"`
		MachineID  string `json:"machine_id"`
	}
	raw, ok := c["caller"]
	if !ok {
		return fmt.Errorf("caller is required")
	}
	if err := json.Unmarshal(raw, &caller); err != nil {
		return fmt.Errorf("caller: %w", err)
	}
	if caller.AgentID == "" || caller.TerminalID == "" || caller.MachineID == "" {
		return fmt.Errorf("caller needs agent_id, terminal_id and machine_id")
	}
	return nil
}

// SyncMessage is `sync.request` and `sync.applied`.
type SyncMessage struct {
	TableID    string `json:"table_id" proto:"req"`
	Branch     string `json:"branch" proto:"req"`
	BaseCommit string `json:"base_commit,omitempty"`
	TipCommit  string `json:"tip_commit" proto:"req"`
}

// TranscribeRequest is `transcribe.request`.
type TranscribeRequest struct {
	AudioXferID string `json:"audio_xfer_id" proto:"req"`
	Language    string `json:"language,omitempty"`
}

// TranscribeResult is `transcribe.result`.
type TranscribeResult struct {
	Text      string  `json:"text" proto:"req"`
	DurationS float64 `json:"duration_s" proto:"req"`
}

// CertRenew is `cert.renew`.
type CertRenew struct {
	Role string `json:"role" proto:"req"`
	CSR  string `json:"csr" proto:"req"`
}

// Validate checks the role.
func (c *CertRenew) Validate() error {
	if c.Role != "motor" && c.Role != "guardiao" {
		return fmt.Errorf("role %q is not motor or guardiao", c.Role)
	}
	return nil
}

// CertIssued is `cert.issued`.
type CertIssued struct {
	Cert string `json:"cert" proto:"req"`
}

// UpdateApply is `update.apply`.
type UpdateApply struct {
	ReleaseTag string `json:"release_tag" proto:"req"`
	Component  string `json:"component" proto:"req"`
	XferID     string `json:"xfer_id" proto:"req"`
}

// Validate checks the component.
func (u *UpdateApply) Validate() error {
	if u.Component != "motor" && u.Component != "guardiao" {
		return fmt.Errorf("component %q is not motor or guardiao", u.Component)
	}
	return nil
}

// The kinds of transfer (docs/conectividade/protocolo.md#transferência-em-partes).
const (
	KindGitBundle  = "git-bundle"
	KindFile       = "file"
	KindDBSnapshot = "db-snapshot"
	KindRelease    = "release"
)

// DefaultChunkSize is the size of a part unless the sender says otherwise.
const DefaultChunkSize = 256 << 10

// MaxXferSize is the largest content a transfer may announce (64 GiB).
const MaxXferSize = 64 << 30

// XferBegin is `xfer.begin`. For a bundle Meta carries the table, the
// branch, the prerequisite commits and the tip; for a file, the destination
// path.
type XferBegin struct {
	XferID    string `json:"xfer_id" proto:"req"`
	Kind      string `json:"kind" proto:"req"`
	Size      int64  `json:"size" proto:"req"`
	SHA256    string `json:"sha256" proto:"req"`
	ChunkSize int    `json:"chunk_size,omitempty"`
	Meta      Object `json:"meta,omitempty"`
}

// Chunk is the size of a part, with the default applied.
func (b *XferBegin) Chunk() int {
	if b.ChunkSize <= 0 {
		return DefaultChunkSize
	}
	return b.ChunkSize
}

// Parts is how many parts the transfer has; it does not overflow whatever
// the size.
func (b *XferBegin) Parts() uint64 {
	if b.Size <= 0 {
		return 0
	}
	c, s := uint64(b.Chunk()), uint64(b.Size)
	n := s / c
	if s%c != 0 {
		n++
	}
	return n
}

// Validate checks the transfer.
func (b *XferBegin) Validate() error {
	if _, err := ParseULID(b.XferID); err != nil {
		return fmt.Errorf("xfer_id: %w", err)
	}
	if !slices.Contains([]string{KindGitBundle, KindFile, KindDBSnapshot, KindRelease}, b.Kind) {
		return fmt.Errorf("kind %q is unknown", b.Kind)
	}
	if b.Size < 0 || b.Size > MaxXferSize {
		return fmt.Errorf("size %d is out of range (at most %d)", b.Size, int64(MaxXferSize))
	}
	if b.ChunkSize < 0 || b.ChunkSize > MaxDataPayload {
		return fmt.Errorf("chunk_size %d is out of range", b.ChunkSize)
	}
	if sum, err := hex.DecodeString(b.SHA256); err != nil || len(sum) != 32 {
		return fmt.Errorf("sha256 is not 64 hex digits")
	}
	return nil
}

// XferReady answers xfer.begin with the part to start from: 0, or the point
// where the receiver already has its temporary file.
type XferReady struct {
	XferID    string `json:"xfer_id" proto:"req"`
	NextIndex uint64 `json:"next_index" proto:"req"`
}

// XferProgress is `xfer.progress`: the next part the receiver expects; it
// frees the window of the sender. Resend says the receiver dropped what came
// after NextIndex (a part was missing): the sender goes back to it.
type XferProgress struct {
	XferID    string `json:"xfer_id" proto:"req"`
	NextIndex uint64 `json:"next_index" proto:"req"`
	Resend    bool   `json:"resend,omitempty"`
}

// XferEnd is `xfer.end`.
type XferEnd struct {
	XferID string `json:"xfer_id" proto:"req"`
}

// XferDone is `xfer.done`: ok, or the error.
type XferDone struct {
	XferID string `json:"xfer_id" proto:"req"`
	OK     bool   `json:"ok" proto:"req"`
	Error  *Error `json:"error,omitempty"`
}
