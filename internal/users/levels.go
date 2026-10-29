package users

import (
	"errors"
	"slices"

	"github.com/mestre-ia/mestre/internal/i18n"
)

// Level is the level of a user. The table of what each one can do is Can: it is
// the only place of the rules of level (docs/telegram/usuarios-e-niveis.md).
type Level string

// The levels, from the most to the least powerful.
const (
	Owner         Level = "owner"
	Administrator Level = "administrator"
	Approver      Level = "approver"
	Observer      Level = "observer"
)

// Valid says whether l is one of the four levels.
func (l Level) Valid() bool { return rank(l) > 0 }

func rank(l Level) int {
	switch l {
	case Owner:
		return 4
	case Administrator:
		return 3
	case Approver:
		return 2
	case Observer:
		return 1
	}
	return 0
}

// Msg is the name of the level as a message, translated by whoever shows it.
func (l Level) Msg() i18n.Msg {
	switch l {
	case Owner:
		return i18n.M("level.owner")
	case Administrator:
		return i18n.M("level.administrator")
	case Approver:
		return i18n.M("level.approver")
	case Observer:
		return i18n.M("level.observer")
	}
	return i18n.M("level.unknown", "level", string(l))
}

// Name is how the level is said to people (in pt-BR, for the places that have
// no language yet: use Msg).
func (l Level) Name() string {
	switch l {
	case Owner:
		return "Senhor da Guilda"
	case Administrator:
		return "Administrador"
	case Approver:
		return "Aprovador"
	case Observer:
		return "Observador"
	}
	return string(l)
}

// Capability is one row of the table of levels.
type Capability string

// The capabilities of the table, by the order of the document.
const (
	Notifications        Capability = "notifications"         // receive alerts and notices
	Status               Capability = "status"                // /status, /missoes, the panel in reading
	TalkToGrandMaster    Capability = "talk_to_grand_master"  // requests and questions
	DecideApprovals      Capability = "decide_approvals"      // approve or deny
	OneOffException      Capability = "one_off_exception"     // an exception of its own will
	PermanentDecree      Capability = "permanent_decree"      // a rule that stays
	Pause                Capability = "pause"                 // /pausar and /retomar
	PauseButtons         Capability = "pause_buttons"         // Pausar mesa, Pausar autonomia, resume an autonomy pause by a person
	RevokeTemporaryRules Capability = "revoke_temporary"      // table rules, subdelegations, "Foi um erro"
	Lock                 Capability = "lock"                  // /travar
	Unlock               Capability = "unlock"                // /destravar (with PIN)
	EndOperatorSession   Capability = "end_operator_session"  // /operador encerrar
	TypeInTerminal       Capability = "type_in_terminal"      // from the canvas
	EditCanvas           Capability = "edit_canvas"           // connections, missions, pins, scrolls, grimoires and rituals at the canvas
	CreateWorkspace      Capability = "create_workspace"      //
	ConfigureWorkspace   Capability = "configure_workspace"   //
	ApproveWorkspaceCls  Capability = "approve_workspace_cls" // a class of the workspace
	RaiseCeiling         Capability = "raise_ceiling"         // widen the ceiling or go past the cost ceiling
	ApproveHealing       Capability = "approve_healing"       // cure actions that ask for a person
	ActivateClasses      Capability = "activate_classes"      // suggested classes, Testar, MCP catalog, reserve page, Audit
	Transcription        Capability = "transcription"         // Whisper on a machine
	ManageUsers          Capability = "manage_users"          // register, edit, suspend, remove, pairing codes
	ManageMachines       Capability = "manage_machines"       // accept, remove, exclude, priority, promote, pass the Citadel
	BasesVaultPolicy     Capability = "bases_vault_policy"    // bases, sheets, approval policy, the vault
	GitHubApps           Capability = "github_apps"           //
	AccountProfiles      Capability = "account_profiles"      //
	OperatorActivation   Capability = "operator_activation"   // activation of the Operator, `operator` in the ceiling, its captures
	DelegateHigh         Capability = "delegate_high"         // delegate a high type; /revogar_tudo
	GuildSettings        Capability = "guild_settings"        // time zone, total daily limit, glossary_seen
	ConnectOwnGitHub     Capability = "connect_own_github"    //
	TransferOwner        Capability = "transfer_owner"        //
)

// minimum is the lowest level that has each capability.
var minimum = map[Capability]Level{
	Notifications: Observer, Status: Observer,
	TalkToGrandMaster: Approver, DecideApprovals: Approver, OneOffException: Approver, PauseButtons: Approver, ConnectOwnGitHub: Approver,
	PermanentDecree: Administrator, Pause: Administrator, RevokeTemporaryRules: Administrator, Lock: Administrator, EndOperatorSession: Administrator,
	TypeInTerminal: Administrator, EditCanvas: Administrator, CreateWorkspace: Administrator, ConfigureWorkspace: Administrator, ApproveWorkspaceCls: Administrator,
	RaiseCeiling: Administrator, ApproveHealing: Administrator, ActivateClasses: Administrator, Transcription: Administrator,
	Unlock: Owner, ManageUsers: Owner, ManageMachines: Owner, BasesVaultPolicy: Owner, GitHubApps: Owner, AccountProfiles: Owner,
	OperatorActivation: Owner, DelegateHigh: Owner, GuildSettings: Owner, TransferOwner: Owner,
}

// Can says whether a user of the level has the capability.
func Can(l Level, c Capability) bool {
	need, ok := minimum[c]
	return ok && rank(l) >= rank(need)
}

// Minimum is the lowest level that has the capability ("" if unknown).
func Minimum(c Capability) Level { return minimum[c] }

// LevelError is the refusal of an action the level does not allow: the bot or the
// panel says which level is required, and nothing is done.
type LevelError struct {
	Capability Capability
	Have, Need Level
}

func (e *LevelError) Error() string { return e.Msg().String() }

// Msg is the refusal as a message: the level that is required and the one the
// user has, translated by whoever shows it.
func (e *LevelError) Msg() i18n.Msg {
	if e.Need == Owner {
		return i18n.M("level.required", "need", e.Need.Msg(), "have", e.Have.Msg())
	}
	return i18n.M("level.required_or_above", "need", e.Need.Msg(), "have", e.Have.Msg())
}

// ErrLevel is what errors.Is finds in every LevelError.
var ErrLevel = errors.New("users: the level does not allow it")

// Is implements errors.Is.
func (e *LevelError) Is(target error) bool { return target == ErrLevel }

// Require is nil when the level has the capability and a *LevelError otherwise
// (a capability the table does not know is refused: it fails closed).
func Require(l Level, c Capability) error {
	if Can(l, c) {
		return nil
	}
	need := minimum[c]
	if need == "" {
		need = Owner
	}
	return &LevelError{Capability: c, Have: l, Need: need}
}

// RevokeScope says which decrees a level may revoke
// (docs/telegram/usuarios-e-niveis.md, "Revogar decretos").
type RevokeScope int

// The scopes of revoking.
const (
	RevokeNone RevokeScope = iota
	// RevokeOwnUnused: only its own exceptions that were not used yet.
	RevokeOwnUnused
	// RevokeAdminAndApprover: the ones made by Administrators and Approvers.
	RevokeAdminAndApprover
	// RevokeAny: every one.
	RevokeAny
)

// RevokeDecrees is what the level may revoke of the decrees.
func RevokeDecrees(l Level) RevokeScope {
	switch l {
	case Owner:
		return RevokeAny
	case Administrator:
		return RevokeAdminAndApprover
	case Approver:
		return RevokeOwnUnused
	}
	return RevokeNone
}

// Capabilities lists the capabilities of the table, sorted.
func Capabilities() []Capability {
	out := make([]Capability, 0, len(minimum))
	for c := range minimum {
		out = append(out, c)
	}
	slices.Sort(out)
	return out
}
