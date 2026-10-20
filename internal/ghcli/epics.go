package ghcli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/epicdeps"
)

// EpicDeps is the dependency between epics (*epicdeps.Service): what `gh epic
// update` tells it.
type EpicDeps interface {
	Depend(ctx context.Context, by characters.Character, epic, on int) (epicdeps.Result, error)
	// Release drops a dependency (the undo of a command that did not end).
	Release(ctx context.Context, by characters.Character, epic, on int) error
	// EpicClosed is told that an epic was closed on GitHub.
	EpicClosed(ctx context.Context, workspace string, epic int, notPlanned bool) error
}

// epic is `mestre gh epic create|update|list` (docs/cli/grao-mestre.md#mestre-gh):
// only the Grão-Mestre, with its own App. An epic is an issue with the label
// `epico` (docs/decisoes/0015-epico-como-rotulo.md).
func (h *Handlers) epic(ctx context.Context, c *localchan.Call) localchan.Response {
	args, ok := c.Request.Strings()
	if !ok || len(args) == 0 {
		return usage("usage: mestre gh epic create|update|list …")
	}
	switch args[0] {
	case "list":
		return h.read(h.epicList)(ctx, c)
	case "create", "update":
		return h.write("gh epic "+args[0], h.epicWrite)(ctx, c)
	}
	return usage("gh epic takes create, update or list, not %q", args[0])
}

// grandMaster is the caller when it is the Grão-Mestre of its workspace.
func (h *Handlers) grandMaster(ctx context.Context, c *localchan.Call) (caller, *localchan.Response) {
	who, bad := h.caller(ctx, c)
	if bad != nil {
		return caller{}, bad
	}
	if who.char.Role != characters.RoleGrandMaster {
		r := localchan.Fail("level_denied", "only the Grand Master of the workspace works on the epics", "")
		return caller{}, &r
	}
	return who, nil
}

func (h *Handlers) epicWrite(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.grandMaster(ctx, c)
	if bad != nil {
		return *bad
	}
	args, _ := c.Request.Strings()
	if args[0] == "create" {
		return h.epicCreate(ctx, c, who, args[1:])
	}
	return h.epicUpdate(ctx, c, who, args[1:])
}

func (h *Handlers) epicCreate(ctx context.Context, c *localchan.Call, who caller, rest []string) localchan.Response {
	if len(rest) != 0 {
		return usage("usage: mestre gh epic create --title \"<…>\" (--body \"<…>\" | --body-file <file>) [--label <label>]…")
	}
	title, _ := flagString(c, "title")
	if strings.TrimSpace(title) == "" {
		return usage("--title is required")
	}
	body, _ := flagString(c, "body")
	// the label of the epics is one, spelled as the engine reads it
	labels := []string{github.EpicLabel}
	for _, l := range flagList(c, "label") {
		if !strings.EqualFold(strings.TrimSpace(l), github.EpicLabel) {
			labels = append(labels, l)
		}
	}
	made, err := who.svc.Create(ctx, who.actor, who.repo, github.IssueCreate{Title: title, Body: body, Labels: labels, Op: c.Request.ID})
	if err != nil {
		return refusal(err)
	}
	return localchan.OK(map[string]any{"number": made.Number, "id": made.ID, "url": made.URL,
		"text": fmt.Sprintf("épico #%d criado: %s", made.Number, made.URL)})
}

// isEpic reads an issue and says whether it is an epic.
func (h *Handlers) isEpic(ctx context.Context, who caller, n int) (github.IssueData, localchan.Response, bool) {
	d, err := who.svc.API.IssueData(ctx, who.repo, n)
	switch {
	case errors.Is(err, github.ErrNotFound):
		return d, localchan.Fail("not_found", fmt.Sprintf("#%d does not exist", n), "see `mestre gh epic list`").WithExit(localchan.ExitNotFound), false
	case err != nil:
		return d, refusal(err), false
	case d.IsPull || !slices.Contains(d.Labels, github.EpicLabel):
		return d, localchan.Fail("not_found", fmt.Sprintf("#%d is not an epic (it has no label %s)", n, github.EpicLabel), "see `mestre gh epic list`").WithExit(localchan.ExitNotFound), false
	}
	return d, localchan.Response{}, true
}

func (h *Handlers) epicUpdate(ctx context.Context, c *localchan.Call, who caller, rest []string) localchan.Response {
	if len(rest) != 1 {
		return usage("usage: mestre gh epic update <n> [--title …] [--body … | --body-file …] [--depends-on <m>]… [--state open|closed]")
	}
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(rest[0]), "#"))
	if err != nil || n <= 0 {
		return usage("the epic is a number")
	}
	var depends []int
	for _, raw := range flagList(c, "depends-on") {
		m, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(raw), "#"))
		if err != nil || m <= 0 {
			return usage("--depends-on is the number of an epic")
		}
		depends = append(depends, m)
	}
	in := github.IssueUpdate{}
	if v, ok := flagString(c, "title"); ok {
		in.Title = &v
	}
	if v, ok := flagString(c, "body"); ok {
		in.Body = &v
	}
	in.State, _ = flagString(c, "state")
	switch in.State {
	case "", "open", "closed":
	default:
		return usage("--state is open or closed")
	}
	if in.Title == nil && in.Body == nil && in.State == "" && len(depends) == 0 {
		return usage("nothing to change: give --title, --body, --state or --depends-on")
	}
	if (len(depends) > 0 || in.State == "closed") && h.Deps == nil {
		return unavailable("the dependencies between epics are not available in this run: nothing was changed")
	}
	cur, resp, ok := h.isEpic(ctx, who, n)
	if !ok {
		return resp
	}
	for _, m := range depends {
		if _, resp, ok := h.isEpic(ctx, who, m); !ok {
			return resp
		}
	}
	// An epic that is closed already stays as it was closed (the reason is not
	// rewritten) and the tables are told the way it was closed.
	wasClosed := cur.State != "open"
	notPlanned := cur.StateReason == "not_planned"
	if in.State == "closed" {
		if wasClosed {
			in.State = ""
		} else {
			in.Reason = "completed"
		}
	}
	// The dependencies are the engine's own and are kept first; if one fails or
	// GitHub refuses afterwards, the ones this command made are dropped.
	var deps []map[string]any
	var made []int
	undo := func() {
		for _, m := range made {
			// best effort: the one left stays pending and the Grand Master drops it
			_ = h.Deps.Release(context.WithoutCancel(ctx), who.char, n, m)
		}
	}
	for _, m := range depends {
		res, err := h.Deps.Depend(ctx, who.char, n, m)
		var ring *epicdeps.CycleError
		switch {
		case errors.As(err, &ring):
			undo()
			return localchan.Fail("dependency_cycle", ring.Error(), "ask the users which comes first").WithExit(localchan.ExitConflict)
		case errors.Is(err, epicdeps.ErrNotGrandMaster):
			undo()
			return localchan.Fail("level_denied", "only the Grand Master of the workspace works on the epics", "")
		case errors.Is(err, epicdeps.ErrSelf):
			undo()
			return usage("an epic cannot depend on itself")
		case err != nil:
			undo()
			return localchan.Fail(localchan.CodeInternal, "the dependency was not kept: "+err.Error(), "")
		}
		if res.New {
			made = append(made, m)
		}
		deps = append(deps, map[string]any{"epic": m, "state": string(res.State), "waiting": res.Waiting})
	}
	data := map[string]any{"number": n, "depends_on": orEmptyMaps(deps)}
	if in.Title != nil || in.Body != nil || in.State != "" {
		got, err := who.svc.Update(ctx, who.actor, who.repo, n, in)
		if err != nil {
			undo()
			return refusal(err)
		}
		data["state"] = got.State
	}
	if flagStr(c, "state") == "closed" {
		if err := h.Deps.EpicClosed(ctx, who.char.Workspace, n, wasClosed && notPlanned); err != nil {
			return localchan.Fail(localchan.CodeInternal, fmt.Sprintf("the epic #%d is closed on GitHub but the tables that wait for it were not told: %v", n, err), "run the command again")
		}
	}
	data["text"] = fmt.Sprintf("épico #%d atualizado", n)
	return localchan.OK(data)
}

func flagStr(c *localchan.Call, name string) string {
	v, _ := flagString(c, name)
	return v
}

func orEmptyMaps(m []map[string]any) []map[string]any {
	if m == nil {
		return []map[string]any{}
	}
	return m
}

func (h *Handlers) epicList(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.grandMaster(ctx, c)
	if bad != nil {
		return *bad
	}
	args, _ := c.Request.Strings()
	if len(args) != 1 {
		return usage("usage: mestre gh epic list [--state open|closed|all]")
	}
	state, _ := flagString(c, "state")
	switch state {
	case "":
		state = "open"
	case "open", "closed", "all":
	default:
		return usage("--state is open, closed or all")
	}
	all, err := who.svc.API.Issues(ctx, who.repo, state)
	if err != nil {
		return refusal(err)
	}
	out := []map[string]any{}
	var text strings.Builder
	for _, d := range all {
		if !d.IsPull && slices.Contains(d.Labels, github.EpicLabel) {
			out = append(out, issueView(d))
			fmt.Fprintf(&text, "- #%d %s (%s)\n", d.Number, d.Title, d.State)
		}
	}
	if len(out) == 0 {
		text.WriteString("nenhum épico\n")
	}
	return localchan.OK(map[string]any{"epics": out, "text": strings.TrimRight(text.String(), "\n")})
}
