package tables

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Handlers are the commands `table open|list|status|close` of the Grand
// Master (docs/cli/grao-mestre.md#mestre-table), to register in the local
// forwarder of the Citadel. The CLI answers the syntax and the level; the
// engine checks the level again, because the identity of the caller is its
// own.
func (s *Service) Handlers() map[string]localchan.Handler {
	return map[string]localchan.Handler{
		"table open":    localchan.HandlerFunc(s.handleOpen),
		"table list":    localchan.HandlerFunc(s.handleList),
		"table status":  localchan.HandlerFunc(s.handleStatus),
		"table overlap": localchan.HandlerFunc(s.handleOverlap),
		"table close":   localchan.HandlerFunc(s.handleClose),
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
		return localchan.Fail("level_denied", "only the Grand Master of the workspace opens and closes tables", "")
	case errors.As(err, &na):
		return localchan.Fail("workspace_not_active", na.Error(), "wait for the workspace to become active, or resume it if it is paused").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrEpicNotFound):
		return refuse("epic_not_found", err, "create the epic with `mestre gh epic create` first; `table open` never creates an epic").WithExit(localchan.ExitNotFound)
	case errors.Is(err, ErrEpicClosed):
		return refuse("epic_closed", err, "reopen the epic or create another").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrTableExists):
		return refuse("table_exists", err, "see `mestre table list`").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrNotMerged):
		return refuse("not_merged", err, "the merge of the PR must be confirmed first; the engine tells you when it is").WithExit(localchan.ExitConflict)
	case errors.As(err, &miss):
		return missingResponse(miss)
	case errors.Is(err, ErrApprovalRequired):
		return localchan.Fail("pending", "cancelling a table is a destructive action: it waits for the approval of a user", "the decision arrives in your inbox").WithExit(localchan.ExitPending)
	case errors.Is(err, ErrNoTable):
		return refuse("not_found", err, "see `mestre table list`")
	case errors.Is(err, ErrBadTransition):
		return refuse("bad_state", err, "").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrRefMoved):
		return refuse("ref_moved", err, "an Administrator must check and trust the ref again").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrMachineUnreachable):
		return refuse("machine_unreachable", err, "")
	case errors.Is(err, workspaces.ErrNotFound):
		return refuse("not_found", err, "")
	}
	return refuse(localchan.CodeInternal, err, "")
}

// refuse is the refusal for an error: a coded error goes to the catalog in
// English with its parameters, any other keeps its text.
func refuse(code string, err error, hint string) localchan.Response {
	var m i18n.Messager
	if errors.As(err, &m) {
		return localchan.FailMsg(code, m.Msg(), hint)
	}
	return localchan.Fail(code, err.Error(), hint)
}

// missingResponse answers a table that cannot end yet: unmerged_changes with
// the list when something needs a decision, otherwise "scheduled" (it only
// waits for characters to reach a safe point).
func missingResponse(m *MissingError) localchan.Response {
	var r localchan.Response
	if len(m.Decide) > 0 {
		r = localchan.FailMsg("unmerged_changes", m.Msg(), "ask the users and call `table close` again (with --keep to keep the worktrees)").WithExit(localchan.ExitConflict)
	} else {
		r = localchan.FailMsg("scheduled", m.Msg(), "the engine goes on with the closing by itself and tells you when it ends").WithExit(localchan.ExitPending)
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
		return localchan.Fail(localchan.CodeUsage, `usage: mestre table open --epic <n> [--sheet "<sheet>"] [--base-branch <branch>] [--from-message <id>]`, "")
	}
	res, err := s.Open(ctx, OpenRequest{By: gm, Epic: epic, Sheet: flagString(c, "sheet"), BaseBranch: flagString(c, "base-branch"),
		FromMessage: flagString(c, "from-message"), Notes: flagString(c, "notes")})
	if err != nil {
		return Refusal(err)
	}
	master := map[string]any{"name": res.Master.Name, "id": res.Master.ID, "machine": res.Master.Machine}
	return localchan.OK(map[string]any{
		"text": fmt.Sprintf("table %s opened (branch %s); Table Master %s recruited", res.Table.ID, res.Table.Branch, res.Master.Name),
		"name": res.Table.ID, "branch": res.Table.Branch, "epic": res.Table.Epic, "worktree": res.Worktree, "master": master,
	})
}

// stateLabel is the state as the agent reads it, with the mark of the pause.
var stateLabel = map[State]string{
	StateOpen: "open", StateWorking: "working", StateInReview: "in review", StateClosing: "closing",
	StateClosed: "closed", StateCancelling: "cancelling", StateCancelled: "cancelled",
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
		// WaitingFor are the epics whose tables must be merged first.
		WaitingFor []int `json:"waiting_for"`
	}
	rows := make([]row, 0, len(ts))
	var text strings.Builder
	for _, t := range ts {
		if filter == "open" && Over(t.State) || filter == "closed" && !Over(t.State) {
			continue
		}
		r := row{Name: t.ID, State: string(t.State), Paused: t.Paused, Epic: t.Epic, Branch: t.Branch, PR: t.PRNumber, Machines: []string{}, Usage: "-", WaitingFor: s.waitingFor(ctx, t)}
		if m, ok, _ := s.o.Registry.TableMaster(ctx, t.Workspace, t.ID); ok {
			r.Master = m.Name
		}
		if wts, err := s.store.Worktrees(ctx, t.Workspace, t.ID); err == nil {
			for _, w := range wts {
				r.Machines = append(r.Machines, w.Machine)
			}
		}
		rows = append(rows, r)
		st := stateLabel[t.State]
		if t.Paused {
			st += " (paused)"
		}
		st += waitingLabel(r.WaitingFor)
		fmt.Fprintf(&text, "- %s: %s, epic #%d, branch %s, PR %s\n", t.ID, st, t.Epic, t.Branch, prLabel(t.PRNumber))
	}
	if len(rows) == 0 {
		text.WriteString("no tables\n")
	}
	return localchan.OK(map[string]any{"text": strings.TrimRight(text.String(), "\n"), "tables": rows})
}

// waitingFor gives the epics the table waits for (none when it is over, or
// when the dependencies cannot be read: the table list is not worth failing).
func (s *Service) waitingFor(ctx context.Context, t Table) []int {
	if s.o.Dependencies == nil || Over(t.State) {
		return []int{}
	}
	on, err := s.o.Dependencies.Waiting(ctx, t.Workspace, t.Epic)
	if err != nil || on == nil {
		return []int{}
	}
	return on
}

// waitingLabel is " (aguardando a mesa #12)" for the epics a table waits for.
func waitingLabel(on []int) string {
	if len(on) == 0 {
		return ""
	}
	parts := make([]string, len(on))
	for i, e := range on {
		parts[i] = "#" + strconv.Itoa(e)
	}
	noun := "table "
	if len(on) > 1 {
		noun = "tables "
	}
	return " (waiting for " + noun + strings.Join(parts, ", ") + ")"
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
	if !ok || len(args) > 1 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre table status ["<table>"]`, "")
	}
	if len(args) == 0 {
		// every table that is not over, to route a request without guessing
		ts, err := s.store.List(ctx, gm.Workspace)
		if err != nil {
			return Refusal(err)
		}
		rows := []map[string]any{}
		var text strings.Builder
		for _, t := range ts {
			if Over(t.State) {
				continue
			}
			data, line, resp := s.statusOf(ctx, t)
			if resp != nil {
				return *resp
			}
			delete(data, "text")
			rows = append(rows, data)
			text.WriteString(line + "\n\n")
		}
		if len(rows) == 0 {
			text.WriteString("no open tables")
		}
		return localchan.OK(map[string]any{"tables": rows, "text": strings.TrimRight(text.String(), "\n")})
	}
	t, err := s.Find(ctx, gm.Workspace, args[0])
	if err != nil {
		return Refusal(err)
	}
	data, _, resp := s.statusOf(ctx, t)
	if resp != nil {
		return *resp
	}
	return localchan.OK(data)
}

// statusOf is the status of a table: its state, its epic with the criteria and
// the open issues, its pull request with the size and the CI, the folders its
// branch changes, its characters and the last useful delivery.
func (s *Service) statusOf(ctx context.Context, t Table) (map[string]any, string, *localchan.Response) {
	fail := func(err error) (map[string]any, string, *localchan.Response) {
		r := Refusal(err)
		return nil, "", &r
	}
	cs, err := s.o.Registry.AliveOf(ctx, t.Workspace)
	if err != nil {
		return fail(err)
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
	st := stateLabel[t.State]
	if t.Paused {
		st += " (paused: " + t.PausedReason + ")"
	}
	waiting := s.waitingFor(ctx, t)
	st += waitingLabel(waiting)
	fmt.Fprintf(&text, "Table %s: %s. Epic #%d, branch %s, PR %s", t.ID, st, t.Epic, t.Branch, prLabel(t.PRNumber))
	if t.Merged {
		text.WriteString(" (merged)")
	}
	text.WriteString(".\nCharacters:\n")
	for _, ch := range chars {
		fmt.Fprintf(&text, "- %s (%s): %s\n", ch.Name, ch.Role, ch.State)
	}
	if len(chars) == 0 {
		text.WriteString("- nobody\n")
	}
	issues, pr, folders := s.issuesOf(ctx, t), s.prOf(ctx, t), s.foldersOf(ctx, t)
	if list, ok := issues.([]map[string]any); ok {
		fmt.Fprintf(&text, "\nOpen issues: %d", len(list))
	}
	if pr["error"] == nil && t.PRNumber != 0 {
		fmt.Fprintf(&text, "\nPR: %v files, %v lines, CI %v", pr["files"], pr["lines"], pr["ci"])
	}
	if len(folders) > 0 {
		fmt.Fprintf(&text, "\nChanged folders: %s", strings.Join(folders, ", "))
	}
	data := map[string]any{"name": t.ID, "state": string(t.State), "paused": t.Paused,
		"pause_reason": t.PausedReason, "epic": s.epicOf(ctx, t), "issues_open": issues, "branch": t.Branch, "pr": pr, "merged": t.Merged,
		"folders": folders, "waiting_for": waiting, "characters": chars, "missions": []string{}, "waits": []string{}, "usage": "-"}
	if s.o.Oversight != nil {
		paused, reason, queue, err := s.o.Oversight.Status(ctx, t.Workspace, t.ID)
		if err != nil {
			return fail(err)
		}
		data["autonomy_paused"], data["autonomy_reason"], data["approval_queue"] = paused, reason, queue
		if paused {
			fmt.Fprintf(&text, "\nAutonomy of the Table Master paused: %s", reason)
		}
		if queue != "" {
			fmt.Fprintf(&text, "\n%s", queue)
		}
	}
	data["last_delivery_at"] = ""
	if at, err := s.LastDelivery(ctx, t.Workspace, t.ID); err == nil && !at.IsZero() {
		data["last_delivery_at"] = at.UTC().Format(time.RFC3339)
		fmt.Fprintf(&text, "\nLast useful delivery: %s", at.UTC().Format("2006-01-02 15:04 UTC"))
	}
	data["proofs"] = s.proofsOf(ctx, t)
	line := strings.TrimRight(text.String(), "\n")
	data["text"] = line
	return data, line, nil
}

// issuesOf are the open issues under the epic of the table; an error says
// why they could not be read, which is not the same as having none
// (a map with the key error instead of the list).
func (s *Service) issuesOf(ctx context.Context, t Table) any {
	d, ok := s.o.GitHub.(Details)
	if !ok {
		return map[string]string{"error": "the repository host of this run does not list issues"}
	}
	if err := s.gate("table status"); err != nil {
		return map[string]string{"error": CleanText(err.Error())}
	}
	rctx, cancel := clock.WithTimeout(ctx, s.clk, epicReadTimeout)
	defer cancel()
	issues, err := d.OpenIssues(rctx, t.Workspace, t.Epic)
	if err != nil {
		return map[string]string{"error": CleanText(err.Error())}
	}
	out := make([]map[string]any, 0, len(issues))
	for _, i := range issues {
		out = append(out, map[string]any{"number": i.Number, "title": CleanText(i.Title)})
	}
	return out
}

// prOf is the pull request of the table: its number and, when the repository
// host answers, the files, the lines and the CI.
func (s *Service) prOf(ctx context.Context, t Table) map[string]any {
	pr := map[string]any{"number": t.PRNumber, "files": 0, "lines": 0, "ci": "none"}
	if t.PRNumber == 0 {
		return pr
	}
	d, ok := s.o.GitHub.(Details)
	if !ok {
		pr["error"] = "the repository host of this run does not read pull requests"
		return pr
	}
	if err := s.gate("table status"); err != nil {
		pr["error"] = CleanText(err.Error())
		return pr
	}
	rctx, cancel := clock.WithTimeout(ctx, s.clk, epicReadTimeout)
	defer cancel()
	st, err := d.PullStats(rctx, t.Workspace, t.PRNumber)
	if err != nil {
		pr["error"] = CleanText(err.Error())
		return pr
	}
	pr["files"], pr["lines"], pr["ci"] = st.Files, st.Lines, st.CI
	return pr
}

// maxFolders is how many folders `table status` shows.
const maxFolders = 20

// foldersOf are the folders the branch of the table changes against the commit
// it started from, read from the mirror (first the ones with more files, then by
// name), at most maxFolders.
func (s *Service) foldersOf(ctx context.Context, t Table) []string {
	out := []string{}
	if t.BaseCommit == "" {
		return out
	}
	mirror, err := s.o.Workspaces.OpenMirror(ctx, t.Workspace)
	if err != nil {
		return out
	}
	head, err := mirror.ResolveRef(ctx, "refs/heads/"+t.Branch)
	if err != nil {
		return out
	}
	changes, err := mirror.DiffNameStatus(ctx, t.BaseCommit, string(head))
	if err != nil {
		return out
	}
	count := map[string]int{}
	for _, c := range changes {
		dir := "."
		if i := strings.LastIndexByte(c.Path, '/'); i >= 0 {
			dir = c.Path[:i]
		}
		count[CleanText(dir)]++
	}
	for dir := range count {
		out = append(out, dir)
	}
	sort.Slice(out, func(i, j int) bool {
		if count[out[i]] != count[out[j]] {
			return count[out[i]] > count[out[j]]
		}
		return out[i] < out[j]
	})
	if len(out) > maxFolders {
		out = out[:maxFolders]
	}
	return out
}

// proofsOf is the proof of each criterion of the missions of the table, as the
// engine has it: whether it checked the item on the issue and where the proof
// came from ("verificada pelo motor" or "declarada por <personagem>"). It is
// the only thing that says a criterion is proved: the body of an issue is edited
// by anyone.
func (s *Service) proofsOf(ctx context.Context, t Table) any {
	if s.o.Proofs == nil {
		return []ProofState{}
	}
	ps, err := s.o.Proofs.Of(ctx, t.Workspace, t.ID)
	if err != nil {
		return map[string]string{"error": CleanText(err.Error())}
	}
	if ps == nil {
		ps = []ProofState{}
	}
	return ps
}

// epicReadTimeout is how long `table status` waits for the epic.
const epicReadTimeout = 10 * time.Second

// epicOf is the epic of a table for `table status`: its number, its title and
// the criteria written in the section `## Critérios` of its body, read from
// the repository host now. When it cannot be read the criteria are empty and
// criteria_error says why: the Grand Master must not take that for an epic
// without criteria.
func (s *Service) epicOf(ctx context.Context, t Table) map[string]any {
	e := map[string]any{"number": t.Epic, "title": CleanText(t.EpicTitle), "criteria": []string{}}
	if err := s.gate("table status"); err != nil {
		e["criteria_error"] = err.Error()
		return e
	}
	rctx, cancel := clock.WithTimeout(ctx, s.clk, epicReadTimeout)
	defer cancel()
	epic, err := s.o.GitHub.Epic(rctx, t.Workspace, t.Epic)
	if err != nil {
		e["criteria_error"] = err.Error()
		return e
	}
	if epic.Title != "" {
		e["title"] = CleanText(epic.Title)
	}
	c, err := ParseCriteria(epic.Body)
	if err != nil {
		e["criteria_error"] = err.Error()
		return e
	}
	e["criteria"] = c
	return e
}

func (s *Service) handleClose(ctx context.Context, c *localchan.Call) localchan.Response {
	gm, refused := s.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre table close "<table>" [--cancel --reason "<text>"]`, "")
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
	verb := "closed"
	if res.Table.State == StateCancelled {
		verb = "cancelled"
	}
	text := fmt.Sprintf("table %s %s", res.Table.ID, verb)
	if len(res.Retained) > 0 {
		text += "; worktrees kept by decision: " + strings.Join(res.Retained, ", ")
	}
	return localchan.OK(map[string]any{"text": text, "name": res.Table.ID, "state": string(res.Table.State), "retained": orEmpty(res.Retained)})
}
