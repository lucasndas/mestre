package tables

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
)

// Handlers are the commands `table open|list|status|close` of the Grand
// Master (docs/cli/grao-mestre.md#mestre-table), to register in the local
// forwarder of the Citadel. The CLI answers the syntax and the level; the
// engine checks the level again, because the identity of the caller is its
// own.
func (s *Service) Handlers() map[string]localchan.Handler {
	return map[string]localchan.Handler{
		"table open":   localchan.HandlerFunc(s.handleOpen),
		"table list":   localchan.HandlerFunc(s.handleList),
		"table status": localchan.HandlerFunc(s.handleStatus),
		"table close":  localchan.HandlerFunc(s.handleClose),
	}
}

// Register registers the handlers in the local forwarder of the Citadel
// (app.Engine.Commands).
func (s *Service) Register(fwd *localchan.LocalForwarder) {
	for cmd, h := range s.Handlers() {
		fwd.Handle(cmd, h)
	}
}

// Refusal turns an error of this package into the answer of the CLI, with the
// codes of docs/cli/grao-mestre.md#mestre-table.
func Refusal(err error) localchan.Response {
	var miss *MissingError
	var na *workspaces.NotActiveError
	switch {
	case errors.Is(err, ErrNotGrandMaster):
		return localchan.Fail("level_denied", "só o Grão-Mestre do workspace abre e encerra mesas", "")
	case errors.As(err, &na):
		return localchan.Fail("workspace_not_active", na.Error(), "espere o workspace ficar ativo, ou retome-o se está pausado").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrEpicNotFound):
		return localchan.Fail("epic_not_found", err.Error(), "crie o épico com `mestre gh epic create` antes; `table open` nunca cria épico").WithExit(localchan.ExitNotFound)
	case errors.Is(err, ErrEpicClosed):
		return localchan.Fail("epic_closed", err.Error(), "reabra o épico ou crie outro").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrTableExists):
		return localchan.Fail("table_exists", err.Error(), "veja `mestre table list`").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrNotMerged):
		return localchan.Fail("not_merged", err.Error(), "o merge do PR precisa estar confirmado; o motor avisa quando estiver").WithExit(localchan.ExitConflict)
	case errors.As(err, &miss):
		return missingResponse(miss)
	case errors.Is(err, ErrApprovalRequired):
		return localchan.Fail("pending", "cancelar uma mesa é ação destrutiva: aguarda a aprovação de um usuário", "a decisão chega pela caixa de entrada").WithExit(localchan.ExitPending)
	case errors.Is(err, ErrNoTable):
		return localchan.Fail("not_found", err.Error(), "veja `mestre table list`")
	case errors.Is(err, ErrBadTransition):
		return localchan.Fail("bad_state", err.Error(), "").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrRefMoved):
		return localchan.Fail("ref_moved", err.Error(), "um Administrador precisa conferir e confiar de novo na ref").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrMachineUnreachable):
		return localchan.Fail("machine_unreachable", err.Error(), "")
	case errors.Is(err, workspaces.ErrNotFound):
		return localchan.Fail("not_found", err.Error(), "")
	}
	return localchan.Fail(localchan.CodeInternal, err.Error(), "")
}

// missingResponse answers a table that cannot end yet: unmerged_changes with
// the list when something needs a decision, otherwise "scheduled" (it only
// waits for characters to reach a safe point).
func missingResponse(m *MissingError) localchan.Response {
	var r localchan.Response
	if len(m.Decide) > 0 {
		r = localchan.Fail("unmerged_changes", m.Error(), "pergunte aos usuários e chame `table close` de novo (com --keep para manter os worktrees)").WithExit(localchan.ExitConflict)
	} else {
		r = localchan.Fail("scheduled", m.Error(), "o motor continua o encerramento sozinho e avisa quando terminar").WithExit(localchan.ExitPending)
	}
	r.Error.Details = map[string]any{"table": m.Table, "decide": orEmpty(m.Decide), "pending": orEmpty(m.Pending)}
	return r
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// caller is the Grand Master the engine verified.
func (s *Service) caller(ctx context.Context, c *localchan.Call) (characters.Character, *localchan.Response) {
	if !c.Caller.Identified {
		r := localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this process as a process of a character", "run mestre debug")
		return characters.Character{}, &r
	}
	x, err := s.o.Registry.Get(ctx, c.Caller.AgentID)
	if err != nil {
		r := localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this character", "run mestre debug")
		return characters.Character{}, &r
	}
	if x.Role != characters.RoleGrandMaster {
		r := Refusal(ErrNotGrandMaster)
		return characters.Character{}, &r
	}
	return x, nil
}

func flagString(c *localchan.Call, name string) string {
	v, _ := c.Request.Flags[name].(string)
	return v
}

func flagBool(c *localchan.Call, name string) bool {
	v, _ := c.Request.Flags[name].(bool)
	return v
}

func (s *Service) handleOpen(ctx context.Context, c *localchan.Call) localchan.Response {
	gm, refused := s.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	epic, err := strconv.Atoi(strings.TrimPrefix(flagString(c, "epic"), "#"))
	if err != nil || epic <= 0 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre table open --epic <n> [--sheet "<ficha>"] [--base-branch <branch>] [--from-message <id>]`, "")
	}
	res, err := s.Open(ctx, OpenRequest{By: gm, Epic: epic, Sheet: flagString(c, "sheet"), BaseBranch: flagString(c, "base-branch"),
		FromMessage: flagString(c, "from-message"), Notes: flagString(c, "notes")})
	if err != nil {
		return Refusal(err)
	}
	master := map[string]any{"name": res.Master.Name, "id": res.Master.ID, "machine": res.Master.Machine}
	return localchan.OK(map[string]any{
		"text": fmt.Sprintf("mesa %s aberta (branch %s); Mestre da mesa %s recrutado", res.Table.ID, res.Table.Branch, res.Master.Name),
		"name": res.Table.ID, "branch": res.Table.Branch, "epic": res.Table.Epic, "worktree": res.Worktree, "master": master,
	})
}

// statePT is the state as the people read it, with the mark of the pause.
var statePT = map[State]string{
	StateOpen: "aberta", StateWorking: "em trabalho", StateInReview: "em revisão", StateClosing: "encerrando",
	StateClosed: "encerrada", StateCancelling: "cancelando", StateCancelled: "cancelada",
}

func (s *Service) handleList(ctx context.Context, c *localchan.Call) localchan.Response {
	gm, refused := s.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	filter := flagString(c, "state")
	if filter == "" {
		filter = "all"
	}
	if filter != "open" && filter != "closed" && filter != "all" {
		return localchan.Fail(localchan.CodeUsage, "--state is open, closed or all", "")
	}
	ts, err := s.store.List(ctx, gm.Workspace)
	if err != nil {
		return Refusal(err)
	}
	type row struct {
		Name     string   `json:"name"`
		State    string   `json:"state"`
		Paused   bool     `json:"paused"`
		Epic     int      `json:"epic"`
		Branch   string   `json:"branch"`
		Master   string   `json:"master"`
		Machines []string `json:"machines"`
		PR       int      `json:"pr"`
		Usage    string   `json:"usage"`
	}
	rows := make([]row, 0, len(ts))
	var text strings.Builder
	for _, t := range ts {
		if filter == "open" && Over(t.State) || filter == "closed" && !Over(t.State) {
			continue
		}
		r := row{Name: t.ID, State: string(t.State), Paused: t.Paused, Epic: t.Epic, Branch: t.Branch, PR: t.PRNumber, Machines: []string{}, Usage: "-"}
		if m, ok, _ := s.o.Registry.TableMaster(ctx, t.Workspace, t.ID); ok {
			r.Master = m.Name
		}
		if wts, err := s.store.Worktrees(ctx, t.Workspace, t.ID); err == nil {
			for _, w := range wts {
				r.Machines = append(r.Machines, w.Machine)
			}
		}
		rows = append(rows, r)
		st := statePT[t.State]
		if t.Paused {
			st += " (pausada)"
		}
		fmt.Fprintf(&text, "- %s: %s, épico #%d, branch %s, PR %s\n", t.ID, st, t.Epic, t.Branch, prLabel(t.PRNumber))
	}
	if len(rows) == 0 {
		text.WriteString("nenhuma mesa\n")
	}
	return localchan.OK(map[string]any{"text": strings.TrimRight(text.String(), "\n"), "tables": rows})
}

func prLabel(n int) string {
	if n == 0 {
		return "-"
	}
	return "#" + strconv.Itoa(n)
}

func (s *Service) handleStatus(ctx context.Context, c *localchan.Call) localchan.Response {
	gm, refused := s.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre table status "<mesa>"`, "")
	}
	t, err := s.Find(ctx, gm.Workspace, args[0])
	if err != nil {
		return Refusal(err)
	}
	cs, err := s.o.Registry.AliveOf(ctx, t.Workspace)
	if err != nil {
		return Refusal(err)
	}
	type charRow struct {
		Name    string `json:"name"`
		Role    string `json:"role"`
		Machine string `json:"machine"`
		State   string `json:"state"`
		Reason  string `json:"reason,omitempty"`
	}
	chars := []charRow{}
	for _, ch := range cs {
		if ch.Table == t.ID {
			chars = append(chars, charRow{Name: ch.Name, Role: string(ch.Role), Machine: ch.Machine, State: string(ch.State), Reason: ch.Reason})
		}
	}
	var text strings.Builder
	st := statePT[t.State]
	if t.Paused {
		st += " (pausada: " + t.PausedReason + ")"
	}
	fmt.Fprintf(&text, "Mesa %s: %s. Épico #%d, branch %s, PR %s", t.ID, st, t.Epic, t.Branch, prLabel(t.PRNumber))
	if t.Merged {
		text.WriteString(" (mesclado)")
	}
	text.WriteString(".\nPersonagens:\n")
	for _, ch := range chars {
		fmt.Fprintf(&text, "- %s (%s): %s\n", ch.Name, ch.Role, ch.State)
	}
	if len(chars) == 0 {
		text.WriteString("- ninguém\n")
	}
	return localchan.OK(map[string]any{"text": strings.TrimRight(text.String(), "\n"), "name": t.ID, "state": string(t.State), "paused": t.Paused,
		"pause_reason": t.PausedReason, "epic": t.Epic, "branch": t.Branch, "pr": t.PRNumber, "merged": t.Merged, "characters": chars,
		"missions": []string{}, "waits": []string{}, "usage": "-"})
}

func (s *Service) handleClose(ctx context.Context, c *localchan.Call) localchan.Response {
	gm, refused := s.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre table close "<mesa>" [--cancel --reason "<texto>"]`, "")
	}
	cancel := flagBool(c, "cancel")
	reason := flagString(c, "reason")
	if cancel && strings.TrimSpace(reason) == "" {
		return localchan.Fail(localchan.CodeUsage, "--cancel needs --reason", "")
	}
	res, err := s.Close(ctx, CloseRequest{By: gm, Table: args[0], Cancel: cancel, Reason: reason, Keep: flagBool(c, "keep")})
	if err != nil {
		return Refusal(err)
	}
	verb := "encerrada"
	if res.Table.State == StateCancelled {
		verb = "cancelada"
	}
	text := fmt.Sprintf("mesa %s %s", res.Table.ID, verb)
	if len(res.Retained) > 0 {
		text += "; worktrees mantidos por decisão: " + strings.Join(res.Retained, ", ")
	}
	return localchan.OK(map[string]any{"text": text, "name": res.Table.ID, "state": string(res.Table.State), "retained": orEmpty(res.Retained)})
}
