package chain

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/pkg/clock"
)

func isPermission(t classes.RequestType) bool {
	return t == "" || t == classes.RequestClassPermission
}

// refused writes the refusal to the audit and returns it.
func (s *Service) refused(ctx context.Context, r Request, approver string, ans Answer, ref *Refusal) *Refusal {
	s.audit(ctx, r, AuditEntry{Approver: approver, Answer: ans, Outcome: "refused", Refusal: ref.Code, Reason: ref.Text()})
	return ref
}

// refusedAndClimbed is a refusal for a guardrail: the request goes up at once.
func (s *Service) refusedAndClimbed(ctx context.Context, r Request, approver string, ans Answer, ref *Refusal) *Refusal {
	s.audit(ctx, r, AuditEntry{Approver: approver, Answer: ans, Outcome: "refused", Refusal: ref.Code, Reason: ref.Text()})
	ref.Climbed = true
	s.climb(ctx, r.ID, r.Level, answerGuardrail, ref.Code+": "+ref.Text())
	return ref
}

// Decide is the decision of an agent that holds the level the request is at
// (`mestre approval decide`, milestone 5). The engine checks the level, the
// guardrails and the ceiling before anything is decided; a decision that
// breaks one is refused, audited, and, for a guardrail, the request goes up
// at once (docs/orquestracao/cadeia-de-aprovacao.md#salvaguardas).
func (s *Service) Decide(ctx context.Context, d Decision) (Outcome, error) {
	r, err := s.st.Get(ctx, d.Request)
	switch {
	case errors.Is(err, ErrNoRequest):
		return Outcome{}, refuse(CodeNoRequest, "approval.no_request", "id", d.Request)
	case err != nil:
		return Outcome{}, err
	case r.State != Open:
		return Outcome{}, refuse(CodeNoRequest, "approval.already_closed", "id", r.ID, "state", r.State.Msg())
	}
	if r.Level.Kind == LevelPeople {
		// what waits on the people is not for an agent: the Grand Master was told
		// it only to know, and the people answer it
		return Outcome{}, s.refused(ctx, r, d.By, d.Answer, refuse(CodeHumanRequired, "approval.waits_on_people", "id", r.ID))
	}
	if r.Level.Agent != d.By {
		return Outcome{}, s.refused(ctx, r, d.By, d.Answer, refuse(CodeNotAtLevel, "approval.not_at_your_level", "id", r.ID, "level", r.Level.Msg()))
	}
	if !d.Answer.Valid() {
		return Outcome{}, s.refused(ctx, r, d.By, d.Answer, refuse(CodeBadAnswer, "approval.unknown_answer", "answer", string(d.Answer)))
	}
	if d.Answer == AnswerDelegate {
		return Outcome{}, s.refused(ctx, r, d.By, d.Answer, refuse(CodeHumanRequired, "approval.delegate_for_people"))
	}
	switch {
	case d.By == r.Requester:
		return Outcome{}, s.refusedAndClimbed(ctx, r, d.By, d.Answer, refuse(CodeSelfDecision, "approval.self_decision"))
	case r.Designer != "" && d.By == r.Designer:
		return Outcome{}, s.refusedAndClimbed(ctx, r, d.By, d.Answer, refuse(CodeSelfDecision, "approval.designer_decision"))
	case r.Proposer != "" && d.By == r.Proposer:
		return Outcome{}, s.refusedAndClimbed(ctx, r, d.By, d.Answer, refuse(CodeSelfDecision, "approval.proposer_decision"))
	}
	instructed := d.Answer == AnswerDenyInstruction && strings.TrimSpace(d.Instruction) != ""
	if (d.Answer.Approves() || d.Answer.Denies()) && strings.TrimSpace(d.Reason) == "" && !instructed {
		return Outcome{}, s.refused(ctx, r, d.By, d.Answer, refuse(CodeReasonRequired, "approval.reason_required"))
	}
	st, _ := s.settings(ctx, r.Workspace)
	appr, err := s.o.Characters.Get(ctx, d.By)
	if err != nil {
		return Outcome{}, err
	}
	level := r.Level
	switch d.Answer {
	case AnswerClimb:
		s.audit(ctx, r, AuditEntry{Approver: d.By, Answer: d.Answer, Outcome: "accepted", Reason: d.Reason})
		s.climb(ctx, r.ID, level, AnswerClimb, orDefault(d.Reason, "subiu a pedido do nível"))
		return s.outcome(ctx, r.ID, "climbed"), nil
	case AnswerAskBack:
		return s.askBack(ctx, r, d.By, level, d.Instruction)
	}
	if d.Answer.Approves() {
		// The ceiling and the conditions of the level, again: the policy or the
		// group may have changed since the request came.
		p, err := s.path(ctx, r)
		if err != nil {
			return Outcome{}, err
		}
		found := false
		for _, h := range p {
			if h.level.same(level) {
				found = true
				if v := s.can(ctx, h, r, st); !v.ok {
					return Outcome{}, s.refusedAndClimbed(ctx, r, d.By, d.Answer, refuse(CodeAboveCeiling, "approval.level_cannot_decide", "reason", v.why))
				}
			}
		}
		if !found {
			// The holder left the path (replaced, ended, moved): nobody
			// approves from a place that is no longer in the chain.
			ref := s.refused(ctx, r, d.By, d.Answer, refuse(CodeNotAtLevel, "approval.no_longer_in_chain", "id", r.ID))
			s.reroute(ctx, r, "quem decidia saiu da cadeia do pedido")
			ref.Climbed = true
			return Outcome{}, ref
		}
		if ref := s.attenuation(ctx, r, d.By); ref != nil {
			return Outcome{}, s.refusedAndClimbed(ctx, r, d.By, d.Answer, ref)
		}
		if level.Kind == LevelGrandMaster {
			// The conditions of the policy: a refusal here does not climb the
			// request; the Grão-Mestre takes it to the people (approval ask).
			if ref := s.policyGate(ctx, r); ref != nil {
				return Outcome{}, s.refused(ctx, r, d.By, d.Answer, ref)
			}
		}
	}
	c := Closing{State: Denied, By: d.By, Answer: d.Answer, Reason: d.Reason}
	if d.Answer.Approves() {
		c.State = Approved
	}
	if d.Answer == AnswerDenyInstruction && d.Instruction != "" {
		c.Reason = strings.TrimSpace(orDefault(d.Reason, "") + " " + d.Instruction)
	}
	// The request is claimed before anything is granted: a decision overtaken
	// by a climb or an expiry grants nothing.
	if _, ok := s.claim(ctx, d.Request, &level, c); !ok {
		return Outcome{}, refuse(CodeNoRequest, "approval.decided_meanwhile", "id", d.Request)
	}
	applied, ref := s.effect(ctx, r, st, d.By, level, d.Answer, d.Pattern, d.For, d.UntilMissionEnd, true, d.Reason)
	if ref != nil {
		s.unclaim(ctx, d.Request)
		return Outcome{}, s.refused(ctx, r, d.By, d.Answer, ref)
	}
	r, ok := s.closeRequest(ctx, d.Request, &level, c)
	if !ok {
		return Outcome{}, refuse(CodeNoRequest, "approval.decided_meanwhile", "id", d.Request)
	}
	if level.Kind == LevelGrandMaster && d.Answer.Approves() && r.Risk.Risk == classes.RiskHigh {
		if rec, ok := s.o.Delegations.(DelegationRecorder); ok {
			if err := rec.Used(ctx, r, d.By); err != nil {
				s.log.Warn("chain: the use of the delegation was not registered", "request", r.ID, "err", err)
			}
		}
	}
	if level.Kind == LevelMesa && d.Answer.Approves() && s.o.SubDelegations != nil && !isPermission(r.Type) {
		if err := s.o.SubDelegations.SubUsed(ctx, r, d.By); err != nil {
			s.log.Warn("chain: the use of the subdelegation was not registered", "request", r.ID, "err", err)
		}
	}
	s.audit(ctx, r, AuditEntry{Approver: d.By, Level: level.Kind, Answer: d.Answer, Outcome: "accepted", Reason: c.Reason})
	s.settle(ctx, r, st, appr.Name, d.Instruction)
	return Outcome{Request: r.ID, State: r.State, Applied: applied}, nil
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func (s *Service) outcome(ctx context.Context, id int64, applied string) Outcome {
	r, err := s.st.Get(ctx, id)
	if err != nil {
		return Outcome{Request: id, Applied: applied}
	}
	return Outcome{Request: id, State: r.State, Level: r.Level, Applied: applied}
}

// attenuation is the first guardrail of the decision of an agent: it approves
// only what its own effective rules would not deny, in the worktree of whoever
// asked. The rules of the orchestrators come from the built-in class
// orquestrador.
func (s *Service) attenuation(ctx context.Context, r Request, approver string) *Refusal {
	if !isPermission(r.Type) || r.Event == nil {
		return nil
	}
	rules, err := s.o.Classes.Rules(ctx, approver)
	if err != nil || rules == nil {
		return refuse(CodeAttenuation, "approval.approver_rules_unknown")
	}
	env, err := s.o.Classes.Env(ctx, r.Requester)
	if err != nil {
		return refuse(CodeAttenuation, "approval.requester_env_unknown")
	}
	if ok, dec := classes.Attenuates(ctx, env, rules, *r.Event); !ok {
		return refuse(CodeAttenuation, "approval.attenuation", "reason", dec.Reason)
	}
	return nil
}

// effect makes what an approval or a denial does besides closing the request.
// agent is true for the decision of an agent: an answer that only people give
// becomes a suggestion for the Owner.
func (s *Service) effect(ctx context.Context, r Request, st Settings, by string, level Level, ans Answer, pattern string, dur time.Duration, untilEnd, agent bool, reason string) (string, *Refusal) {
	if r.Type == classes.RequestGMAction && ans != AnswerApprove && !ans.Denies() || r.Type == classes.RequestGMAction && ans == AnswerDenyAlways {
		// a question of the Grão-Mestre makes no rule, no decree and no delegation
		return "", refuse(CodeBadAnswer, "approval.approve_or_deny_only")
	}
	switch ans {
	case AnswerApprove, AnswerApproveAlways:
		applied := "approved"
		if isPermission(r.Type) && r.Action != nil {
			if s.o.Grants == nil {
				return "", refuse(CodeUnavailable, "approval.no_exceptions_yet")
			}
			if err := s.o.Grants.Exception(ctx, ExceptionGrant{Request: r.ID, Character: r.Requester, Action: *r.Action, By: by, Level: level}); err != nil {
				return "", refuse(CodeUnavailable, "approval.exception_failed", "reason", err.Error())
			}
			applied = "exception"
		}
		if ans == AnswerApproveAlways {
			if agent {
				s.suggest(ctx, r, ans, by, reason)
				applied += "+suggestion"
			} else if ref := s.decree(ctx, r, by, level, ans, pattern, dur); ref != nil {
				return "", ref
			}
		}
		return applied, nil
	case AnswerApproveFor:
		return s.temp(ctx, r, st, by, level, nil, dur, untilEnd, agent)
	case AnswerApprovePattern:
		if strings.TrimSpace(pattern) == "" {
			return "", refuse(CodeBadAnswer, "approval.pattern_needs_flag")
		}
		if r.Action == nil {
			return "", refuse(CodeBadAnswer, "approval.pattern_for_action")
		}
		ap, err := classes.CheckApprovablePattern(pattern, *r.Action, r.Class, r.Table)
		switch {
		case errors.Is(err, classes.ErrPatternTooBroad):
			return "", refuse(CodePatternTooBroad, "approval.detail", "detail", err.Error())
		case err != nil:
			return "", refuse(CodePatternNoMatch, "approval.detail", "detail", err.Error())
		}
		return s.temp(ctx, r, st, by, level, &ap, dur, untilEnd, agent)
	case AnswerDenyAlways:
		if agent {
			s.suggest(ctx, r, ans, by, reason)
			return "denied+suggestion", nil
		}
		return "denied", s.decree(ctx, r, by, level, ans, pattern, dur)
	case AnswerDelegate:
		return "delegation", s.decree(ctx, r, by, level, ans, pattern, dur)
	}
	return "denied", nil
}

// temp makes the temporary table rule of an agent's approval for a time or a
// pattern: up to the risk the level decides (baixo for an agent superior, medio
// for the Mestre da mesa, whose groups must be in cadeia.mesa_decide, and for
// the Grão-Mestre), inside the longest validity of a rule; a person's approval
// is a decree (docs/orquestracao/regras-temporarias-de-mesa.md).
func (s *Service) temp(ctx context.Context, r Request, st Settings, by string, level Level, ap *classes.ApprovablePattern, dur time.Duration, untilEnd, agent bool) (string, *Refusal) {
	if !agent {
		return "decree", s.decree(ctx, r, by, level, AnswerApproveFor, patternOf(ap), dur)
	}
	if !isPermission(r.Type) || r.Action == nil {
		return "", refuse(CodeBadAnswer, "approval.temp_rule_for_action")
	}
	ceiling := classes.RiskLow
	if level.Kind == LevelMesa || level.Kind == LevelGrandMaster {
		ceiling = classes.RiskMedium
	}
	if !riskWithin(r.Risk.Risk, ceiling) {
		return "", refuse(CodeAboveCeiling, "approval.temp_rule_ceiling", "ceiling", ceiling, "risk", r.Risk.Risk)
	}
	if ap == nil {
		// The same text of a command does another thing at each use when the
		// engine does not read all of it: no rule of the exact action for it.
		var tool *providers.Tool
		if r.Event != nil {
			tool = r.Event.Tool
		}
		if why := classes.TableRuleBlocker(*r.Action, tool, r.Risk); why != "" {
			return "", refuse(CodeBadAnswer, "approval.temp_rule_blocked", "reason", why)
		}
	}
	if ap != nil {
		if !riskWithin(ap.Risk.Risk, ceiling) {
			return "", refuse(CodeAboveCeiling, "approval.pattern_above_ceiling", "risk", ap.Risk.Risk, "rule", ap.Risk.Rule, "ceiling", ceiling)
		}
		if ap.Risk.Risk == classes.RiskMedium && level.Kind == LevelMesa {
			if why := s.mesaPatternCan(ctx, r, st, ap); why != "" {
				return "", refuse(CodeAboveCeiling, "approval.detail", "detail", why)
			}
		}
	}
	if ap == nil && dur <= 0 && !untilEnd {
		return "", refuse(CodeBadAnswer, "approval.approve_for_needs_duration")
	}
	if dur > st.TempRuleMax {
		return "", refuse(CodeDurationTooLong, "approval.temp_rule_max", "max", st.TempRuleMax.String())
	}
	if s.o.TempRules == nil {
		return "", refuse(CodeUnavailable, "approval.temp_rules_unavailable")
	}
	now := s.clk.Now()
	rule := TempRule{Request: r.ID, Workspace: r.Workspace, Table: r.Table, Class: r.Class, Requester: r.Requester, Pattern: ap, Action: *r.Action,
		Risk: ceilingOfRule(r, ap), UntilMissionEnd: untilEnd, By: by, Level: level, Until: now.Add(st.TempRuleMax)}
	if dur > 0 {
		rule.Until = now.Add(dur)
	}
	if err := s.o.TempRules.Create(ctx, rule); err != nil {
		if errors.Is(err, ErrNoMission) {
			return "", refuse(CodeBadAnswer, "approval.no_mission_to_end")
		}
		return "", refuse(CodeUnavailable, "approval.temp_rule_failed", "reason", err.Error())
	}
	return "temporary_rule", nil
}

// ceilingOfRule is the risk a rule covers: the one of the pattern, or of the
// exact action.
func ceilingOfRule(r Request, ap *classes.ApprovablePattern) classes.Risk {
	if ap != nil {
		return ap.Risk.Risk
	}
	return r.Risk.Risk
}

// riskWithin says whether a risk fits under a ceiling that is baixo or medio.
func riskWithin(r, ceiling classes.Risk) bool {
	switch r {
	case classes.RiskLow:
		return true
	case classes.RiskMedium:
		return ceiling == classes.RiskMedium
	}
	return false
}

// mesaPatternCan says why the Mestre da mesa may not make a rule of a medio
// pattern ("" when it may): the autonomy of the table is not paused and every
// group the pattern reaches is one it decides.
func (s *Service) mesaPatternCan(ctx context.Context, r Request, st Settings, ap *classes.ApprovablePattern) string {
	if s.o.Supervision != nil {
		if paused, err := s.o.Supervision.AutonomyPaused(ctx, r.Workspace, r.Table); err != nil || paused {
			return "a autonomia da mesa está em pausa: o Mestre da mesa não cria regra de medio"
		}
	}
	if len(ap.Risk.Groups) == 0 {
		return "o motor não deu o grupo do padrão: o Mestre da mesa não cria regra dele"
	}
	for _, g := range ap.Risk.Groups {
		switch {
		case g == classes.GroupNone:
			return "o padrão alcança algo sem grupo (o motor não o analisa): o Mestre da mesa não cria regra dele"
		case !st.MesaDecide(g):
			return fmt.Sprintf("o padrão alcança o grupo %s, que não está em cadeia.mesa_decide", g)
		}
	}
	return ""
}

func patternOf(ap *classes.ApprovablePattern) string {
	if ap == nil {
		return ""
	}
	return ap.Pattern
}

// decree asks the flow of the people (milestone 5) for the decree of an answer
// that only people give.
func (s *Service) decree(ctx context.Context, r Request, by string, level Level, ans Answer, pattern string, dur time.Duration) *Refusal {
	if s.o.Grants == nil {
		return refuse(CodeUnavailable, "approval.no_decrees_yet")
	}
	if err := s.o.Grants.Decree(ctx, DecreeGrant{Request: r.ID, Answer: ans, Character: r.Requester, Class: r.Class, Table: r.Table, Pattern: pattern, For: dur, By: by, Level: level}); err != nil {
		return refuse(CodeUnavailable, "approval.decree_failed", "reason", err.Error())
	}
	return nil
}

func (s *Service) suggest(ctx context.Context, r Request, ans Answer, by, reason string) {
	x := Suggestion{Request: r.ID, At: s.clk.Now(), Kind: ans, By: by, Class: r.Class, Action: r.About, Reason: strings.TrimSpace(reason)}
	if r.Event != nil && r.Event.Tool != nil {
		x.Action = orDefault(r.Event.Tool.Command, orDefault(r.Event.Tool.URL, r.Event.Tool.NativeName))
	}
	if err := s.st.AddSuggestion(ctx, x); err != nil {
		s.log.Warn("chain: the suggestion was not kept", "request", r.ID, "err", err)
	}
}

// askBack is the answer "perguntar de volta": the requester gets an item with
// the question, the request waits and the time of the level does not run until
// the answer comes; at the people the final time keeps running.
func (s *Service) askBack(ctx context.Context, r Request, by string, level Level, question string) (Outcome, error) {
	question = strings.TrimSpace(question)
	switch {
	case r.Requester == "":
		return Outcome{}, s.refused(ctx, r, by, AnswerAskBack, refuse(CodeBadAnswer, "approval.nobody_asked"))
	case question == "":
		return Outcome{}, s.refused(ctx, r, by, AnswerAskBack, refuse(CodeBadAnswer, "approval.ask_back_needs_text"))
	}
	st, _ := s.settings(ctx, r.Workspace)
	now := s.clk.Now()
	s.mu.Lock()
	cur, err := s.st.Get(ctx, r.ID)
	if err != nil || cur.State != Open || cur.Deciding != nil || !cur.Level.same(level) {
		s.mu.Unlock()
		return Outcome{}, refuse(CodeNoRequest, "approval.changed_meanwhile", "id", r.ID)
	}
	cur.AskedBack, cur.AskedAt = true, now
	// While it waits for the requester the level's time stops; no answer at
	// all is bounded by the people's time, then the request goes up.
	cur.Deadline = now.Add(st.peopleDeadline(cur.Type))
	if level.Kind == LevelPeople {
		cur.Deadline = r.Deadline
	}
	err = s.st.Save(ctx, cur)
	s.mu.Unlock()
	if err != nil {
		return Outcome{}, err
	}
	s.arm(r.ID, level, cur.Deadline)
	text := fmt.Sprintf("Quem decide o seu pedido de aprovação #%d (%s) pede contexto antes de decidir:\n%s\n\nResponda com `mestre approval reply %d \"<texto>\"`; o pedido espera a sua resposta.",
		r.ID, oneLine(orDefault(r.About, string(orPerm(r.Type)))), Unverified(question), r.ID)
	if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: r.Workspace, Target: r.Requester, Kind: inbox.KindAnswer, Sender: "Motor", Text: text,
		Refs: inbox.Refs{Approval: r.ID, Table: r.Table}}); err != nil {
		s.log.Warn("chain: the question did not reach the requester", "request", r.ID, "err", err)
	}
	s.audit(ctx, r, AuditEntry{Approver: by, Level: level.Kind, Answer: AnswerAskBack, Outcome: "accepted", Reason: question})
	return Outcome{Request: r.ID, State: Open, Level: level, Applied: "asked_back"}, nil
}

// Reply is the answer of the requester to a question of the level: free text,
// marked as not verified, back to the same level, whose time starts again (the
// final time of the people keeps running).
func (s *Service) Reply(ctx context.Context, request int64, from, text string) error {
	r, err := s.st.Get(ctx, request)
	switch {
	case err != nil:
		return err
	case r.State != Open || !r.AskedBack:
		return refuse(codeNothingToAnswer, "approval.asks_nothing", "id", request)
	case r.Requester != from:
		return refuse(CodeNotRequester, "approval.only_requester_answers")
	}
	st, _ := s.settings(ctx, r.Workspace)
	s.mu.Lock()
	cur, err := s.st.Get(ctx, request)
	if err != nil || cur.State != Open || !cur.AskedBack {
		s.mu.Unlock()
		return refuse(codeNothingToAnswer, "approval.asks_nothing", "id", request)
	}
	cur.AskedBack = false
	if cur.Level.Kind != LevelPeople {
		cur.Deadline = s.clk.Now().Add(levelDeadline(st, cur.Level.Kind, cur.Type))
	}
	err = s.st.Save(ctx, cur)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.arm(cur.ID, cur.Level, cur.Deadline)
	body := fmt.Sprintf("Resposta de %s à sua pergunta sobre o pedido de aprovação #%d:\n%s\n\nO pedido segue com você.", orDefault(r.RequesterName, r.Requester), r.ID, Unverified(text))
	if cur.Level.Kind == LevelPeople {
		if s.o.Users != nil {
			s.o.Users.Ask(ctx, s.question(cur, Text(cur, cur.Level, AnswersFor(LevelPeople, cur))+"\n"+body, AnswersFor(LevelPeople, cur))) //nolint:errcheck // logged by the channel
		}
	} else if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: r.Workspace, Target: cur.Level.Agent, Kind: inbox.KindAnswer, Sender: "Motor", Text: body,
		Refs: inbox.Refs{Approval: r.ID, Table: r.Table}}); err != nil {
		s.log.Warn("chain: the reply did not reach the level", "request", r.ID, "err", err)
	}
	s.audit(ctx, cur, AuditEntry{Approver: from, Answer: answerReply, Outcome: "accepted"})
	return nil
}

// DecideByPerson is the decision of a person through the flow of approval
// (milestone 5, which checks the level of the user, the PIN and the quorum):
// the people are above every level, so the request may be anywhere in the
// chain, and no ceiling nor attenuation holds. It trusts the caller for who
// the user is, the quorum, the PIN, the target and the time: its only caller is
// the flow of approval (internal/approvals), which checks them; a new caller
// must check them too.
func (s *Service) DecideByPerson(ctx context.Context, d PersonDecision) (Outcome, error) {
	r, err := s.st.Get(ctx, d.Request)
	switch {
	case errors.Is(err, ErrNoRequest):
		return Outcome{}, refuse(CodeNoRequest, "approval.no_request", "id", d.Request)
	case err != nil:
		return Outcome{}, err
	case r.State != Open:
		return Outcome{}, refuse(CodeNoRequest, "approval.already_closed", "id", r.ID, "state", r.State.Msg())
	}
	who := "user:" + d.User
	level := Level{Kind: LevelPeople, Name: d.User}
	// The level of the user, against the minimum of the policy for the type and
	// the risk: below it the user answers nothing (an Approver does not approve
	// an alto, nor deny it for good). The quorum is counted by the flow.
	rules, _ := s.rulesOf(r.Workspace)
	if _, _, min := rules.Need(r); !peopleLevel(d.Level).AtLeast(min) {
		return Outcome{}, s.refused(ctx, r, who, d.Answer, refuse(CodeHumanRequired, "approval.below_minimum", "min", min))
	}
	if (IsSystem(r.Type) || r.Type == classes.RequestGMAction) && !slices.Contains(AnswersFor(LevelPeople, r), d.Answer) {
		return Outcome{}, s.refused(ctx, r, who, d.Answer, refuse(CodeBadAnswer, "approval.engine_request_binary"))
	}
	switch d.Answer {
	case AnswerClimb:
		return Outcome{}, s.refused(ctx, r, who, d.Answer, refuse(CodeBadAnswer, "approval.no_level_above"))
	case AnswerAskBack:
		return s.askBack(ctx, r, who, r.Level, d.Instruction)
	}
	if !d.Answer.Valid() {
		return Outcome{}, s.refused(ctx, r, who, d.Answer, refuse(CodeBadAnswer, "approval.unknown_answer", "answer", string(d.Answer)))
	}
	st, _ := s.settings(ctx, r.Workspace)
	c := Closing{State: Denied, By: who, Answer: d.Answer, Reason: strings.TrimSpace(d.Reason + " " + d.Instruction)}
	if d.Answer.Approves() || d.Answer == AnswerDelegate {
		c.State = Approved
	}
	if _, ok := s.claim(ctx, d.Request, nil, c); !ok {
		return Outcome{}, refuse(CodeNoRequest, "approval.decided_meanwhile", "id", d.Request)
	}
	applied, ref := s.effect(ctx, r, st, who, level, d.Answer, d.Pattern, d.For, false, false, d.Reason)
	if ref != nil {
		s.unclaim(ctx, d.Request)
		return Outcome{}, s.refused(ctx, r, who, d.Answer, ref)
	}
	r, ok := s.closeRequest(ctx, d.Request, nil, c)
	if !ok {
		return Outcome{}, refuse(CodeNoRequest, "approval.decided_meanwhile", "id", d.Request)
	}
	c.Level = level
	s.audit(ctx, r, AuditEntry{Approver: who, Level: LevelPeople, Answer: d.Answer, Outcome: "accepted", Reason: c.Reason})
	s.settle(ctx, r, st, d.User, d.Instruction)
	return Outcome{Request: r.ID, State: r.State, Applied: applied}, nil
}

// claim takes an open request for a decision, atomically, before the effect
// of the decision is made: from, when set, is the level that must still hold
// it. Only one decision claims it, and a climb, an expiry or a cancellation
// that comes meanwhile finds it claimed and leaves it alone. The request
// stays open, at its level, until closeRequest (or unclaim) ends the claim.
func (s *Service) claim(ctx context.Context, id int64, from *Level, c Closing) (Request, bool) {
	s.mu.Lock()
	r, err := s.st.Get(ctx, id)
	if err != nil || r.State != Open || r.Deciding != nil || (from != nil && !r.Level.same(*from)) {
		s.mu.Unlock()
		return r, false
	}
	c.Level = r.Level
	r.Deciding = &c
	err = s.st.Save(ctx, r)
	s.mu.Unlock()
	if err != nil {
		s.log.Warn("chain: the claim of a decision was not saved", "request", id, "err", err)
		return r, false
	}
	s.disarm(id)
	return r, true
}

// unclaim gives the request back at the same level when the effect of the
// decision was refused: its time starts again, the whole time of the level
// (docs/orquestracao/cadeia-de-aprovacao.md).
func (s *Service) unclaim(ctx context.Context, id int64) {
	var st Settings
	if r, err := s.st.Get(ctx, id); err == nil {
		st, _ = s.settings(ctx, r.Workspace)
	}
	s.mu.Lock()
	r, err := s.st.Get(ctx, id)
	if err != nil || r.State != Open || r.Deciding == nil {
		s.mu.Unlock()
		return
	}
	r.Deciding = nil
	if !r.AskedBack { // while it waits for the requester the time is the people's
		r.Deadline = s.clk.Now().Add(levelDeadline(st, r.Level.Kind, r.Type))
	}
	err = s.st.Save(ctx, r)
	s.mu.Unlock()
	if err != nil {
		s.log.Warn("chain: the claim of a decision was not given back", "request", id, "err", err)
		return
	}
	s.arm(id, r.Level, r.Deadline)
}

// claimMatches: an unclaimed request is closed only by an expiry or a
// cancellation; a claimed one only by the decision that claimed it.
func claimMatches(claim *Closing, c Closing) bool {
	if claim == nil {
		return c.State == Expired || c.State == Cancelled || c.State == Invalidated
	}
	return claim.State == c.State && claim.By == c.By && claim.Answer == c.Answer
}

// closeRequest ends an open request, atomically: from, when set, is the level
// that must still hold it. A request under a claim is closed only by the
// decision that holds it.
func (s *Service) closeRequest(ctx context.Context, id int64, from *Level, c Closing) (Request, bool) {
	s.mu.Lock()
	r, err := s.st.Get(ctx, id)
	if err != nil || r.State != Open || (from != nil && !r.Level.same(*from)) || !claimMatches(r.Deciding, c) {
		s.mu.Unlock()
		return r, false
	}
	item, wasAsked := r.Item, r.Level.Kind == LevelPeople || r.AskedBack
	c.Level = r.Level
	r.State, r.ClosedAt, r.Deadline, r.Item, r.AskedBack, r.Decision, r.Deciding = c.State, s.clk.Now(), time.Time{}, "", false, &c, nil
	err = s.st.Save(ctx, r)
	s.mu.Unlock()
	if err != nil {
		s.log.Warn("chain: the end of a request was not saved", "request", id, "err", err)
		return r, false
	}
	s.disarm(id)
	if item != "" {
		if err := s.o.Inbox.Cancel(ctx, item, inbox.Cancelled); err != nil {
			s.log.Debug("chain: the item of the level was not cancelled", "item", item, "err", err)
		}
	}
	if wasAsked && s.o.Users != nil {
		s.o.Users.Withdraw(ctx, id, string(c.State)) //nolint:errcheck // the channel logs it
	}
	return r, true
}

// settle does what follows the end of a request: the circuit breaker, the
// word to whoever asked (or to the owner of its type), the supervision.
func (s *Service) settle(ctx context.Context, r Request, st Settings, byName, instruction string) {
	approved := r.State == Approved
	text := s.resultText(r, byName, instruction)
	if sink := s.o.Sinks[r.Type]; sink != nil {
		by := ""
		if r.Decision != nil && !strings.HasPrefix(r.Decision.By, "user:") {
			by = r.Decision.By
		}
		reason := ""
		if r.Decision != nil {
			reason = r.Decision.Reason
		}
		if err := sink.Decided(ctx, Result{Request: r, Approved: approved, By: by, Reason: reason}); err != nil {
			s.log.Warn("chain: the owner of the request was not told", "request", r.ID, "type", r.Type, "err", err)
		}
	} else if r.Type == classes.RequestGMAction && r.Requester != "" {
		// the Grão-Mestre is not waiting in a command: the word comes to its inbox
		if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: r.Workspace, Target: r.Requester, Kind: inbox.KindApproval, Sender: inbox.SystemSender, Text: text,
			Refs: inbox.Refs{Approval: r.ID, Table: r.Table}}); err != nil {
			s.log.Warn("chain: the Grand Master was not told what the people answered", "request", r.ID, "err", err)
		}
	} else if s.o.Waits != nil && r.Requester != "" {
		s.o.Waits.ApprovalDecided(ctx, r.ID, text)
	}
	if r.Requester != "" {
		if r.Type != classes.RequestGMAction { // a question of the Grão-Mestre is no action that was denied
			s.breaker(ctx, r, st)
		}
		// The wait is over: the status says what the character does again.
		if err := s.o.Inbox.Pump(ctx, r.Requester); err != nil {
			s.log.Debug("chain: the delivery to the requester was not pumped", "request", r.ID, "err", err)
		}
	}
	if s.o.Supervision != nil && r.Decision != nil && r.Decision.By != "" && !strings.HasPrefix(r.Decision.By, "user:") {
		if err := s.o.Supervision.Decided(ctx, AuditEntry{Request: r.ID, At: s.clk.Now(), Approver: r.Decision.By, Level: r.Decision.Level.Kind, Answer: r.Decision.Answer,
			Outcome: "accepted", Risk: string(r.Risk.Risk), Reason: r.Decision.Reason}); err != nil {
			s.log.Debug("chain: the supervision was not told", "request", r.ID, "err", err)
		}
	}
}

// resultText is what whoever asked reads when its request ends.
func (s *Service) resultText(r Request, byName, instruction string) string {
	by := ""
	if byName != "" {
		by = " por " + byName
	}
	if r.Type == classes.RequestGMAction {
		switch r.State {
		case Approved:
			return fmt.Sprintf("As pessoas responderam SIM ao pedido #%d (ação do Grão-Mestre)%s. O motor não fez nada: aja pelos comandos de sempre.", r.ID, by)
		case Expired:
			return fmt.Sprintf("O pedido #%d (ação do Grão-Mestre) expirou sem resposta: considere negado.", r.ID)
		case Cancelled, Invalidated:
			return fmt.Sprintf("O pedido #%d (ação do Grão-Mestre) foi cancelado.", r.ID)
		}
		text := fmt.Sprintf("As pessoas responderam NÃO ao pedido #%d (ação do Grão-Mestre)%s.", r.ID, by)
		if r.Decision != nil && r.Decision.Reason != "" {
			text += "\n" + inbox.Say("Motivo:", r.Decision.Reason, "|")
			if inbox.Multiline(r.Decision.Reason) {
				text += "\n" + inbox.QuoteNote
			}
		}
		return text
	}
	switch r.State {
	case Approved:
		return fmt.Sprintf("O pedido de aprovação #%d foi aprovado%s. Tente a ação de novo.", r.ID, by)
	case Expired:
		return fmt.Sprintf("O pedido de aprovação #%d expirou sem resposta e foi negado. Nada foi aprovado.", r.ID)
	case Cancelled:
		return fmt.Sprintf("O pedido de aprovação #%d foi cancelado.", r.ID)
	case Invalidated:
		return fmt.Sprintf("O pedido de aprovação #%d foi invalidado: o alvo mudou ou quem aprovou perdeu o direito antes de a ação ser aplicada. Nada foi aprovado.", r.ID)
	}
	// who decided wrote the reason and the instruction: with more than one line they
	// come quoted, so they cannot imitate another line of the engine
	text := fmt.Sprintf("O pedido de aprovação #%d foi negado%s.", r.ID, by)
	quoted := false
	if r.Decision != nil && r.Decision.Reason != "" {
		text += "\n" + inbox.Say("Motivo:", r.Decision.Reason, "|")
		quoted = inbox.Multiline(r.Decision.Reason)
	}
	if instruction != "" {
		text += "\n" + inbox.Say("Instrução:", instruction, "|")
		quoted = quoted || inbox.Multiline(instruction)
	}
	if quoted {
		text += "\n" + inbox.QuoteNote
	}
	return text
}

// breaker counts the denials in a row of a character's requests: past the
// limit the character is paused (it gets nothing delivered) and the people are
// told; an approval in the middle clears the count.
func (s *Service) breaker(ctx context.Context, r Request, st Settings) {
	s.bmu.Lock()
	defer s.bmu.Unlock()
	switch r.State {
	case Approved:
		b, err := s.st.Breaker(ctx, r.Requester)
		if err == nil && b.Denials != 0 && !b.Paused {
			b.Denials = 0
			s.st.SaveBreaker(ctx, b) //nolint:errcheck // a counter
		}
	case Denied:
		b, err := s.st.Breaker(ctx, r.Requester)
		if err != nil {
			return
		}
		b.Denials++
		justPaused := !b.Paused && b.Denials >= st.DenialsToPause
		if justPaused {
			b.Paused, b.PausedAt = true, s.clk.Now()
		}
		if err := s.st.SaveBreaker(ctx, b); err != nil {
			s.log.Warn("chain: the breaker was not saved", "character", r.Requester, "err", err)
			return
		}
		if justPaused {
			s.audit(ctx, r, AuditEntry{Approver: r.Requester, Answer: answerPause, Outcome: "accepted", Reason: fmt.Sprintf("%d denials in a row", b.Denials)})
			if s.o.Alerts != nil {
				msg := fmt.Sprintf("%s foi pausado: %d pedidos de aprovação negados em seguida. Um Administrador ou acima o retoma.", orDefault(r.RequesterName, r.Requester), b.Denials)
				if err := s.o.Alerts.Notify(ctx, r.Workspace, msg); err != nil {
					s.log.Warn("chain: the people were not told of the pause", "character", r.Requester, "err", err)
				}
			}
		}
	}
}

// Held implements inbox.Holds: a character the breaker paused gets nothing.
func (s *Service) Held(ctx context.Context, characterID string) (bool, error) {
	b, err := s.st.Breaker(ctx, characterID)
	return b.Paused, err
}

// Resume lifts the pause of the circuit breaker (an Administrator or above).
func (s *Service) Resume(ctx context.Context, characterID string) error {
	s.bmu.Lock()
	err := s.st.SaveBreaker(ctx, Breaker{Character: characterID})
	s.bmu.Unlock()
	if err != nil {
		return err
	}
	return s.o.Inbox.Pump(ctx, characterID)
}

// climb takes the request, still at level from, to the next level that can
// decide it. Climbing is not resending: the id, the action and the fingerprint
// stay; only the level, the time and the item change.
func (s *Service) climb(ctx context.Context, id int64, from Level, cause Answer, why string) {
	r, err := s.st.Get(ctx, id)
	if err != nil || r.State != Open || r.Deciding != nil || !r.Level.same(from) || r.Level.Kind == LevelPeople {
		return
	}
	st, _ := s.settings(ctx, r.Workspace)
	next, nwhy, err := s.next(ctx, r, st)
	if err != nil {
		s.log.Warn("chain: the next level was not found", "request", id, "err", err)
		return
	}
	s.mu.Lock()
	cur, err := s.st.Get(ctx, id)
	if err != nil || cur.State != Open || cur.Deciding != nil || !cur.Level.same(from) {
		s.mu.Unlock()
		return
	}
	old := cur.Item
	cur.Level, cur.Item, cur.Deadline, cur.AskedBack = next, "", time.Time{}, false
	cur.Why = append(cur.Why, nwhy...)
	err = s.st.Save(ctx, cur)
	s.mu.Unlock()
	if err != nil {
		s.log.Warn("chain: the climb was not saved", "request", id, "err", err)
		return
	}
	s.disarm(id)
	if old != "" {
		if err := s.o.Inbox.Cancel(ctx, old, inbox.Cancelled); err != nil {
			s.log.Debug("chain: the item of the level below was not cancelled", "item", old, "err", err)
		}
	}
	if s.o.Users != nil && from.Kind == LevelPeople {
		s.o.Users.Withdraw(ctx, id, "climbed") //nolint:errcheck // the channel logs it
	}
	s.audit(ctx, cur, AuditEntry{Level: from.Kind, Answer: cause, Outcome: "climbed", Reason: why + " → " + string(next.Kind)})
	s.arrive(ctx, id, st)
}

// Reroute takes a request off a level that stopped being a place for it (its
// holder was replaced, ended or stopped) and puts it where it belongs now,
// with the time of the new level starting.
func (s *Service) reroute(ctx context.Context, r Request, why string) {
	s.climb(ctx, r.ID, r.Level, answerReroute, why)
}

// RerouteTable takes the open requests the Mestre da mesa of a table holds and
// no longer may decide to the level above, with the same id. The autonomy of
// the table was paused: the medio ones go up at once, the baixo ones stay
// (docs/orquestracao/supervisao-da-cadeia.md#pausa-de-autonomia).
func (s *Service) RerouteTable(ctx context.Context, workspace, table, why string) error {
	rs, err := s.st.OpenOfTable(ctx, workspace, table)
	if err != nil {
		return err
	}
	for _, r := range rs {
		if r.Level.Kind != LevelMesa {
			continue
		}
		st, _ := s.settings(ctx, r.Workspace)
		if ok, err := s.stillHere(ctx, r, st); err == nil && ok {
			continue
		}
		s.reroute(ctx, r, why)
	}
	return nil
}

// expire ends a request whose last level ran out of time: negated, never
// approved on its own.
func (s *Service) expire(ctx context.Context, id int64) {
	r, ok := s.closeRequest(ctx, id, nil, Closing{State: Expired, Reason: "o prazo acabou sem resposta"})
	if !ok {
		return
	}
	st, _ := s.settings(ctx, r.Workspace)
	s.audit(ctx, r, AuditEntry{Level: r.Level.Kind, Answer: answerTimeout, Outcome: "accepted", Reason: "expired at the last level"})
	s.settle(ctx, r, st, "", "")
}

// Cancel ends a request nobody needs anymore (the requester ended, the table
// was cancelled).
func (s *Service) cancel(ctx context.Context, r Request, why string) {
	r, ok := s.closeRequest(ctx, r.ID, nil, Closing{State: Cancelled, Reason: why})
	if !ok {
		return
	}
	st, _ := s.settings(ctx, r.Workspace)
	s.audit(ctx, r, AuditEntry{Level: r.Level.Kind, Answer: "cancel", Outcome: "accepted", Reason: why})
	s.settle(ctx, r, st, "", "")
}

// Invalidate ends an open request whose target changed, or whose approver lost
// the right, before the approval was applied: nothing is applied and whoever
// waited is told.
func (s *Service) Invalidate(ctx context.Context, id int64, why string) error {
	r, ok := s.closeRequest(ctx, id, nil, Closing{State: Invalidated, Reason: why})
	if !ok {
		return refuse(CodeNoRequest, "approval.not_open", "id", id)
	}
	st, _ := s.settings(ctx, r.Workspace)
	s.audit(ctx, r, AuditEntry{Level: r.Level.Kind, Answer: "invalidate", Outcome: "accepted", Reason: why})
	s.settle(ctx, r, st, "", "")
	return nil
}

// CancelTable cancels the open requests of a table that was cancelled.
func (s *Service) CancelTable(ctx context.Context, workspace, table string) error {
	rs, err := s.st.OpenOfTable(ctx, workspace, table)
	if err != nil {
		return err
	}
	for _, r := range rs {
		s.cancel(ctx, r, "a mesa foi cancelada")
	}
	return nil
}

// StateChanged implements characters.Observer. A requester that ended cancels
// its requests; a holder that ended or stopped (the Mestre da mesa stuck or
// restarting) sends its open requests up at once, and the next ones are born
// above it until it is back; a character that went to waiting says which level
// its request waits on.
func (s *Service) StateChanged(ctx context.Context, c characters.Character, _ characters.State) error {
	switch c.State {
	case characters.Stopped:
		own, err := s.st.OpenOf(ctx, c.ID)
		if err != nil {
			return err
		}
		for _, r := range own {
			s.cancel(ctx, r, c.Name+" terminou")
		}
		fallthrough
	case characters.Stopping, characters.Interrupted, characters.Starting, characters.Stuck:
		held, err := s.st.OpenAt(ctx, c.ID)
		if err != nil {
			return err
		}
		for _, r := range held {
			s.reroute(ctx, r, c.Name+" está "+string(c.State))
		}
	case characters.Waiting:
		own, err := s.st.OpenOf(ctx, c.ID)
		if err != nil {
			return err
		}
		for _, r := range own {
			if r.Type == classes.RequestClassPermission {
				s.relabel(ctx, r)
				break
			}
		}
	}
	return nil
}

// Done implements characters.Observer.
func (s *Service) Done(context.Context, characters.Character, string) error { return nil }

// TurnEnded implements characters.Observer.
func (s *Service) TurnEnded(context.Context, characters.Character, string) error { return nil }

// Recover brings back the timers of the open requests after a restart of the
// engine: a request whose time ran out while it was down goes up (or expires)
// now.
func (s *Service) Recover(ctx context.Context) error {
	rs, err := s.st.Open(ctx)
	if err != nil {
		return err
	}
	for _, r := range rs {
		if r.Deciding != nil {
			// The engine went down between the claim and the end of a
			// decision: the decision is finished as it was claimed. If the
			// exception was not made, who asked is told to try again and the
			// action is asked anew: nothing is granted without the claim.
			s.finish(ctx, r)
			continue
		}
		if !r.Deadline.IsZero() {
			s.arm(r.ID, r.Level, r.Deadline)
		}
	}
	return nil
}

// finish closes a claimed request whose decision was cut by a crash.
func (s *Service) finish(ctx context.Context, r Request) {
	c := *r.Deciding
	level := r.Level
	closed, ok := s.closeRequest(ctx, r.ID, &level, c)
	if !ok {
		return
	}
	st, _ := s.settings(ctx, closed.Workspace)
	s.audit(ctx, closed, AuditEntry{Approver: c.By, Level: level.Kind, Answer: c.Answer, Outcome: "accepted", Reason: orDefault(c.Reason, "decisão concluída após reinício")})
	s.settle(ctx, closed, st, "", "")
}

// arm sets the timer of the level of a request.
func (s *Service) arm(id int64, level Level, at time.Time) {
	s.disarm(id)
	if at.IsZero() {
		return
	}
	d := max(at.Sub(s.clk.Now()), 0)
	t := s.clk.AfterFunc(d, func() { s.fire(id, level, at) })
	s.tmu.Lock()
	s.timers[id] = t
	s.tmu.Unlock()
}

// StopTimers stops the timer of every level: the Citadel stopped acting (it
// lost the fence or is shutting down) and no request may climb or expire until
// Recover brings the timers back.
func (s *Service) StopTimers() {
	s.tmu.Lock()
	ts := s.timers
	s.timers = map[int64]clock.Timer{}
	s.tmu.Unlock()
	for _, t := range ts {
		t.Stop()
	}
}

func (s *Service) disarm(id int64) {
	s.tmu.Lock()
	t := s.timers[id]
	delete(s.timers, id)
	s.tmu.Unlock()
	if t != nil {
		t.Stop()
	}
}

// fire is the timer of a level: its time ran out.
func (s *Service) fire(id int64, level Level, at time.Time) {
	ctx := context.Background()
	r, err := s.st.Get(ctx, id)
	if err != nil || r.State != Open || r.Deciding != nil || !r.Level.same(level) || ms(r.Deadline) != ms(at) {
		return
	}
	if r.Level.Kind == LevelPeople {
		s.expire(ctx, id)
		return
	}
	why := "o prazo do nível acabou"
	if r.AskedBack {
		why = "quem pediu não respondeu à pergunta"
	}
	s.climb(ctx, id, level, answerTimeout, why)
}

// peopleLevel reads the level of a user as the policy names it (the registry of
// the users says `administrator`, the decrees say `admin`).
func peopleLevel(l string) classes.Level {
	if l == "administrator" {
		return classes.LevelAdmin
	}
	return classes.Level(l)
}

// RerouteWorkspace takes the open requests of a workspace that wait at a level
// that no longer may decide them to the level above: the Owner revoked every
// delegation, so what leaned on one goes up at once
// (docs/aprovacoes/delegacao.md#revogar-tudo).
func (s *Service) RerouteWorkspace(ctx context.Context, workspace, why string) error {
	rs, err := s.st.Open(ctx)
	if err != nil {
		return err
	}
	st, _ := s.settings(ctx, workspace)
	for _, r := range rs {
		if r.Workspace != workspace || r.Level.Kind == LevelPeople {
			continue
		}
		if ok, err := s.stillHere(ctx, r, st); err == nil && ok {
			continue
		}
		s.reroute(ctx, r, why)
	}
	return nil
}
