package chain

import (
	"context"
	"errors"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Characters is the registry of the characters and the superior each has
// (characters.Service and characters.Registry).
type Characters interface {
	Get(ctx context.Context, id string) (characters.Character, error)
	// Superior is who decides the requests of a character, from who created it.
	Superior(ctx context.Context, id string) (characters.Superior, error)
	TableMaster(ctx context.Context, workspace, table string) (characters.Character, bool, error)
	GrandMaster(ctx context.Context, workspace string) (characters.Character, bool, error)
	// Relabel changes the reason of a waiting character without a transition,
	// so the status says which level its request waits on.
	Relabel(ctx context.Context, id string, reason string) error
}

// CharactersFrom joins the service and the registry of the characters.
func CharactersFrom(svc *characters.Service, reg characters.Registry, clk clock.Clock) Characters {
	return charactersAdapter{svc: svc, reg: reg, clk: clk}
}

type charactersAdapter struct {
	svc *characters.Service
	reg characters.Registry
	clk clock.Clock
}

func (a charactersAdapter) Get(ctx context.Context, id string) (characters.Character, error) {
	return a.reg.Get(ctx, id)
}

func (a charactersAdapter) Superior(ctx context.Context, id string) (characters.Superior, error) {
	return a.svc.Superior(ctx, id)
}

func (a charactersAdapter) TableMaster(ctx context.Context, ws, table string) (characters.Character, bool, error) {
	return a.reg.TableMaster(ctx, ws, table)
}

func (a charactersAdapter) GrandMaster(ctx context.Context, ws string) (characters.Character, bool, error) {
	return a.reg.GrandMaster(ctx, ws)
}

func (a charactersAdapter) Relabel(ctx context.Context, id, reason string) error {
	c, err := a.reg.Get(ctx, id)
	if err != nil || c.State != characters.Waiting {
		return err
	}
	_, _, err = a.reg.Transition(ctx, id, characters.Waiting, characters.Change{Reason: reason, At: a.clk.Now()})
	return err
}

// Inbox is the inbox of the characters.
type Inbox interface {
	Enqueue(ctx context.Context, n inbox.NewItem) (inbox.Item, error)
	Cancel(ctx context.Context, id string, to inbox.State) error
	Pump(ctx context.Context, target string) error
}

// Waits keeps the wait of the requester for the decision.
type Waits interface {
	WaitApproval(ctx context.Context, waiter characters.Character, request int64, timeout time.Duration) (waits.Wait, error)
	ApprovalDecided(ctx context.Context, request int64, text string)
}

// Classes gives what the chain asks of the classes and the rules of the
// characters: the effective rules of the one that decides, and, for the one
// that asked, the environment the attenuation runs in and the domains its
// class or the ceiling list.
type Classes interface {
	// Rules are the effective rules of a character (the built-in class
	// orquestrador for the orchestrators).
	Rules(ctx context.Context, characterID string) (*classes.Rules, error)
	// Env is the environment of a character: its worktree, home, system.
	Env(ctx context.Context, characterID string) (classes.Env, error)
	// Domains are the network.domains of the class of the character and of the
	// ceiling of its workspace (a domain in either counts).
	Domains(ctx context.Context, characterID string) ([]string, error)
}

// Policy gives the part of the approval policy file the chain reads
// (docs/aprovacoes/arquivo-de-politica.md, section cadeia; milestone 5 reads
// the file).
type Policy interface {
	// Chain is the section cadeia and Version the version (hash) of the file
	// that holds now.
	Chain(ctx context.Context, workspace string) (Settings, string, error)
}

// StaticPolicy is the default content of the file, the same for every
// workspace.
type StaticPolicy struct{ Settings Settings }

// Chain implements Policy.
func (p StaticPolicy) Chain(context.Context, string) (Settings, string, error) {
	s := p.Settings
	if s.AgentDeadline == 0 && s.GrandMasterDeadline == 0 {
		s = DefaultSettings()
	}
	return s, "default", nil
}

// UserAsker is the level of the people (milestone 5 has the Telegram side:
// the buttons, the quorum and the PIN). The answer comes back in
// Service.DecideByPerson.
type UserAsker interface {
	// Ask puts the request to the people; asking again for the same request
	// (a new level of people, a changed question) updates the question.
	Ask(ctx context.Context, q PeopleQuestion) error
	// Withdraw takes the question back: the request was decided elsewhere,
	// expired or was cancelled.
	Withdraw(ctx context.Context, request int64, why string) error
}

// PeopleQuestion is what the people see.
type PeopleQuestion struct {
	Request  Request
	Text     string
	Deadline time.Time
	// Answers are those the people may give.
	Answers []Answer
}

// Grants makes the effects of an approval or a denial.
type Grants interface {
	// Exception makes the one-time exception tied to the exact action, used up
	// at its first use (decree of kind exception, docs/arquitetura/precedencia.md).
	Exception(ctx context.Context, e ExceptionGrant) error
	// Decree makes what only people give: a permanent rule for the class, a
	// decree with validity, a delegation, a decree that denies (milestone 5).
	Decree(ctx context.Context, d DecreeGrant) error
}

// DecreeGrant is a decree made by the answer of a person.
type DecreeGrant struct {
	Request int64
	// Answer is approve_for, approve_pattern, approve_always, delegate or
	// deny_always.
	Answer    Answer
	Character string
	Class     string
	Table     string
	Pattern   string
	For       time.Duration
	By        string
	Level     Level
}

// ExceptionGrant is a one-time exception.
type ExceptionGrant struct {
	Request   int64
	Character string
	Action    classes.Action
	By        string
	Level     Level
}

// ErrNoMission is what TempRules.Create answers to a rule "until the mission
// ends" when the requester works on no mission.
var ErrNoMission = errors.New("chain: the requester has no mission")

// TempRules creates the temporary table rules
// (docs/orquestracao/regras-temporarias-de-mesa.md, stage 11).
type TempRules interface {
	Create(ctx context.Context, r TempRule) error
}

// TempRule is the rule an agent makes by approving for a time or a pattern.
type TempRule struct {
	Request   int64
	Workspace string
	Table     string
	Class     string
	// Requester is the character whose request it came from: the mission of
	// a rule "until the mission ends" is the one it is working on.
	Requester string
	// Risk is the highest risk the rule covers (baixo or medio).
	Risk classes.Risk
	// Pattern is the approved pattern (zero for an approval of the exact
	// action).
	Pattern *classes.ApprovablePattern
	Action  classes.Action
	// Until is the validity; UntilMissionEnd the other form.
	Until           time.Time
	UntilMissionEnd bool
	By              string
	Level           Level
}

// Sink is told what became of a request of a type with an owner (the quests
// for a ponto_de_missao, the scrolls for the deletion of one).
type Sink interface {
	Decided(ctx context.Context, r Result) error
}

// Result is the end of a request, as the owner of its type is told.
type Result struct {
	Request  Request
	Approved bool
	// By is the character that decided ("" for a person or the time).
	By     string
	Reason string
}

// Supervision is told about the decisions of the agents and tells whether the
// autonomy of a table is paused (docs/orquestracao/supervisao-da-cadeia.md,
// stage 12). Nil: nothing is paused and nobody is told.
type Supervision interface {
	AutonomyPaused(ctx context.Context, workspace, table string) (bool, error)
	Decided(ctx context.Context, d AuditEntry) error
}

// Alerts tells the people something the chain did on its own (a character
// paused by the circuit breaker).
type Alerts interface {
	Notify(ctx context.Context, workspace, text string) error
}

// Tasks gives the task a character is doing: what the approver reads as
// "tarefa original" (the text of the step or of the request it received).
type Tasks interface {
	Task(ctx context.Context, characterID string) (string, error)
}
