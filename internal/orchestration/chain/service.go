package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Delegations says whether a delegation of the Owner covers a request of risk
// alto for the Grão-Mestre (milestone 5: the decrees of delegation).
type Delegations interface {
	Covers(ctx context.Context, r Request) (bool, error)
}

// Options are the pieces of the Service. Store, Characters, Inbox, Classes
// and Policy are required; the rest may be nil: no wait is kept, the people
// are not asked (the request waits at their level until it expires), no
// exception or rule is made, nobody is told.
type Options struct {
	Store      *Store
	Clock      clock.Clock
	Characters Characters
	Inbox      Inbox
	Waits      Waits
	Classes    Classes
	Policy     Policy

	Users       UserAsker
	Grants      Grants
	TempRules   TempRules
	Delegations Delegations
	// PRs reads the PRs the conditions of the policy check; without it a
	// request of a PR is for the people (diff_incompleto).
	PRs         PRSource
	Supervision Supervision
	Alerts      Alerts
	Tasks       Tasks
	// Sinks tell the owner of a type of request what became of it.
	Sinks  map[classes.RequestType]Sink
	Logger *slog.Logger
}

// Service is the chain of approval of the Citadel
// (docs/orquestracao/cadeia-de-aprovacao.md). It is the hook.Approvals of the
// engine (the requests of permissao_de_classe), the quests.Approvals of the
// points of approval, the Observer that follows the state of the characters,
// and the inbox.Holds that keeps a character the circuit breaker paused from
// getting deliveries.
type Service struct {
	o   Options
	st  *Store
	clk clock.Clock
	log *slog.Logger

	mu     sync.Mutex // short critical sections on a request; never held over a call out
	tmu    sync.Mutex
	omu    sync.Mutex // FindOpen and Insert of a request, as one step
	bmu    sync.Mutex // the counter of the circuit breaker
	timers map[int64]clock.Timer
	pol    policyReader
}

var (
	_ hook.Approvals      = (*Service)(nil)
	_ quests.Approvals    = (*Service)(nil)
	_ characters.Observer = (*Service)(nil)
	_ inbox.Holds         = (*Service)(nil)
)

// NewService returns the Service.
func NewService(o Options) (*Service, error) {
	switch {
	case o.Store == nil || o.Store.DB == nil:
		return nil, errors.New("chain: the service needs the store")
	case o.Characters == nil, o.Inbox == nil, o.Classes == nil, o.Policy == nil:
		return nil, errors.New("chain: the service needs the characters, the inbox, the classes and the policy")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{o: o, st: o.Store, clk: o.Clock, log: o.Logger, timers: map[int64]clock.Timer{}}, nil
}

// Store gives access to the tables.
func (s *Service) Store() *Store { return s.st }

// Get returns a request.
func (s *Service) Get(ctx context.Context, id int64) (Request, error) { return s.st.Get(ctx, id) }

// Audit lists the audit of a request.
func (s *Service) Audit(ctx context.Context, id int64) ([]AuditEntry, error) {
	return s.st.AuditOf(ctx, id)
}

func (s *Service) settings(ctx context.Context, workspace string) (Settings, string) {
	return s.pol.read(ctx, s.o.Policy, s.log, workspace)
}

// ApprovalTimeout is the time limit of a wait for an approval that does not
// know the type of the request: the times of the agent, of the Grão-Mestre and
// the longest of the people (docs/orquestracao/esperas-e-caixa-de-entrada.md#tempos-limite).
func (s *Service) ApprovalTimeout(ctx context.Context, workspace string) time.Duration {
	st, _ := s.settings(ctx, workspace)
	people := st.PeopleDeadline
	for _, d := range st.PeopleDeadlines {
		people = max(people, d)
	}
	return st.AgentDeadline + st.GrandMasterDeadline + people + approvalWaitGrace
}

// pathTime is the time limit of the wait of a requester: the times of every
// level of the path of the request (an agent above the other counts again),
// and the grace.
func (s *Service) pathTime(ctx context.Context, r Request, st Settings) time.Duration {
	hops, err := s.path(ctx, r)
	if err != nil {
		return st.AgentDeadline + st.GrandMasterDeadline + st.peopleDeadline(r.Type) + approvalWaitGrace
	}
	total := approvalWaitGrace
	for _, h := range hops {
		total += levelDeadline(st, h.level.Kind, r.Type)
	}
	return total
}

// approvalWaitGrace is how long the wait of a requester outlives the last time
// of the chain, so that the closing of the request (the denial that tells the
// requester) comes before the expiry of the wait.
const approvalWaitGrace = time.Minute

// OpenRequest is a request to put in the chain.
type OpenRequest struct {
	// Type is the request type; "" is permissao_de_classe.
	Type      classes.RequestType
	Workspace string
	// Table is the table of the request; for a requester it is the one its
	// registry row says.
	Table string
	// Requester is the character that asked ("" for none).
	Requester string
	// Designer and Proposer decide nothing of the request.
	Designer, Proposer string
	// Cross says the request crosses tables.
	Cross bool

	Action *classes.Action
	Event  *providers.Event
	// About is the action and the rule that asked, in one line.
	About         string
	Justification string
	// Risk is the classification; zero is the risk of the type.
	Risk    classes.RiskResult
	Mission *Mission
	// PR is the PR of a request of type pr or merge.
	PR *PRRef
	// Task overrides the original task of the requester.
	Task string
	// Target is the exact target (JSON); the request is bound to its
	// impression.
	Target json.RawMessage
}

// Open implements hook.Approvals: the requests of permissao_de_classe the
// rules left in ask. Repeating the exact action while the request is open
// finds the same one.
func (s *Service) Open(ctx context.Context, req hook.ApprovalRequest) (int64, error) {
	ev := req.Event
	a := req.Action
	return s.Request(ctx, OpenRequest{Type: classes.RequestClassPermission, Table: req.TableID, Requester: req.AgentID, Action: &a, Event: &ev, About: req.About,
		Justification: description(ev), Risk: req.Risk})
}

// description is what the agent wrote about the call, when its tool takes one
// (the description of a Bash call): text of the agent, never verified.
func description(ev providers.Event) string {
	if ev.Tool == nil || len(ev.Tool.RawInput) == 0 {
		return ""
	}
	var in struct {
		Description string `json:"description"`
	}
	if json.Unmarshal(ev.Tool.RawInput, &in) != nil {
		return ""
	}
	return strings.TrimSpace(in.Description)
}

// OpenMissionPoint implements quests.Approvals: a ponto_de_missao, medio. The
// designer of the quest never decides it.
func (s *Service) OpenMissionPoint(ctx context.Context, p quests.MissionPoint) (int64, error) {
	return s.Request(ctx, OpenRequest{Type: classes.RequestMissionPoint, Workspace: p.Workspace, Table: p.Table, Requester: p.Character, Designer: p.Designer,
		About: fmt.Sprintf("ponto de aprovação (%s) da etapa %s da missão %s", p.When, p.StepName, p.QuestName), Task: p.Summary,
		Mission: &Mission{Quest: p.Quest, QuestName: p.QuestName, Step: p.Step, StepName: p.StepName, When: p.When, Summary: p.Summary}})
}

// Request puts a request in the chain at the first level that can decide it
// and returns its number.
func (s *Service) Request(ctx context.Context, o OpenRequest) (int64, error) {
	if o.Type == "" {
		o.Type = classes.RequestClassPermission
	}
	r := Request{Workspace: o.Workspace, Table: o.Table, Type: o.Type, State: Open, Requester: o.Requester, Designer: o.Designer, Proposer: o.Proposer,
		Cross: o.Cross, Action: o.Action, Event: o.Event, About: o.About, Justification: o.Justification, Risk: o.Risk, Mission: o.Mission, PR: o.PR, Task: o.Task,
		CreatedAt: s.clk.Now()}
	if len(o.Target) > 0 {
		imp, err := Impress(o.Target)
		if err != nil {
			return 0, fmt.Errorf("chain: the target of the request: %w", err)
		}
		r.Target, r.Impression = o.Target, imp
	}
	if r.Risk.Risk == "" {
		r.Risk = classes.ClassifyRisk(classes.Env{}, nil, classes.RiskRequest{Type: o.Type})
	}
	switch {
	case r.Risk.Risk == classes.RiskFixed:
		return 0, ErrFixed
	case r.Risk.Risk != classes.RiskLow && r.Risk.Risk != classes.RiskMedium && r.Risk.Risk != classes.RiskHigh:
		return 0, fmt.Errorf("chain: request of unknown risk %q", r.Risk.Risk)
	}
	var who characters.Character
	// FindOpen and Insert are one step for the same action: two asks at once
	// open one request.
	locked := false
	unlock := func() {
		if locked {
			locked = false
			s.omu.Unlock()
		}
	}
	defer unlock()
	if o.Requester != "" {
		c, err := s.o.Characters.Get(ctx, o.Requester)
		if err != nil {
			return 0, err
		}
		who = c
		r.Workspace, r.Table, r.Machine, r.Class, r.RequesterName = c.Workspace, c.Table, c.Machine, c.Class, c.Name
		if r.Task == "" && s.o.Tasks != nil {
			if t, err := s.o.Tasks.Task(ctx, c.ID); err == nil {
				r.Task = t
			}
		}
		if r.Type == classes.RequestClassPermission && r.Action != nil {
			dir := ""
			if env, err := s.o.Classes.Env(ctx, c.ID); err == nil {
				dir = env.Worktree
			}
			r.Fingerprint = classes.ClassPermissionFingerprint(c.ID, *r.Action, dir)
			s.omu.Lock()
			locked = true
			if found, ok, err := s.st.FindOpen(ctx, c.ID, r.Fingerprint); err != nil {
				return 0, err
			} else if ok {
				return found.ID, nil
			}
		}
	}
	if r.Workspace == "" {
		return 0, errors.New("chain: a request needs a workspace")
	}
	st, _ := s.settings(ctx, r.Workspace)
	lvl, why, err := s.initial(ctx, r, st)
	if err != nil {
		return 0, err
	}
	r.Level, r.Why = lvl, why
	r, err = s.st.Insert(ctx, r)
	unlock()
	if err != nil {
		return 0, err
	}
	s.audit(ctx, r, AuditEntry{Answer: answerBorn, Outcome: "accepted", Level: lvl.Kind, Reason: joinWhy(why)})
	s.arrive(ctx, r.ID, st)
	if r.Type == classes.RequestClassPermission && s.o.Waits != nil && who.ID != "" {
		if _, err := s.o.Waits.WaitApproval(ctx, who, r.ID, s.pathTime(ctx, r, st)); err != nil {
			s.log.Warn("chain: the wait of the requester was not made", "request", r.ID, "err", err)
		}
	}
	return r.ID, nil
}

// levelDeadline is the time of a level for a request of a type (the type
// matters only to the people; "" is any other).
func levelDeadline(st Settings, k LevelKind, t classes.RequestType) time.Duration {
	switch k {
	case LevelGrandMaster:
		return st.GrandMasterDeadline
	case LevelPeople:
		return st.peopleDeadline(t)
	}
	return st.AgentDeadline
}

// arrive puts the request in front of its level: the item of the inbox of the
// agent, or the question to the people; it starts the time of the level and
// tells the requester where the request waits.
func (s *Service) arrive(ctx context.Context, id int64, st Settings) {
	r, err := s.st.Get(ctx, id)
	if err != nil || r.State != Open {
		return
	}
	now := s.clk.Now()
	r.Deadline = now.Add(levelDeadline(st, r.Level.Kind, r.Type))
	r.AskedBack = false
	r.Item = ""
	answers := AnswersFor(r.Level.Kind, r)
	informed := false
	switch r.Level.Kind {
	case LevelPeople:
		if s.o.Users != nil {
			if err := s.o.Users.Ask(ctx, s.question(r, Text(r, r.Level, answers), answers)); err != nil {
				s.log.Warn("chain: the people were not asked", "request", r.ID, "err", err)
			}
		}
		informed = !r.Informed
	default:
		it, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: r.Workspace, Target: r.Level.Agent, Kind: inbox.KindApproval, Sender: "Motor",
			Text: Text(r, r.Level, answers), Refs: inbox.Refs{Approval: r.ID, Table: r.Table}})
		if err != nil {
			s.log.Warn("chain: the item of the approval was not queued, the time of the level decides", "request", r.ID, "level", r.Level.Kind, "err", err)
		} else {
			r.Item = it.ID
		}
	}
	if informed {
		r.Informed = true
	}
	s.mu.Lock()
	cur, err := s.st.Get(ctx, id)
	if err == nil && cur.State == Open && cur.Level.same(r.Level) {
		cur.Deadline, cur.AskedBack, cur.Item, cur.Informed = r.Deadline, false, r.Item, r.Informed
		err = s.st.Save(ctx, cur)
	}
	s.mu.Unlock()
	if err != nil {
		s.log.Warn("chain: the request was not saved", "request", id, "err", err)
		return
	}
	s.arm(r.ID, r.Level, r.Deadline)
	s.relabel(ctx, r)
	if informed {
		s.informGM(ctx, r)
	}
}

// question is what the people are asked: the text, the answers and what the
// policy requires of who answers.
func (s *Service) question(r Request, text string, answers []Answer) PeopleQuestion {
	rules, _ := s.rulesOf(r.Workspace)
	n, require, min := rules.Need(r)
	return PeopleQuestion{Request: r, Text: text, Deadline: r.Deadline, Answers: answers, Approvals: n, Require: require, MinLevel: min, PIN: rules.NeedsPIN(r)}
}

// informGM tells the Grão-Mestre about a request that went over him to the
// people: an informative event, nothing to decide.
func (s *Service) informGM(ctx context.Context, r Request) {
	gm, ok, err := s.o.Characters.GrandMaster(ctx, r.Workspace)
	if err != nil || !ok || gm.ID == r.Requester {
		return
	}
	text := fmt.Sprintf("Evento informativo: o pedido de aprovação #%d (tipo %s, risco %s) de %s foi às pessoas. Você não precisa decidir.\n%s",
		r.ID, orPerm(r.Type), r.Risk.Risk, orDash(r.RequesterName), describe(r))
	if len(r.Why) > 0 {
		text += "Por que subiu: " + joinWhy(r.Why) + "\n"
	}
	if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: r.Workspace, Target: gm.ID, Kind: inbox.KindSystem, Sender: "Motor", Text: text,
		Refs: inbox.Refs{Approval: r.ID, Table: r.Table}}); err != nil {
		s.log.Warn("chain: the Grand Master was not told", "request", r.ID, "err", err)
	}
}

// relabel tells the requester's status which level its request waits on:
// "aguardando aprovação do Mestre" and, after a climb, "do Grão-Mestre".
func (s *Service) relabel(ctx context.Context, r Request) {
	if r.Requester == "" || r.State != Open || r.Type != classes.RequestClassPermission {
		return
	}
	if err := s.o.Characters.Relabel(ctx, r.Requester, waitReason(r)); err != nil {
		s.log.Debug("chain: the status of the requester was not relabeled", "request", r.ID, "err", err)
	}
}

func waitReason(r Request) string { return fmt.Sprintf("approval #%d · %s", r.ID, r.Level.Label()) }

// WaitLabel is the status of a character that waits for a request of
// approval: "aguardando aprovação do Mestre", following the climb.
func (s *Service) WaitLabel(ctx context.Context, characterID string) (string, bool) {
	rs, err := s.st.OpenOf(ctx, characterID)
	if err != nil {
		return "", false
	}
	for _, r := range rs {
		if r.Type == classes.RequestClassPermission {
			return characters.Status{State: characters.Waiting, Reason: waitReason(r)}.Label(), true
		}
	}
	return "", false
}

// audit appends a row of the audit with the versions that hold now.
func (s *Service) audit(ctx context.Context, r Request, e AuditEntry) {
	_, pv := s.settings(ctx, r.Workspace)
	e.Request, e.At, e.Risk, e.RiskTableVersion, e.PolicyVersion = r.ID, s.clk.Now(), string(r.Risk.Risk), r.Risk.TableVersion, pv
	if e.Level == "" {
		e.Level = r.Level.Kind
	}
	if e.Approver != "" && e.RulesVersion == 0 && !strings.HasPrefix(e.Approver, "user:") {
		if rules, err := s.o.Classes.Rules(ctx, e.Approver); err == nil && rules != nil {
			e.RulesVersion = rules.Version
		}
	}
	if err := s.st.Audit(ctx, e); err != nil {
		s.log.Warn("chain: the audit was not written", "request", r.ID, "err", err)
	}
}
