package chain

import (
	"errors"
	"fmt"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
)

// LevelKind is a level of the chain
// (docs/orquestracao/cadeia-de-aprovacao.md#a-cadeia).
type LevelKind string

// The levels, lowest first.
const (
	// LevelAgent is a superior that is an agent and not a master: it decides
	// baixo.
	LevelAgent LevelKind = "agent"
	// LevelMesa is the Mestre da mesa.
	LevelMesa LevelKind = "mesa"
	// LevelGrandMaster is the Grão-Mestre, the supervisor.
	LevelGrandMaster LevelKind = "grand_master"
	// LevelPeople is the people, through the UserAsker.
	LevelPeople LevelKind = "people"
)

func (k LevelKind) rank() int {
	switch k {
	case LevelAgent:
		return 0
	case LevelMesa:
		return 1
	case LevelGrandMaster:
		return 2
	}
	return 3
}

// Level is a place in the chain: a kind and, for the three levels of agents,
// the character that holds it.
type Level struct {
	Kind  LevelKind `json:"kind"`
	Agent string    `json:"agent,omitempty"`
	// Name is how the status of the requester says it ("Mestre",
	// "Grão-Mestre", "das pessoas"); kept with the level for the audit.
	Name string `json:"name,omitempty"`
}

func (l Level) same(o Level) bool { return l.Kind == o.Kind && l.Agent == o.Agent }

// Label is the level as the wait of the requester says it: "aguardando
// aprovação <Label>" (docs/orquestracao/esperas-e-caixa-de-entrada.md#status-aguardando).
func (l Level) Label() string {
	switch l.Kind {
	case LevelMesa:
		return "do Mestre"
	case LevelGrandMaster:
		return "do Grão-Mestre"
	case LevelPeople:
		return "das pessoas"
	}
	if l.Name != "" {
		return "de " + l.Name
	}
	return "do superior"
}

// State is the state of a request.
type State string

// The states of a request.
const (
	Open      State = "open"
	Approved  State = "approved"
	Denied    State = "denied"
	Expired   State = "expired"
	Cancelled State = "cancelled"
)

// Closed says the request was decided or ended.
func (s State) Closed() bool { return s != Open }

// Answer is a reply to a request (docs/orquestracao/cadeia-de-aprovacao.md#respostas).
type Answer string

// The catalogue of answers.
const (
	AnswerApprove         Answer = "approve"          // aprovar agora
	AnswerApproveFor      Answer = "approve_for"      // aprovar por um tempo
	AnswerApprovePattern  Answer = "approve_pattern"  // aprovar o padrão
	AnswerApproveAlways   Answer = "approve_always"   // aprovar sempre para a classe
	AnswerDelegate        Answer = "delegate"         // delegar
	AnswerDeny            Answer = "deny"             // negar agora
	AnswerDenyInstruction Answer = "deny_instruction" // negar com instrução
	AnswerDenyAlways      Answer = "deny_always"      // negar sempre
	AnswerAskBack         Answer = "ask_back"         // perguntar de volta
	AnswerClimb           Answer = "climb"            // subir agora
	answerTimeout         Answer = "timeout"          // audit only: the time of the level ran out
	answerGuardrail       Answer = "guardrail"        // audit only: a guardrail lifted the request
	answerReroute         Answer = "reroute"          // audit only: the level stopped being available
	answerReply           Answer = "reply"            // audit only: the requester answered a question
	answerBorn            Answer = "born"             // audit only: the request was opened
	answerPause           Answer = "pause_character"  // audit only: the circuit breaker
)

// Approves says the answer lets the action through.
func (a Answer) Approves() bool {
	switch a {
	case AnswerApprove, AnswerApproveFor, AnswerApprovePattern, AnswerApproveAlways:
		return true
	}
	return false
}

// Denies says the answer refuses the action.
func (a Answer) Denies() bool {
	switch a {
	case AnswerDeny, AnswerDenyInstruction, AnswerDenyAlways:
		return true
	}
	return false
}

// Valid says whether a is in the catalogue.
func (a Answer) Valid() bool {
	return a.Approves() || a.Denies() || a == AnswerDelegate || a == AnswerAskBack || a == AnswerClimb
}

// Request is a request of approval as the chain keeps it.
type Request struct {
	ID        int64
	Workspace string
	// Table is the table of the requester ("" for a request with none).
	Table string
	Type  classes.RequestType
	State State
	// Requester is the character that asked ("" for none).
	Requester string
	// RequesterName is how the approver reads who asked.
	RequesterName string
	// Designer is the character that made the quest of a mission point, and
	// Proposer the one whose proposal the request serves (a class, the
	// AGENTS.md, a PR): neither decides it.
	Designer, Proposer string
	// Cross says the request crosses tables (another branch, another worktree,
	// the mirror, an issue or a PR of another table): the Mestre da mesa never
	// decides it.
	Cross bool

	// Action is the exact action of a permissao_de_classe; Event the hook
	// event it came from (the attenuation reads it).
	Action *classes.Action  `json:"action,omitempty"`
	Event  *providers.Event `json:"event,omitempty"`
	// Fingerprint binds the request to the exact action.
	Fingerprint string
	// About is what the request is about, in one line (the action and the
	// rule that asked).
	About string
	// Machine, Class and Task are what the approver sees of who asked.
	Machine, Class, Task string
	// Justification is what the requester wrote: shown last, as text of the
	// agent, never verified.
	Justification string

	Risk classes.RiskResult
	// Why is why the lower levels were skipped, in the words the approver and
	// the people read ("rede: exemplo.io não está nos domínios da classe").
	Why []string

	Level Level
	// Deadline is when the time of the level runs out (zero: the request waits
	// for the requester's answer to a question, or has no level).
	Deadline time.Time
	// Item is the item of the inbox of the level, while one is open.
	Item string
	// Informed says the Grão-Mestre was told the request went to the people.
	Informed bool
	// AskedBack says the level asked the requester for context and waits.
	AskedBack bool
	// AskedAt is when the question was asked.
	AskedAt time.Time
	// Mission is set for a ponto_de_missao.
	Mission *Mission `json:"mission,omitempty"`
	// Decision is how it closed.
	Decision *Closing `json:"decision,omitempty"`

	CreatedAt, ClosedAt time.Time
}

// Mission is what a ponto_de_missao carries.
type Mission struct {
	Quest, QuestName, Step, StepName string
	When, Summary                    string
}

// Closing is how a request ended.
type Closing struct {
	State  State  `json:"state"`
	By     string `json:"by,omitempty"`
	Level  Level  `json:"level"`
	Answer Answer `json:"answer,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Refusal is a decision the engine did not accept (a guardrail, a ceiling, a
// level that is not the request's): nothing is decided by it. Climbed says the
// request went up at once.
type Refusal struct {
	Code    string
	Message string
	Climbed bool
}

func (r *Refusal) Error() string { return fmt.Sprintf("%s: %s", r.Code, r.Message) }

// The refusal codes
// (docs/orquestracao/cadeia-de-aprovacao.md#salvaguardas, #respostas).
const (
	CodeNotAtLevel      = "not_at_level"
	CodeNoRequest       = "no_request"
	CodeAttenuation     = "attenuation"
	CodeSelfDecision    = "self_decision"
	CodeReasonRequired  = "reason_required"
	CodeAboveCeiling    = "above_ceiling"
	CodeHumanRequired   = "human_required"
	CodeBadAnswer       = "bad_answer"
	CodeUnavailable     = "unavailable"
	CodeDurationTooLong = "duration_too_long"
	CodePatternTooBroad = "pattern_too_broad"
	CodePatternNoMatch  = "pattern_no_match"
	CodeAskedBack       = "asked_back"
	CodeMesaCondition   = "mesa_condition"
	CodePaused          = "character_paused"
	CodeNotRequester    = "not_requester"
	codeNothingToAnswer = "nothing_to_answer"
)

// Errors of the service.
var (
	// ErrFixed: a risk fixo does not enter the chain.
	ErrFixed = errors.New("a request of risk fixo follows the rule of its type, not the chain")
	// ErrNoRequest: no such request.
	ErrNoRequest = errors.New("no such request of approval")
)

func refuse(code, format string, a ...any) *Refusal {
	return &Refusal{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Decision is a decision of a character that is an agent.
type Decision struct {
	Request int64
	// By is the character that decides.
	By     string
	Answer Answer
	// Reason is required of an agent.
	Reason string
	// Instruction is the text of deny_instruction ("use pnpm"); the question of
	// ask_back.
	Instruction string
	// Pattern is the pattern of approve_pattern.
	Pattern string
	// For is the validity of approve_for; UntilMissionEnd the other form.
	For             time.Duration
	UntilMissionEnd bool
}

// PersonDecision is the decision of a person, through the flow of approval
// (milestone 5): the level, the PIN and the quorum are conferred there.
type PersonDecision struct {
	Request int64
	// User is the id of the user and Level their level.
	User   string
	Level  string
	Answer Answer
	Reason string
	// Instruction as in Decision.
	Instruction string
	Pattern     string
	For         time.Duration
}

// Outcome is what a decision did.
type Outcome struct {
	Request int64
	// State is the state of the request now.
	State State
	// Level is where it is (when still open).
	Level Level
	// Applied is what became of the answer ("exception", "temporary_rule",
	// "suggestion", "asked_back", "climbed", "denied").
	Applied string
}
