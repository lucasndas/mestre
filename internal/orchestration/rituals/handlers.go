package rituals

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// Handlers are the commands of the rituals to register in the local forwarder
// of the Citadel: `ritual create|list|show|edit|enable|disable|run|delete`
// (docs/cli/coordenacao-classes-e-rituais.md has the syntax; the CLI answers
// the syntax and the level, the engine the rest).
func (s *Service) Handlers() map[string]localchan.Handler {
	return map[string]localchan.Handler{
		"ritual create":  localchan.HandlerFunc(s.handleCreate),
		"ritual list":    localchan.HandlerFunc(s.handleList),
		"ritual show":    localchan.HandlerFunc(s.handleShow),
		"ritual edit":    localchan.HandlerFunc(s.handleEdit),
		"ritual enable":  localchan.HandlerFunc(s.handleEnable),
		"ritual disable": localchan.HandlerFunc(s.handleDisable),
		"ritual run":     localchan.HandlerFunc(s.handleRun),
		"ritual delete":  localchan.HandlerFunc(s.handleDelete),
	}
}

// Register registers the handlers in the local forwarder of the Citadel.
func (s *Service) Register(fwd *localchan.LocalForwarder) {
	for cmd, h := range s.Handlers() {
		fwd.Handle(cmd, h)
	}
}

// Refusal turns an error of this package into the answer of the CLI.
func Refusal(err error) localchan.Response {
	var pending *ApprovalPendingError
	switch {
	case errors.As(err, &pending):
		r := localchan.Fail("approval_required", err.Error(), "the decision arrives in your inbox").WithExit(localchan.ExitPending)
		r.Error.Details["request"] = pending.Request
		return r
	case errors.Is(err, ErrApproval):
		return localchan.Fail("approval_required", err.Error(), "ask a user to request it").WithExit(localchan.ExitPending)
	case errors.Is(err, ErrNoRitual):
		return localchan.Fail("not_found", err.Error(), "see `mestre ritual list`").WithExit(localchan.ExitNotFound)
	case errors.Is(err, ErrNotVisible):
		return localchan.Fail("not_visible", err.Error(), "aim at a character of your table, not at a sheet").WithExit(localchan.ExitNotFound)
	case errors.Is(err, ErrNameTaken):
		return localchan.Fail("name_taken", err.Error(), "use another name").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrInvalidSchedule):
		return localchan.Fail("invalid_schedule", err.Error(), "use exactly one of --every, --daily, --weekly and --once").WithExit(localchan.ExitUsage)
	case errors.Is(err, ErrLevelDenied):
		return localchan.Fail("level_denied", err.Error(), "the Master of the table has the rituals of its table; the Grand Master the ones of the workspace").WithExit(localchan.ExitDenied)
	case errors.Is(err, ErrBadRequest):
		return localchan.Fail(localchan.CodeUsage, err.Error(), "see `mestre help ritual create`")
	}
	return localchan.Fail(localchan.CodeInternal, err.Error(), "")
}

func flagText(c *localchan.Call, name string) (string, bool) {
	v, ok := c.Request.Flags[name].(string)
	return strings.TrimSpace(v), ok
}

// scope is who asks and the scope of the rituals he has: the table of a Master
// of a table, the workspace for the Grand Master.
func (s *Service) scope(ctx context.Context, c *localchan.Call) (Actor, string, string, *localchan.Response) {
	x, refused := inbox.Caller(ctx, s.o.Registry, c)
	if refused != nil {
		return Actor{}, "", "", refused
	}
	switch x.Role {
	case characters.RoleTableMaster:
		return Actor{Kind: ByCharacter, Character: x}, x.Workspace, x.Table, nil
	case characters.RoleGrandMaster:
		return Actor{Kind: ByCharacter, Character: x}, x.Workspace, "", nil
	}
	r := Refusal(ErrLevelDenied)
	return Actor{}, "", "", &r
}

// ritualOf finds the ritual the command names.
func (s *Service) ritualOf(ctx context.Context, c *localchan.Call) (Ritual, Actor, *localchan.Response) {
	by, ws, table, refused := s.scope(ctx, c)
	if refused != nil {
		return Ritual{}, by, refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		r := localchan.Fail(localchan.CodeUsage, `usage: mestre ritual <command> "<name>"`, "")
		return Ritual{}, by, &r
	}
	r, err := s.Get(ctx, by, ws, table, strings.TrimSpace(args[0]))
	if err != nil {
		resp := Refusal(err)
		return Ritual{}, by, &resp
	}
	return r, by, nil
}

// view is a ritual in the answers (docs/cli/coordenacao-classes-e-rituais.md).
func (s *Service) view(ctx context.Context, r Ritual) map[string]any {
	when := func(t time.Time) any {
		if t.IsZero() {
			return nil
		}
		return t.UTC().Format(time.RFC3339)
	}
	last := ""
	if fs, err := s.st.Firings(ctx, r.ID); err == nil && len(fs) > 0 {
		last = string(fs[len(fs)-1].State)
	}
	table := r.Table
	if table == "" {
		table = r.TargetTable
	}
	return map[string]any{"name": r.Name, "target": r.Target, "quest": r.Quest, "table": table, "action": string(r.Action), "schedule": r.Schedule.String(),
		"pre_run": r.PreRun, "timeout": r.Timeout.String(), "enabled": r.State == Enabled, "state": string(r.State), "next_run": when(r.NextRun), "last_result": last}
}

func (s *Service) line(v map[string]any) string {
	target := fmt.Sprint(v["target"])
	if v["action"] == string(ActionQuest) {
		target = "quest " + fmt.Sprint(v["quest"])
	}
	state := "on"
	if v["enabled"] != true {
		state = "off (" + fmt.Sprint(v["state"]) + ")"
	}
	last := fmt.Sprint(v["last_result"])
	if last == "" {
		last = "-"
	}
	return fmt.Sprintf("%s  %s  %s  next %v  %s  last %s", v["name"], target, v["schedule"], v["next_run"], state, last)
}

func (s *Service) handleCreate(ctx context.Context, c *localchan.Call) localchan.Response {
	by, ws, table, refused := s.scope(ctx, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre ritual create "<name>" (--target "<character>" (--prompt "<text>" | --command "<line>") | --quest "<quest>") (--every <dur> | --daily <HH:MM> | --weekly "<days> <HH:MM>" | --once "<ISO 8601>")`, "")
	}
	target, _ := flagText(c, "target")
	quest, _ := flagText(c, "quest")
	prompt, _ := c.Request.Flags["prompt"].(string)
	command, _ := c.Request.Flags["command"].(string)
	preRun, _ := flagText(c, "pre-run")
	targetTable, ok := s.targetTable(by, table, c)
	if !ok {
		return Refusal(ErrLevelDenied)
	}
	if table == "" && target != "" && targetTable == "" && !strings.EqualFold(target, by.Character.Name) {
		return localchan.Fail("table_required", "the Grand Master aims a ritual at a table master with --table", "add --table \"<table>\"").WithExit(localchan.ExitUsage)
	}
	sch, err := s.schedule(ctx, ws, c)
	if err != nil {
		return Refusal(err)
	}
	timeout, err := flagDuration(c, "timeout")
	if err != nil {
		return Refusal(err)
	}
	r, err := s.Create(ctx, CreateRequest{Workspace: ws, Table: table, Name: args[0], Target: target, TargetTable: targetTable, Quest: quest, Prompt: prompt, Command: command,
		Schedule: sch, PreRun: preRun, Timeout: timeout, By: by})
	if err != nil {
		return Refusal(err)
	}
	v := s.view(ctx, r)
	v["text"] = fmt.Sprintf("ritual %s made: %s, next %v", r.Name, r.Schedule.String(), v["next_run"])
	return localchan.OK(v)
}

// targetTable is the table the target of a ritual of the workspace is in: the
// flag --table of the Grand Master. A Master of a table has no such thing.
func (s *Service) targetTable(by Actor, ownTable string, c *localchan.Call) (string, bool) {
	t, given := flagText(c, "table")
	switch {
	case !given || t == "":
		return "", true
	case ownTable != "":
		return "", t == ownTable
	}
	return t, true
}

// schedule reads the four flags of the schedule that are present.
func (s *Service) schedule(ctx context.Context, ws string, c *localchan.Call) (Schedule, error) {
	var sp Spec
	sp.Every, _ = flagText(c, "every")
	sp.Daily, _ = flagText(c, "daily")
	sp.Weekly, _ = flagText(c, "weekly")
	sp.Once, _ = flagText(c, "once")
	return s.ParseSchedule(ctx, ws, sp)
}

func flagDuration(c *localchan.Call, name string) (time.Duration, error) {
	v, ok := flagText(c, name)
	if !ok || v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%w: --%s %q is not a duration", ErrBadRequest, name, v)
	}
	return d, nil
}

func (s *Service) handleList(ctx context.Context, c *localchan.Call) localchan.Response {
	by, ws, table, refused := s.scope(ctx, c)
	if refused != nil {
		return *refused
	}
	rs, err := s.List(ctx, by, ws, table)
	if err != nil {
		return Refusal(err)
	}
	views := []map[string]any{}
	var text strings.Builder
	for _, r := range rs {
		v := s.view(ctx, r)
		views = append(views, v)
		text.WriteString(s.line(v) + "\n")
	}
	if len(views) == 0 {
		text.WriteString("no rituals\n")
	}
	return localchan.OK(map[string]any{"rituals": views, "text": strings.TrimRight(text.String(), "\n")})
}

func (s *Service) handleShow(ctx context.Context, c *localchan.Call) localchan.Response {
	r, by, refused := s.ritualOf(ctx, c)
	if refused != nil {
		return *refused
	}
	v := s.view(ctx, r)
	v["content"] = r.Content
	hist := []map[string]any{}
	text := s.line(v)
	fs, err := s.History(ctx, by, r.Workspace, r.Table, r.ID)
	if err != nil {
		return Refusal(err)
	}
	for i := len(fs) - 1; i >= 0 && len(hist) < 10; i-- {
		f := fs[i]
		h := map[string]any{"scheduled_for": f.ScheduledFor.UTC().Format(time.RFC3339), "state": string(f.State), "reason": f.Reason}
		hist = append(hist, h)
		text += fmt.Sprintf("\n  %s  %s  %s", h["scheduled_for"], f.State, f.Reason)
	}
	v["history"] = hist
	v["text"] = text
	return localchan.OK(v)
}

func (s *Service) handleEdit(ctx context.Context, c *localchan.Call) localchan.Response {
	r, by, refused := s.ritualOf(ctx, c)
	if refused != nil {
		return *refused
	}
	req := EditRequest{By: by}
	str := func(name string) *string {
		if v, ok := c.Request.Flags[name].(string); ok {
			v = strings.TrimSpace(v)
			return &v
		}
		return nil
	}
	req.Target, req.Quest, req.PreRun = str("target"), str("quest"), str("pre-run")
	if v, ok := c.Request.Flags["prompt"].(string); ok {
		req.Prompt = &v
	}
	if v, ok := c.Request.Flags["command"].(string); ok {
		req.Command = &v
	}
	if t, ok := s.targetTable(by, r.Table, c); !ok {
		return Refusal(ErrLevelDenied)
	} else if _, given := flagText(c, "table"); given {
		req.TargetTable = &t
	}
	timeout, err := flagDuration(c, "timeout")
	if err != nil {
		return Refusal(err)
	}
	if _, given := flagText(c, "timeout"); given {
		req.Timeout = &timeout
	}
	for _, f := range []string{"every", "daily", "weekly", "once"} {
		if _, ok := c.Request.Flags[f]; ok {
			sch, err := s.schedule(ctx, r.Workspace, c)
			if err != nil {
				return Refusal(err)
			}
			req.Schedule = &sch
			break
		}
	}
	out, err := s.Edit(ctx, r.ID, req)
	if err != nil {
		return Refusal(err)
	}
	v := s.view(ctx, out)
	v["text"] = fmt.Sprintf("ritual %s changed: %s, next %v", out.Name, out.Schedule.String(), v["next_run"])
	return localchan.OK(v)
}

func (s *Service) toggle(ctx context.Context, c *localchan.Call, on bool) localchan.Response {
	r, by, refused := s.ritualOf(ctx, c)
	if refused != nil {
		return *refused
	}
	out, err := s.SetEnabled(ctx, r.ID, by, on)
	if err != nil {
		return Refusal(err)
	}
	v := s.view(ctx, out)
	word := "on"
	if !on {
		word = "off"
	}
	v["text"] = fmt.Sprintf("ritual %s is %s", out.Name, word)
	return localchan.OK(v)
}

func (s *Service) handleEnable(ctx context.Context, c *localchan.Call) localchan.Response {
	return s.toggle(ctx, c, true)
}

func (s *Service) handleDisable(ctx context.Context, c *localchan.Call) localchan.Response {
	return s.toggle(ctx, c, false)
}

func (s *Service) handleRun(ctx context.Context, c *localchan.Call) localchan.Response {
	r, by, refused := s.ritualOf(ctx, c)
	if refused != nil {
		return *refused
	}
	f, err := s.Run(ctx, r.ID, by)
	if err != nil {
		return Refusal(err)
	}
	return localchan.OK(map[string]any{"name": r.Name, "state": string(f.State), "reason": f.Reason,
		"text": fmt.Sprintf("ritual %s fired: %s %s", r.Name, f.State, f.Reason)})
}

func (s *Service) handleDelete(ctx context.Context, c *localchan.Call) localchan.Response {
	r, by, refused := s.ritualOf(ctx, c)
	if refused != nil {
		return *refused
	}
	if err := s.Delete(ctx, r.ID, by, false); err != nil {
		return Refusal(err)
	}
	return localchan.OK(map[string]any{"name": r.Name, "text": fmt.Sprintf("ritual %s deleted", r.Name)})
}
