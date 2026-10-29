package decrees

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/users"
)

// Kind tells a permanent rule from a one-time exception.
type Kind string

// The kinds.
const (
	KindRule      Kind = "rule"
	KindException Kind = "exception"
)

// State is the state of a decree.
type State string

// The states of a decree (docs/telegram/decretos.md#estados). The proposal
// that is not confirmed is not a decree: it is cancelled in its own table.
const (
	Active    State = "active"
	Used      State = "used"
	Expired   State = "expired"
	Revoked   State = "revoked"
	Suspended State = "suspended"
)

// Ended says the decree is over for good.
func (s State) Ended() bool { return s == Used || s == Expired || s == Revoked }

// The effects of a decree.
const (
	EffectPermission = "permission"
	EffectBehavior   = "behavior"
	EffectPolicy     = "policy"
	EffectCeiling    = "ceiling"
	EffectDelegation = "delegation"
	// EffectClassEdit is not an effect a decree can have: it names the
	// attempt to change a class itself, which is refused.
	EffectClassEdit = "class"
)

// Effect is what a decree does.
type Effect struct {
	Kind string `json:"kind"`
	// Allow and Deny are the patterns of a permission, with the vocabulary of
	// permissions.yaml; Allow also holds what a ceiling widens.
	Allow *classes.Grant `json:"allow,omitempty"`
	Deny  *classes.Grant `json:"deny,omitempty"`
	// Instruction is the short instruction of a behavior.
	Instruction string `json:"instruction,omitempty"`
	// Setting is the type of request (or the setting) a policy adjusts and
	// Loosens says the adjustment loosens it; for a delegation, Setting is
	// the type of request the Grand Master may then decide.
	Setting string `json:"setting,omitempty"`
	Loosens bool   `json:"loosens,omitempty"`
	// MaxMinutes is the longest activation the Grand Master may approve under
	// a delegation of the Operator (at most 30 by default).
	MaxMinutes int `json:"max_minutes,omitempty"`
	// Field is what a class edit names (refused).
	Field string `json:"field,omitempty"`
}

// TargetKind is what a decree is about.
type TargetKind string

// The targets.
const (
	TargetClass     TargetKind = "class"
	TargetCharacter TargetKind = "character"
	TargetWorkspace TargetKind = "workspace"
	TargetTable     TargetKind = "table"
)

// Target is the class, character, workspace or table a decree is about.
type Target struct {
	Kind TargetKind `json:"kind"`
	Name string     `json:"name"`
}

// Origin is where the order came from.
type Origin struct {
	Channel    string `json:"channel,omitempty"` // telegram, panel, approval
	MessageID  int64  `json:"message_id,omitempty"`
	Audio      bool   `json:"audio,omitempty"`
	Attachment string `json:"attachment,omitempty"`
	Transcript string `json:"transcript,omitempty"`
	// OrderMsg is the order as a message, when the engine worded it (a suggestion
	// of the summary, the answer to a request, a renewal) and not the user: each
	// reader translates it, and Text holds it in English. Zero for what a user wrote.
	OrderMsg i18n.Msg `json:"order_msg,omitzero"`
}

// TextIn is the order in a language: the message of the engine translated, or
// the text as it was kept (what a user wrote, or an order kept before the message).
func (d Decree) TextIn(lang string) string {
	if d.Origin.OrderMsg.IsZero() {
		return d.Text
	}
	return i18n.Render(lang, d.Origin.OrderMsg)
}

// Proposal is what the Grand Master sends to the engine after it read the
// order of a user (`mestre decree propose`).
type Proposal struct {
	// Author is the user who gave the order.
	Author string `json:"author"`
	Kind   Kind   `json:"kind"`
	Effect Effect `json:"effect"`
	Target Target `json:"target"`
	// Workspace and Mesa are the scope; Scope is the rest of it
	// ("missao:<nome>", "classe:<nome>", "repo:<dono/nome>").
	Workspace string `json:"workspace"`
	Mesa      string `json:"mesa,omitempty"`
	Scope     string `json:"scope,omitempty"`
	// Until is the absolute end of the validity; zero is forever.
	Until time.Time `json:"until,omitzero"`
	// Text is the message of the user as it came.
	Text   string `json:"text"`
	Origin Origin `json:"origin"`
	// Action is the exact action an exception frees, TargetRef the state of
	// what it is about (the PR and its head) and RequestType the type of
	// request it answers; the impression is made from the TargetRef.
	Action      *classes.Action `json:"action,omitempty"`
	TargetRef   json.RawMessage `json:"target_ref,omitempty"`
	RequestType string          `json:"request_type,omitempty"`
}

// Pending is a proposal that waits for the click of the author.
type Pending struct {
	ID string
	// Text is what the confirmation says; PIN says the author must give the
	// PIN to confirm, and Why says why.
	Text string
	// Msg is the confirmation as a message, said in the language of the author, and
	// WhyMsg the reason of the PIN (Text and Why are their pt-BR form).
	Msg, WhyMsg i18n.Msg
	// Order is the text of the order of the user, as he wrote it (without the
	// blanks at the ends): shown with the proposal so he sees what was understood
	// next to what he said.
	Order string
	// OrderMsg is the order as a message when the engine worded it (Order is then its English text).
	OrderMsg i18n.Msg
	PIN      bool
	Why      string
	// Author is the only user whose click counts; Workspace and Mesa are the
	// scope that will be stored, and the one the message must name.
	Author          string
	Workspace, Mesa string
}

// Decree is a decree as it is kept.
type Decree struct {
	Number      int64
	Kind        Kind
	State       State
	Effect      Effect
	Target      Target
	Workspace   string
	Mesa        string
	Scope       string
	Author      string
	AuthorLevel classes.Level
	Text        string
	Origin      Origin
	PIN         bool
	Request     int64
	Action      *classes.Action
	RequestType string
	Impression  string
	TargetRef   json.RawMessage
	SuspendedBy string
	CreatedAt   time.Time
	Until       time.Time
	EndedAt     time.Time
}

// Change is a line of the history of states of a decree.
type Change struct {
	At    time.Time
	State State
	By    string
	Note  string
}

// Use is something done on the strength of a decree.
type Use struct {
	At        time.Time
	Character string
	Action    string
}

// View is a decree as /decretos lists it.
type View struct {
	Decree
	// AuthorName is the name of the author; Level the level that counts now.
	AuthorName string
	Level      classes.Level
	// OverriddenBy is the number of the decree of a higher level that takes
	// the same point, if one does.
	OverriddenBy int64
	// Frozen says the Mestre is locked (/travar): every decree is shown as
	// suspended while the lock lasts. The state kept in the database is not
	// touched, so the unlock brings back each decree as it was (the ones a
	// conflict suspended stay suspended).
	Frozen bool
}

// Summary is the one line of /decretos.
func (d Decree) Summary() string { return describeEffect(d.Effect, d.Target, d.Kind, d.Action) }

// SummaryMsg is Summary as a message, said in the language of who reads.
func (d Decree) SummaryMsg() i18n.Msg { return describeMsg(d.Effect, d.Target, d.Kind, d.Action) }

// Refusal is a proposal or an action the engine does not take, with the
// reason as a message (a code and its parameters) that whoever shows it
// translates (docs/plataforma/mensagens-por-codigo.md).
type Refusal struct {
	Code string
	Msg  i18n.Msg
}

// Error is the form of the refusal for a log: never translated.
func (r *Refusal) Error() string { return r.Msg.String() }

// refuse makes a refusal from the code of the kind, the code of the message
// and pairs of name and value for its parameters.
func refuse(code, msg string, kv ...any) *Refusal {
	return &Refusal{Code: code, Msg: i18n.M(msg, kv...)}
}

// refuseMsg is refuse with a message that is already made.
func refuseMsg(code string, m i18n.Msg) *Refusal { return &Refusal{Code: code, Msg: m} }

// levelMsg is the message of a refusal of level (users.Require), or a plain
// one for any other error.
func levelMsg(err error) i18n.Msg {
	var le *users.LevelError
	if errors.As(err, &le) {
		return le.Msg()
	}
	return i18n.M("decree.level_denied", "reason", err.Error())
}

// The codes of a refusal.
const (
	CodeLevel      = "level"
	CodeWorkspace  = "workspace"
	CodeInvalid    = "invalid"
	CodeInvariant  = "invariant"
	CodeBuiltIn    = "built_in"
	CodePolicy     = "policy"
	CodeDelegation = "delegation"
)

// The errors.
var (
	ErrNoDecree   = errors.New("decrees: no such decree")
	ErrNoProposal = errors.New("decrees: no such proposal, or it is over")
	ErrNotAuthor  = errors.New("decrees: only the author confirms the proposal")
	// ErrPINRequired: the proposal or the revocation needs the PIN of the
	// author; ask for it (BeginPIN) and call again with the request.
	ErrPINRequired = errors.New("decrees: this needs the PIN")
	// ErrPINFailed: the PIN was wrong or expired, and the proposal is cancelled.
	ErrPINFailed     = errors.New("decrees: the PIN was wrong or expired; the proposal is cancelled")
	ErrEnded         = errors.New("decrees: the decree is already over")
	ErrNotAllowed    = errors.New("decrees: the level does not revoke this decree")
	ErrTargetChanged = errors.New("decrees: the target changed since the approval; the exception expired unused")
	// ErrLocked: the Mestre is locked (/travar); no decree is confirmed or revoked.
	ErrLocked   = errors.New("decrees: the Mestre is locked")
	ErrNoVerify = errors.New("decrees: the target could not be verified")
	// ErrWrongRequest: the request offered is not the one the exception was
	// made for (another workspace, another target).
	ErrWrongRequest = errors.New("decrees: the request is not the one the exception was made for")
)
