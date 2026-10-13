package guild

import (
	"context"
	"time"
)

// ApprovalRequest is the question to the Owner when a new machine asks to
// enter: the request of type entrada_na_guilda (docs/aprovacoes/fluxo.md#pedidos-da-guilda).
type ApprovalRequest struct {
	JoinID   string
	Name     string
	OS       string
	Hostname string
	Instance string
	// Code is what the Owner compares with the one on the screen of the
	// installer: "123 456".
	Code      string
	RemoteIP  string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Approver puts the question to the Owner, through the Telegram bot and the
// panel, with the button and the PIN; both belong to milestone 5. The
// Guild only asks and is told the answer: the bot or the panel calls
// Citadel.Decide once the Owner pressed the button and gave the PIN. In the
// tests of this milestone the Approver is a double.
type Approver interface {
	// Ask shows the question and returns the id of the approval. It is
	// called once the code exists, and again if an earlier call failed.
	Ask(ctx context.Context, r ApprovalRequest) (approvalID string, err error)
	// Withdraw takes the question down: the request expired.
	Withdraw(ctx context.Context, approvalID string)
}

// Decision is the answer of the Owner.
type Decision struct {
	Accept bool
	// ApprovedBy is the user that answered, with the PIN.
	ApprovedBy string
	// Excluded marks the machine as excluded already at the entry (default:
	// no, docs/guilda/guilda.md#máquina-excluída).
	Excluded bool
}

// Peers is the connection side of what the Citadel does to a machine: tell
// it to leave, and drop the connections of the revoked. ProtocolPeers is the
// implementation over the protocol and the transport.
type Peers interface {
	// Leave sends guild.leave (reason removed) and waits for the ack; it
	// fails when the machine is offline or does not answer in time.
	Leave(ctx context.Context, machineID, reason string) error
	// CloseRevoked closes the open connections of revoked credentials with
	// 4002 and returns how many.
	CloseRevoked() int
}

// Hooks are the points where the later stages (replication, the panel, the
// bot) plug in. Every field is optional.
type Hooks struct {
	// Joined: a machine entered.
	Joined func(m Machine)
	// Removed: a machine was removed by the Owner or left by itself
	// (reason removed or uninstall).
	Removed func(m Machine, reason string)
	// ExclusionChanged: the Owner excluded a machine or undid it. The
	// replication sends vault.drop to the excluded machine, or the vault
	// again when the exclusion is undone, and guild.config to everybody;
	// the machine then renews its certificates.
	ExclusionChanged func(m Machine)
	// RefusalAlert: the number of requests refused in the last 24 hours
	// reached a multiple of Limits.RefusalAlert (20 by default): the Owner is
	// warned.
	RefusalAlert func(refused int)
}

// Wiper erases a machine's share of the Guild when it leaves: the replica of
// the database, the replicated files, the copy of the vault and the Guild
// configuration. The installer and the replication provide it; the keys and
// certificates of the engine files go with it.
type Wiper interface {
	Wipe(ctx context.Context, reason string) error
}
