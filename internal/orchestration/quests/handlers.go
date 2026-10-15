package quests

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

// Handlers are the commands of this document to register in the local
// forwarder of the Citadel: `done` and `quest create|run|pause|resume|cancel|
// status` (docs/cli/comunicacao.md and docs/cli/coordenacao.md have the syntax;
// the CLI answers the syntax, the files and the level, the engine does the
// rest).
func (s *Service) Handlers() map[string]localchan.Handler {
	return map[string]localchan.Handler{
		"done":         localchan.HandlerFunc(s.handleDone),
		"quest create": localchan.HandlerFunc(s.handleCreate),
		"quest run":    localchan.HandlerFunc(s.handleRun),
		"quest pause":  localchan.HandlerFunc(s.handlePause),
		"quest resume": localchan.HandlerFunc(s.handleResume),
		"quest cancel": localchan.HandlerFunc(s.handleCancel),
		"quest status": localchan.HandlerFunc(s.handleStatus),
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
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		return localchan.Fail("invalid_quest", ve.Error(), "fix the definition; `mestre quest create` checks it again").WithExit(localchan.ExitUsage)
	case errors.Is(err, ErrNoQuest):
		return localchan.Fail("not_found", err.Error(), "see `mestre quest status`")
	case errors.Is(err, ErrNameTaken):
		return localchan.Fail("name_taken", err.Error(), "use another name").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrInvalidState):
		return localchan.Fail("invalid_state", err.Error(), "see `mestre quest status`").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrNoReturnStep):
		return localchan.Fail("no_return_step", err.Error(), "only a step with can_return_to hands the work back").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrTooLong):
		return localchan.Fail(localchan.CodeTooLarge, err.Error(), "use --file for a longer summary")
	case errors.Is(err, ErrNotMaster):
		return localchan.Fail("level_denied", err.Error(), "ask the Master of the table")
	case errors.Is(err, characters.ErrTableClosed), errors.Is(err, tables.ErrBadTransition):
		return localchan.Fail("invalid_state", err.Error(), "").WithExit(localchan.ExitConflict)
	}
	return localchan.Fail(localchan.CodeInternal, err.Error(), "")
}

func flagString(c *localchan.Call, name string) string {
	v, _ := c.Request.Flags[name].(string)
	return v
}

// master is the table master that asks, and the table it conducts.
func (s *Service) master(ctx context.Context, c *localchan.Call) (characters.Character, *localchan.Response) {
	x, refused := inbox.Caller(ctx, s.o.Registry, c)
	if refused != nil {
		return x, refused
	}
	if x.Role != characters.RoleTableMaster {
		r := Refusal(ErrNotMaster)
		return x, &r
	}
	return x, nil
}

// stepViews is the shape of the steps in the answers
// (docs/cli/coordenacao.md#mestre-quest): the number, the sheet, the
// character, the state, the returns used, what it waits for and its issue.
func (s *Service) stepViews(ctx context.Context, v View) []map[string]any {
	out := make([]map[string]any, len(v.Steps))
	for i, st := range v.Steps {
		agent := ""
		if st.Character != "" {
			if c, err := s.o.Registry.Get(ctx, st.Character); err == nil {
				agent = c.Name
			}
		}
		out[i] = map[string]any{"n": st.Pos + 1, "id": st.ID, "name": st.Def.Title(), "sheet": st.Def.Sheet, "agent": agent,
			"state": string(st.State), "loops": st.Returns, "waiting_for": st.Waiting, "issue": st.SubIssue}
	}
	return out
}

// currentStep is the number of the step under way (the first that is being
// prepared, runs, is stuck or waits for an approval), or nil.
func currentStep(v View) any {
	for _, st := range v.Steps {
		switch st.State {
		case StepPreparing, StepRunning, StepStuck, StepApproving:
			return st.Pos + 1
		}
	}
	return nil
}

func (s *Service) questOf(ctx context.Context, x characters.Character, c *localchan.Call) (Quest, *localchan.Response) {
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 {
		r := localchan.Fail(localchan.CodeUsage, `usage: mestre quest <command> "<quest>"`, "")
		return Quest{}, &r
	}
	q, err := s.Get(ctx, x.Workspace, x.Table, args[0])
	if err != nil {
		r := Refusal(err)
		return Quest{}, &r
	}
	return q, nil
}

func (s *Service) handleCreate(ctx context.Context, c *localchan.Call) localchan.Response {
	x, refused := s.master(ctx, c)
	if refused != nil {
		return *refused
	}
	// The CLI reads the file of the agent and sends its text; the engine never
	// opens a path an agent names.
	data := []byte(flagString(c, "definition"))
	if len(data) == 0 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre quest create "<quest>" --file <file | ->`, "the definition is YAML or JSON; the CLI reads the file and sends it, the engine never reads a path an agent names")
	}
	def, err := Parse(data)
	if err != nil {
		return Refusal(err)
	}
	if args, ok := c.Request.Strings(); ok && len(args) == 1 && def.Name == "" {
		def.Name = args[0]
	}
	v, err := s.Create(ctx, CreateRequest{Workspace: x.Workspace, Table: x.Table, Def: &def, Designer: x.ID})
	if err != nil {
		return Refusal(err)
	}
	return localchan.OK(map[string]any{"name": v.Name, "id": v.ID, "issue": v.Issue, "state": v.State, "steps": s.stepViews(ctx, v),
		"text": fmt.Sprintf("quest %s created with %d steps (issue #%d)", v.Name, len(v.Steps), v.Issue)})
}

func (s *Service) handleRun(ctx context.Context, c *localchan.Call) localchan.Response {
	x, refused := s.master(ctx, c)
	if refused != nil {
		return *refused
	}
	q, refused := s.questOf(ctx, x, c)
	if refused != nil {
		return *refused
	}
	input := flagString(c, "input")
	got, err := s.Run(ctx, q.ID, input)
	if err != nil {
		return Refusal(err)
	}
	return localchan.OK(map[string]any{"name": got.Name, "id": got.ID, "status": got.State, "round": got.Round,
		"text": fmt.Sprintf("quest %s is %s", got.Name, got.State)})
}

func (s *Service) handlePause(ctx context.Context, c *localchan.Call) localchan.Response {
	return s.simple(ctx, c, func(q Quest) (Quest, error) { return s.Pause(ctx, q.ID) })
}

func (s *Service) handleResume(ctx context.Context, c *localchan.Call) localchan.Response {
	return s.simple(ctx, c, func(q Quest) (Quest, error) { return s.Resume(ctx, q.ID) })
}

func (s *Service) handleCancel(ctx context.Context, c *localchan.Call) localchan.Response {
	return s.simple(ctx, c, func(q Quest) (Quest, error) { return s.Cancel(ctx, q.ID, flagString(c, "reason")) })
}

func (s *Service) simple(ctx context.Context, c *localchan.Call, do func(Quest) (Quest, error)) localchan.Response {
	x, refused := s.master(ctx, c)
	if refused != nil {
		return *refused
	}
	q, refused := s.questOf(ctx, x, c)
	if refused != nil {
		return *refused
	}
	got, err := do(q)
	if err != nil {
		return Refusal(err)
	}
	return localchan.OK(map[string]any{"name": got.Name, "id": got.ID, "status": got.State, "text": fmt.Sprintf("quest %s is %s", got.Name, got.State)})
}

func (s *Service) handleStatus(ctx context.Context, c *localchan.Call) localchan.Response {
	x, refused := inbox.Caller(ctx, s.o.Registry, c)
	if refused != nil {
		return *refused
	}
	table := x.Table
	if x.Role == characters.RoleGrandMaster {
		if table = flagString(c, "table"); table == "" {
			return localchan.Fail("table_required", "the Grand Master names the table with --table", "")
		}
	} else if x.Role != characters.RoleTableMaster {
		return Refusal(ErrNotMaster)
	}
	args, _ := c.Request.Strings()
	if len(args) == 0 {
		qs, err := s.List(ctx, x.Workspace, table)
		if err != nil {
			return Refusal(err)
		}
		out := make([]map[string]any, len(qs))
		var text strings.Builder
		for i, q := range qs {
			out[i] = map[string]any{"name": q.Name, "id": q.ID, "state": q.State, "round": q.Round}
			fmt.Fprintf(&text, "%s  %s\n", q.Name, q.State)
		}
		if len(out) == 0 {
			text.WriteString("no quests\n")
		}
		return localchan.OK(map[string]any{"quests": out, "text": strings.TrimRight(text.String(), "\n")})
	}
	q, err := s.Get(ctx, x.Workspace, table, args[0])
	if err != nil {
		return Refusal(err)
	}
	v, err := s.Status(ctx, q)
	if err != nil {
		return Refusal(err)
	}
	steps := s.stepViews(ctx, v)
	var text strings.Builder
	fmt.Fprintf(&text, "%s  %s", v.Name, v.State)
	if v.PausedReason != "" {
		fmt.Fprintf(&text, " (%s)", v.PausedReason)
	}
	for _, st := range steps {
		fmt.Fprintf(&text, "\n%v. %v  %v  %v  loops %v", st["n"], st["name"], dash(st["agent"]), st["state"], st["loops"])
		if w, _ := st["waiting_for"].(string); w != "" {
			fmt.Fprintf(&text, "  waiting for %s", w)
		}
	}
	return localchan.OK(map[string]any{"name": v.Name, "id": v.ID, "state": v.State, "paused_reason": v.PausedReason, "round": v.Round, "issue": v.Issue,
		"current_step": currentStep(v), "steps": steps, "text": text.String()})
}

// handleDone is `mestre done`: the end of the step of the character, or of its
// task. A done of a character with neither is registered and ignored, and it
// is told so (docs/orquestracao/missoes.md#regras-e-erros).
func (s *Service) handleDone(ctx context.Context, c *localchan.Call) localchan.Response {
	x, refused := inbox.Caller(ctx, s.o.Registry, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre done "<summary>" [--return "<reason>"]`, "")
	}
	req := DoneRequest{Character: x, Text: args[0], Return: flagString(c, "return")}
	req.FromFile, _ = c.Request.Flags["from-file"].(bool)
	if tooLong(req) {
		return Refusal(fmt.Errorf("%w: %d characters at most (--file takes up to %d KiB)", ErrTooLong, MaxSummary, MaxSummaryFile>>10))
	}
	res, err := s.Finish(ctx, req)
	switch {
	case err == nil:
		return localchan.OK(res)
	case !errors.Is(err, ErrNoStep):
		return Refusal(err)
	}
	// No step: a task (the done answers it), or nothing to answer.
	if strings.TrimSpace(flagString(c, "return")) != "" {
		return Refusal(fmt.Errorf("%w: %s has no step", ErrNoReturnStep, x.Name))
	}
	if err := s.o.Characters.Done(ctx, x.ID, args[0]); err != nil {
		return Refusal(err)
	}
	if !x.TaskOpen {
		return localchan.Fail("no_active_task", "you have no step or task under way; the done was registered and ignored",
			"a done answers the task or the step you were given").WithExit(localchan.ExitConflict)
	}
	return localchan.OK(DoneResult{Task: "task", Warnings: []string{}})
}

func dash(v any) any {
	if s, _ := v.(string); s == "" {
		return "-"
	}
	return v
}
