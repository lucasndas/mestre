package chain

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
)

// Handlers are the commands `approval decide|reply|list` to register in the local
// forwarder of the Citadel (docs/cli/grao-mestre.md#mestre-approval). Who calls is
// the character the engine identified; the level of the request, the
// guardrails and the ceilings are the ones of Decide, and the CLI only
// translates.
func (s *Service) Handlers() map[string]localchan.Handler {
	return map[string]localchan.Handler{
		"approval decide": localchan.HandlerFunc(s.handleDecide),
		"approval reply":  localchan.HandlerFunc(s.handleReply),
		"approval list":   localchan.HandlerFunc(s.handleList),
	}
}

// Register registers the handlers in the local forwarder of the Citadel.
func (s *Service) Register(fwd *localchan.LocalForwarder) {
	for cmd, h := range s.Handlers() {
		fwd.Handle(cmd, h)
	}
}

// cliAnswers are the answers of an agent by the name the CLI gives them.
var cliAnswers = map[string]Answer{
	"approve": AnswerApprove, "approve-for": AnswerApproveFor, "approve-pattern": AnswerApprovePattern, "always": AnswerApproveAlways,
	"deny": AnswerDeny, "deny-with": AnswerDenyInstruction, "never": AnswerDenyAlways, "ask-back": AnswerAskBack, "escalate": AnswerClimb,
}

// caller is the character that calls, from the identity the engine itself
// knows.
func (s *Service) caller(ctx context.Context, c *localchan.Call) (characters.Character, *localchan.Response) {
	fail := func(code, msg string) (characters.Character, *localchan.Response) {
		r := localchan.Fail(code, msg, "run mestre debug")
		return characters.Character{}, &r
	}
	if !c.Caller.Identified {
		return fail(localchan.CodeNotIdentified, "the engine does not know this process as a process of a character")
	}
	who, err := s.o.Characters.Get(ctx, c.Caller.AgentID)
	if err != nil {
		return fail(localchan.CodeNotIdentified, "the engine does not know this character")
	}
	return who, nil
}

func requestID(args []string) (int64, bool) {
	if len(args) == 0 {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(args[0]), "#"), 10, 64)
	return id, err == nil && id > 0
}

func flagText(c *localchan.Call, name string) string {
	v, _ := c.Request.Flags[name].(string)
	return v
}

// RefusalResponse turns what Decide and Reply refuse into the answer of the CLI,
// with the codes of docs/cli/grao-mestre.md#mestre-approval.
func RefusalResponse(err error) localchan.Response {
	var ref *Refusal
	switch {
	case errors.Is(err, ErrNoRequest) && !errors.As(err, &ref):
		return localchan.Fail("not_found", err.Error(), "see `mestre approval list`").WithExit(localchan.ExitNotFound)
	case !errors.As(err, &ref):
		return localchan.Fail(localchan.CodeInternal, "the approval could not be handled", "")
	}
	switch ref.Code {
	case CodeNotAtLevel:
		return localchan.Fail("not_at_level", ref.Message, "see `mestre approval list --mine`").WithExit(localchan.ExitDenied)
	case CodeNoRequest:
		return localchan.Fail("already_decided", ref.Message, "").WithExit(localchan.ExitConflict)
	case CodeHumanRequired, CodeAboveCeiling, CodeSelfDecision, CodeAttenuation, CodeMesaCondition:
		hint := "ask the users with `mestre approval ask`"
		if ref.Climbed {
			hint = "the request went up by itself"
		}
		return localchan.Fail("human_required", ref.Message, hint).WithExit(localchan.ExitDenied)
	case CodeDurationTooLong:
		return localchan.Fail("too_long", ref.Message, "").WithExit(localchan.ExitUsage)
	case CodePatternTooBroad:
		return localchan.Fail("pattern_too_broad", ref.Message, "").WithExit(localchan.ExitUsage)
	case CodePatternNoMatch:
		return localchan.Fail("pattern_mismatch", ref.Message, "").WithExit(localchan.ExitUsage)
	case CodeBadAnswer:
		return localchan.Fail("bad_answer", ref.Message, "").WithExit(localchan.ExitUsage)
	case CodeReasonRequired:
		return localchan.Fail(localchan.CodeUsage, ref.Message, `use --reason "<reason>"`)
	case CodeNotRequester:
		return localchan.Fail("not_requester", ref.Message, "").WithExit(localchan.ExitDenied)
	case codeNothingToAnswer:
		return localchan.Fail("nothing_to_answer", ref.Message, "").WithExit(localchan.ExitConflict)
	case CodePaused:
		return localchan.Fail("character_paused", ref.Message, "").WithExit(localchan.ExitDenied)
	}
	return localchan.Fail(ref.Code, ref.Message, "")
}

func (s *Service) handleDecide(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := s.caller(ctx, c)
	if bad != nil {
		return *bad
	}
	args, ok := c.Request.Strings()
	id, okID := requestID(args)
	if !ok || len(args) != 2 || !okID {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre approval decide <id> <answer> --reason "<reason>" [--for <duration> | --until-quest] [--pattern "<pattern>"] [--text "<text>"]`, "")
	}
	answer, known := cliAnswers[args[1]]
	if !known {
		return localchan.Fail(localchan.CodeUsage, fmt.Sprintf("unknown answer %q", args[1]), "see `mestre approval list` for the answers of a request")
	}
	// a request of another workspace is not there for this caller
	r, err := s.st.Get(ctx, id)
	if errors.Is(err, ErrNoRequest) || (err == nil && r.Workspace != who.Workspace) {
		return localchan.Fail("not_found", fmt.Sprintf("no request #%d", id), "see `mestre approval list`").WithExit(localchan.ExitNotFound)
	}
	if err != nil {
		return RefusalResponse(err)
	}
	d := Decision{Request: id, By: who.ID, Answer: answer, Reason: flagText(c, "reason"), Pattern: flagText(c, "pattern"), Instruction: flagText(c, "text")}
	if v, _ := c.Request.Flags["until-quest"].(bool); v {
		d.UntilMissionEnd = true
	}
	if f := flagText(c, "for"); f != "" {
		dur, err := time.ParseDuration(f)
		if err != nil || dur <= 0 {
			return localchan.Fail(localchan.CodeUsage, fmt.Sprintf("--for is a duration like 2h, not %q", f), "")
		}
		d.For = dur
	}
	if answer == AnswerApproveFor || answer == AnswerApprovePattern {
		switch {
		case d.For > 0 && d.UntilMissionEnd:
			return localchan.Fail(localchan.CodeUsage, "--for and --until-quest are two ways to say how long: give one", "")
		case d.For <= 0 && !d.UntilMissionEnd:
			return localchan.Fail(localchan.CodeUsage, args[1]+" needs --for <duration> or --until-quest", "")
		}
	}
	out, err := s.Decide(ctx, d)
	if err != nil {
		return RefusalResponse(err)
	}
	return localchan.OK(outcomeView(out))
}

func outcomeView(o Outcome) map[string]any {
	return map[string]any{"request": o.Request, "state": string(o.State), "level": o.Level.Label(), "applied": o.Applied, "suggested": strings.Contains(o.Applied, "suggestion"),
		"text": fmt.Sprintf("pedido #%d: %s", o.Request, orDefault(o.Applied, string(o.State)))}
}

func (s *Service) handleReply(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := s.caller(ctx, c)
	if bad != nil {
		return *bad
	}
	args, ok := c.Request.Strings()
	id, okID := requestID(args)
	if !ok || len(args) != 2 || !okID || strings.TrimSpace(args[1]) == "" {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre approval reply <id> "<text>"`, "")
	}
	// a request of another workspace is not there for this caller
	if r, err := s.st.Get(ctx, id); err == nil && r.Workspace != who.Workspace {
		return localchan.Fail("not_found", fmt.Sprintf("no request #%d", id), "").WithExit(localchan.ExitNotFound)
	}
	if err := s.Reply(ctx, id, who.ID, args[1]); err != nil {
		return RefusalResponse(err)
	}
	r, err := s.st.Get(ctx, id)
	if err != nil {
		return RefusalResponse(err)
	}
	return localchan.OK(map[string]any{"request": id, "level": r.Level.Label(), "text": fmt.Sprintf("resposta ao pedido #%d entregue a quem perguntou", id)})
}

func (s *Service) handleList(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := s.caller(ctx, c)
	if bad != nil {
		return *bad
	}
	state := flagText(c, "state")
	if state == "" {
		state = "pending"
	}
	where, args := "workspace = ?", []any{who.Workspace}
	switch state {
	case "pending":
		where += " AND state = 'open'"
	case "decided":
		where += " AND state != 'open'"
	case "all":
	default:
		return localchan.Fail(localchan.CodeUsage, "--state is pending, decided or all", "")
	}
	// a table master sees the requests of its own table; the Grand Master, of all
	table := flagText(c, "table")
	if who.Role == characters.RoleTableMaster {
		if table != "" && table != who.Table {
			return localchan.Fail("level_denied", "a Mestre da mesa lists the requests of its own table", "").WithExit(localchan.ExitDenied)
		}
		table = who.Table
	}
	if table != "" {
		where += " AND table_id = ?"
		args = append(args, table)
	}
	if mine, _ := c.Request.Flags["mine"].(bool); mine {
		where += " AND state = 'open' AND level_agent = ?"
		args = append(args, who.ID)
	}
	rs, err := s.st.list(ctx, where, args...)
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, "the requests could not be read", "")
	}
	items := make([]map[string]any, 0, len(rs))
	var text strings.Builder
	for _, r := range rs {
		it := map[string]any{"id": r.ID, "action": r.About, "table": r.Table, "requested_by": orDefault(r.RequesterName, r.Requester), "risk": string(r.Risk.Risk),
			"level": r.Level.Label(), "state": string(r.State), "decided_by": "", "expires_at": ""}
		if r.Decision != nil {
			it["decided_by"] = r.Decision.By
		}
		if !r.Deadline.IsZero() {
			it["expires_at"] = r.Deadline.UTC().Format(time.RFC3339)
		}
		items = append(items, it)
		fmt.Fprintf(&text, "- #%d %s (%s, %s, %s)\n", r.ID, oneLineText(r.About), r.State, r.Risk.Risk, r.Level.Label())
	}
	if len(items) == 0 {
		text.WriteString("nenhum pedido\n")
	}
	return localchan.OK(map[string]any{"items": items, "text": strings.TrimRight(text.String(), "\n")})
}

func oneLineText(s string) string { return strings.Join(strings.Fields(s), " ") }
