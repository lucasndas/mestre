package chain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

func isPermission(t classes.RequestType) bool {
	return t == "" || t == classes.RequestClassPermission
}

// refused writes the refusal to the audit and returns it.
func (s *Service) refused(ctx context.Context, r Request, approver string, ans Answer, ref *Refusal) *Refusal {
	s.audit(ctx, r, AuditEntry{Approver: approver, Answer: ans, Outcome: "refused", Refusal: ref.Code, Reason: ref.Message})
	return ref
}

// refusedAndClimbed is a refusal for a guardrail: the request goes up at once.
func (s *Service) refusedAndClimbed(ctx context.Context, r Request, approver string, ans Answer, ref *Refusal) *Refusal {
	s.audit(ctx, r, AuditEntry{Approver: approver, Answer: ans, Outcome: "refused", Refusal: ref.Code, Reason: ref.Message})
	ref.Climbed = true
	s.climb(ctx, r.ID, r.Level, answerGuardrail, ref.Code+": "+ref.Message)
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
		return Outcome{}, refuse(CodeNoRequest, "no request #%d", d.Request)
	case err != nil:
		return Outcome{}, err
	case r.State != Open:
		return Outcome{}, refuse(CodeNoRequest, "the request #%d is already %s", r.ID, r.State)
	}
	if r.Level.Kind == LevelPeople || r.Level.Agent != d.By {
		return Outcome{}, s.refused(ctx, r, d.By, d.Answer, refuse(CodeNotAtLevel, "the request #%d is not at your level: it waits on %s", r.ID, r.Level.Label()))
	}
	if !d.Answer.Valid() {
		return Outcome{}, s.refused(ctx, r, d.By, d.Answer, refuse(CodeBadAnswer, "unknown answer %q", d.Answer))
	}
	if d.Answer == AnswerDelegate {
		return Outcome{}, s.refused(ctx, r, d.By, d.Answer, refuse(CodeHumanRequired, "delegating is for the people: the Owner or an Administrator"))
	}
	switch {
	case d.By == r.Requester:
		return Outcome{}, s.refusedAndClimbed(ctx, r, d.By, d.Answer, refuse(CodeSelfDecision, "nobody decides a request of its own"))
	case r.Designer != "" && d.By == r.Designer:
		return Outcome{}, s.refusedAndClimbed(ctx, r, d.By, d.Answer, refuse(CodeSelfDecision, "the designer of the mission does not decide its points of approval"))
	case r.Proposer != "" && d.By == r.Proposer:
		return Outcome{}, s.refusedAndClimbed(ctx, r, d.By, d.Answer, refuse(CodeSelfDecision, "nobody decides a request that serves a proposal of its own"))
	}
	instructed := d.Answer == AnswerDenyInstruction && strings.TrimSpace(d.Instruction) != ""
	if (d.Answer.Approves() || d.Answer.Denies()) && strings.TrimSpace(d.Reason) == "" && !instructed {
		return Outcome{}, s.refused(ctx, r, d.By, d.Answer, refuse(CodeReasonRequired, "an agent gives the reason of its decision"))
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
		for _, h := range p {
			if h.level.same(level) {
				if v := s.can(ctx, h, r, st); !v.ok {
					return Outcome{}, s.refusedAndClimbed(ctx, r, d.By, d.Answer, refuse(CodeAboveCeiling, "this level does not decide this request: %s", v.why))
				}
			}
		}
		if ref := s.attenuation(ctx, r, d.By); ref != nil {
			return Outcome{}, s.refusedAndClimbed(ctx, r, d.By, d.Answer, ref)
		}
	}
	applied, ref := s.effect(ctx, r, st, d.By, level, d.Answer, d.Pattern, d.For, d.UntilMissionEnd, true)
	if ref != nil {
		return Outcome{}, s.refused(ctx, r, d.By, d.Answer, ref)
	}
	c := Closing{State: Denied, By: d.By, Answer: d.Answer, Reason: d.Reason}
	if d.Answer.Approves() {
		c.State = Approved
	}
	if d.Answer == AnswerDenyInstruction && d.Instruction != "" {
		c.Reason = strings.TrimSpace(orDefault(d.Reason, "") + " " + d.Instruction)
	}
	r, ok := s.closeRequest(ctx, d.Request, &level, c)
	if !ok {
		return Outcome{}, refuse(CodeNoRequest, "the request #%d was decided meanwhile", d.Request)
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
		return refuse(CodeAttenuation, "the effective rules of the approver are not known: nothing is approved without them")
	}
	env, err := s.o.Classes.Env(ctx, r.Requester)
	if err != nil {
		return refuse(CodeAttenuation, "the environment of whoever asked is not known")
	}
	if ok, dec := classes.Attenuates(ctx, env, rules, *r.Event); !ok {
		return refuse(CodeAttenuation, "your own rules deny this action (%s): you do not approve what you could not do", dec.Reason)
	}
	return nil
}

// effect makes what an approval or a denial does besides closing the request.
// agent is true for the decision of an agent: an answer that only people give
// becomes a suggestion for the Owner.
func (s *Service) effect(ctx context.Context, r Request, st Settings, by string, level Level, ans Answer, pattern string, dur time.Duration, untilEnd, agent bool) (string, *Refusal) {
	switch ans {
	case AnswerApprove, AnswerApproveAlways:
		applied := "approved"
		if isPermission(r.Type) && r.Action != nil {
			if s.o.Grants == nil {
				return "", refuse(CodeUnavailable, "this engine does not make exceptions yet")
			}
			if err := s.o.Grants.Exception(ctx, ExceptionGrant{Request: r.ID, Character: r.Requester, Action: *r.Action, By: by, Level: level}); err != nil {
				return "", refuse(CodeUnavailable, "the exception was not made: %v", err)
			}
			applied = "exception"
		}
		if ans == AnswerApproveAlways {
			if agent {
				s.suggest(ctx, r, ans, by)
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
			return "", refuse(CodeBadAnswer, "approve_pattern needs --pattern")
		}
		if r.Action == nil {
			return "", refuse(CodeBadAnswer, "a pattern is for a request of an action")
		}
		ap, err := classes.CheckApprovablePattern(pattern, *r.Action, r.Class, r.Table)
		switch {
		case errors.Is(err, classes.ErrPatternTooBroad):
			return "", refuse(CodePatternTooBroad, "%v", err)
		case err != nil:
			return "", refuse(CodePatternNoMatch, "%v", err)
		}
		return s.temp(ctx, r, st, by, level, &ap, dur, untilEnd, agent)
	case AnswerDenyAlways:
		if agent {
			s.suggest(ctx, r, ans, by)
			return "denied+suggestion", nil
		}
		return "denied", s.decree(ctx, r, by, level, ans, pattern, dur)
	case AnswerDelegate:
		return "delegation", s.decree(ctx, r, by, level, ans, pattern, dur)
	}
	return "denied", nil
}

// temp makes the temporary table rule of an agent's approval for a time or a
// pattern: baixo only, inside the rule's longest validity; a person's
// approval is a decree.
func (s *Service) temp(ctx context.Context, r Request, st Settings, by string, level Level, ap *classes.ApprovablePattern, dur time.Duration, untilEnd, agent bool) (string, *Refusal) {
	if !agent {
		return "decree", s.decree(ctx, r, by, level, AnswerApproveFor, patternOf(ap), dur)
	}
	if !isPermission(r.Type) || r.Action == nil {
		return "", refuse(CodeBadAnswer, "a temporary rule is for a request of an action")
	}
	if r.Risk.Risk != classes.RiskLow {
		return "", refuse(CodeAboveCeiling, "a temporary rule of an agent covers only baixo; this request is %s", r.Risk.Risk)
	}
	if ap != nil && ap.Risk.Risk != classes.RiskLow {
		return "", refuse(CodeAboveCeiling, "the pattern reaches risk %s (%s), above the ceiling of an agent (baixo)", ap.Risk.Risk, ap.Risk.Rule)
	}
	if ap == nil && dur <= 0 && !untilEnd {
		return "", refuse(CodeBadAnswer, "approve_for needs --for <duration> or --until-mission-end")
	}
	if dur > st.TempRuleMax {
		return "", refuse(CodeDurationTooLong, "a temporary rule goes up to %s", st.TempRuleMax)
	}
	if s.o.TempRules == nil {
		return "", refuse(CodeUnavailable, "temporary table rules are not available in this engine")
	}
	rule := TempRule{Request: r.ID, Workspace: r.Workspace, Table: r.Table, Class: r.Class, Pattern: ap, Action: *r.Action, UntilMissionEnd: untilEnd, By: by, Level: level}
	if dur > 0 {
		rule.Until = s.clk.Now().Add(dur)
	} else if ap != nil {
		rule.Until = s.clk.Now().Add(st.TempRuleMax)
	}
	if err := s.o.TempRules.Create(ctx, rule); err != nil {
		return "", refuse(CodeUnavailable, "the temporary rule was not made: %v", err)
	}
	return "temporary_rule", nil
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
		return refuse(CodeUnavailable, "this engine does not make decrees yet")
	}
	if err := s.o.Grants.Decree(ctx, DecreeGrant{Request: r.ID, Answer: ans, Character: r.Requester, Class: r.Class, Table: r.Table, Pattern: pattern, For: dur, By: by, Level: level}); err != nil {
		return refuse(CodeUnavailable, "the decree was not made: %v", err)
	}
	return nil
}

func (s *Service) suggest(ctx context.Context, r Request, ans Answer, by string) {
	x := Suggestion{Request: r.ID, At: s.clk.Now(), Kind: ans, By: by, Class: r.Class, Action: r.About}
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
		return Outcome{}, s.refused(ctx, r, by, AnswerAskBack, refuse(CodeBadAnswer, "nobody asked this request: there is no one to ask"))
	case question == "":
		return Outcome{}, s.refused(ctx, r, by, AnswerAskBack, refuse(CodeBadAnswer, "ask_back needs the question (--instruction)"))
	}
	st, _ := s.settings(ctx, r.Workspace)
	now := s.clk.Now()
	s.mu.Lock()
	cur, err := s.st.Get(ctx, r.ID)
	if err != nil || cur.State != Open || !cur.Level.same(level) {
		s.mu.Unlock()
		return Outcome{}, refuse(CodeNoRequest, "the request #%d changed meanwhile", r.ID)
	}
	cur.AskedBack, cur.AskedAt = true, now
	// While it waits for the requester the level's time stops; no answer at
	// all is bounded by the people's time, then the request goes up.
	cur.Deadline = now.Add(st.PeopleDeadline)
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
		r.ID, orDefault(r.About, string(orPerm(r.Type))), question, r.ID)
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
		return refuse(codeNothingToAnswer, "the request #%d asks nothing of you", request)
	case r.Requester != from:
		return refuse(CodeNotRequester, "only who asked answers the question")
	}
	st, _ := s.settings(ctx, r.Workspace)
	s.mu.Lock()
	cur, err := s.st.Get(ctx, request)
	if err != nil || cur.State != Open || !cur.AskedBack {
		s.mu.Unlock()
		return refuse(codeNothingToAnswer, "the request #%d asks nothing of you", request)
	}
	cur.AskedBack = false
	if cur.Level.Kind != LevelPeople {
		cur.Deadline = s.clk.Now().Add(levelDeadline(st, cur.Level.Kind))
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
			cur.Why = append(cur.Why, "resposta de quem pediu: "+text)
			s.o.Users.Ask(ctx, PeopleQuestion{Request: cur, Text: Text(cur, cur.Level, AnswersFor(LevelPeople, cur)) + "\n" + body, Deadline: cur.Deadline, Answers: AnswersFor(LevelPeople, cur)}) //nolint:errcheck // logged by the channel
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
// chain, and no ceiling nor attenuation holds.
func (s *Service) DecideByPerson(ctx context.Context, d PersonDecision) (Outcome, error) {
	r, err := s.st.Get(ctx, d.Request)
	switch {
	case errors.Is(err, ErrNoRequest):
		return Outcome{}, refuse(CodeNoRequest, "no request #%d", d.Request)
	case err != nil:
		return Outcome{}, err
	case r.State != Open:
		return Outcome{}, refuse(CodeNoRequest, "the request #%d is already %s", r.ID, r.State)
	}
	who := "user:" + d.User
	level := Level{Kind: LevelPeople, Name: d.User}
	switch d.Answer {
	case AnswerClimb:
		return Outcome{}, s.refused(ctx, r, who, d.Answer, refuse(CodeBadAnswer, "the people have no level above"))
	case AnswerAskBack:
		return s.askBack(ctx, r, who, r.Level, d.Instruction)
	}
	if !d.Answer.Valid() {
		return Outcome{}, s.refused(ctx, r, who, d.Answer, refuse(CodeBadAnswer, "unknown answer %q", d.Answer))
	}
	st, _ := s.settings(ctx, r.Workspace)
	applied, ref := s.effect(ctx, r, st, who, level, d.Answer, d.Pattern, d.For, false, false)
	if ref != nil {
		return Outcome{}, s.refused(ctx, r, who, d.Answer, ref)
	}
	c := Closing{State: Denied, By: who, Answer: d.Answer, Reason: strings.TrimSpace(d.Reason + " " + d.Instruction)}
	if d.Answer.Approves() || d.Answer == AnswerDelegate {
		c.State = Approved
	}
	r, ok := s.closeRequest(ctx, d.Request, nil, c)
	if !ok {
		return Outcome{}, refuse(CodeNoRequest, "the request #%d was decided meanwhile", d.Request)
	}
	c.Level = level
	s.audit(ctx, r, AuditEntry{Approver: who, Level: LevelPeople, Answer: d.Answer, Outcome: "accepted", Reason: c.Reason})
	s.settle(ctx, r, st, d.User, d.Instruction)
	return Outcome{Request: r.ID, State: r.State, Applied: applied}, nil
}

// closeRequest ends an open request, atomically: from, when set, is the level
// that must still hold it.
func (s *Service) closeRequest(ctx context.Context, id int64, from *Level, c Closing) (Request, bool) {
	s.mu.Lock()
	r, err := s.st.Get(ctx, id)
	if err != nil || r.State != Open || (from != nil && !r.Level.same(*from)) {
		s.mu.Unlock()
		return r, false
	}
	item, wasAsked := r.Item, r.Level.Kind == LevelPeople || r.AskedBack
	c.Level = r.Level
	r.State, r.ClosedAt, r.Deadline, r.Item, r.AskedBack, r.Decision = c.State, s.clk.Now(), time.Time{}, "", false, &c
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
	} else if s.o.Waits != nil && r.Requester != "" {
		s.o.Waits.ApprovalDecided(ctx, r.ID, text)
	}
	if r.Requester != "" {
		s.breaker(ctx, r, st)
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
	switch r.State {
	case Approved:
		return fmt.Sprintf("O pedido de aprovação #%d foi aprovado%s. Tente a ação de novo.", r.ID, by)
	case Expired:
		return fmt.Sprintf("O pedido de aprovação #%d expirou sem resposta e foi negado. Nada foi aprovado.", r.ID)
	case Cancelled:
		return fmt.Sprintf("O pedido de aprovação #%d foi cancelado.", r.ID)
	}
	text := fmt.Sprintf("O pedido de aprovação #%d foi negado%s.", r.ID, by)
	if r.Decision != nil && r.Decision.Reason != "" {
		text += " Motivo: " + r.Decision.Reason + "."
	}
	if instruction != "" {
		text += " Instrução: " + instruction
	}
	return text
}

// breaker counts the denials in a row of a character's requests: past the
// limit the character is paused (it gets nothing delivered) and the people are
// told; an approval in the middle clears the count.
func (s *Service) breaker(ctx context.Context, r Request, st Settings) {
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
	if err := s.st.SaveBreaker(ctx, Breaker{Character: characterID}); err != nil {
		return err
	}
	return s.o.Inbox.Pump(ctx, characterID)
}

// climb takes the request, still at level from, to the next level that can
// decide it. Climbing is not resending: the id, the action and the fingerprint
// stay; only the level, the time and the item change.
func (s *Service) climb(ctx context.Context, id int64, from Level, cause Answer, why string) {
	r, err := s.st.Get(ctx, id)
	if err != nil || r.State != Open || !r.Level.same(from) || r.Level.Kind == LevelPeople {
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
	if err != nil || cur.State != Open || !cur.Level.same(from) {
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
		if !r.Deadline.IsZero() {
			s.arm(r.ID, r.Level, r.Deadline)
		}
	}
	return nil
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
	if err != nil || r.State != Open || !r.Level.same(level) || ms(r.Deadline) != ms(at) {
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
