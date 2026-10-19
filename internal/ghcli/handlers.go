package ghcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Characters is the registry of the characters (characters.Registry).
type Characters interface {
	Get(ctx context.Context, id string) (characters.Character, error)
}

// Clients gives the GitHub API with the App of who asks: the Grão-Mestre, the
// Mestre, or the App of the class of the character (the App "Mestre" until it is
// ready). The token is made when the API is used and never leaves it.
type Clients interface {
	For(ctx context.Context, c characters.Character, repo string) (github.PullAPI, error)
}

// Repos gives the repository of a workspace.
type Repos interface {
	Repo(ctx context.Context, workspace string) (string, error)
}

// PRs says which PR is the table's (the one `pr open` made).
type PRs interface {
	PRNumber(ctx context.Context, tableKey string) (n int, ok bool, err error)
}

// Handlers are the commands `issue` and `pr`, to register in the local
// forwarder of the Citadel.
type Handlers struct {
	PRs PRs
	// Lease gates every call to GitHub; without it every command is refused.
	Lease Lease
	// Locked says the Mestre is locked (/travar): nothing is done on GitHub. nil: never.
	Locked func(ctx context.Context) bool
	// Quota, when set, holds what changes data back while the App is low.
	Quota      Quota
	Characters Characters
	Clients    Clients
	Repos      Repos
	Tables     github.Tables
	Objects    *github.Objects
	Clock      clock.Clock
}

// Register registers the handlers.
func (h *Handlers) Register(fwd *localchan.LocalForwarder) {
	for name, f := range map[string]handler{
		"issue list": h.read(h.issueList), "pr status": h.read(h.prStatus),
		"issue create": h.write("issue create", h.issueCreate), "issue update": h.write("issue update", h.issueUpdate), "issue comment": h.write("issue comment", h.issueComment),
		"pr comment": h.write("pr comment", h.prComment), "pr reply": h.write("pr reply", h.prReply), "pr note": h.write("pr note", h.prNote),
	} {
		fwd.Handle(name, localchan.HandlerFunc(f))
	}
}

// TableKey is how the guard knows a table: its workspace and its id (the ids
// of two workspaces may be the same).
func TableKey(c characters.Character) string { return c.Workspace + "/" + c.Table }

// caller is who asks, with the repository it works on, the actor the guard
// judges and the service over its App.
type caller struct {
	char  characters.Character
	repo  string
	actor github.Actor
	svc   *github.IssueService
	prs   *github.PRService
}

func (h *Handlers) caller(ctx context.Context, c *localchan.Call) (caller, *localchan.Response) {
	fail := func(code, msg, hint string) (caller, *localchan.Response) {
		r := localchan.Fail(code, msg, hint)
		return caller{}, &r
	}
	if !c.Caller.Identified {
		return fail(localchan.CodeNotIdentified, "the engine does not know this process as a process of a character", "run mestre debug")
	}
	ch, err := h.Characters.Get(ctx, c.Caller.AgentID)
	if err != nil {
		return fail(localchan.CodeNotIdentified, "the engine does not know this character", "run mestre debug")
	}
	out := caller{char: ch}
	if ch.Role == characters.RoleGrandMaster {
		out.actor = github.Actor{Kind: github.ActorGrandMaster}
		if out.repo, err = h.Repos.Repo(ctx, ch.Workspace); err != nil {
			return fail("not_found", "the workspace has no repository", "")
		}
	} else {
		if ch.Table == "" {
			return fail("table_required", "this command is for the characters of a table", "")
		}
		key := TableKey(ch)
		ref, ok, err := h.Tables.Epic(ctx, key)
		if err != nil {
			return fail(localchan.CodeInternal, "the table of the character could not be read", "")
		}
		if !ok {
			return fail("outside_epic", "your table has no epic registered", "ask the Grand Master")
		}
		out.repo = ref.Repo
		out.actor = github.Actor{Kind: github.ActorTable, Table: key, MestreDaMesa: ch.Role == characters.RoleTableMaster}
	}
	api, err := h.Clients.For(ctx, ch, out.repo)
	if err != nil {
		return fail("github_unavailable", "GitHub is not available for this character now", "try again later")
	}
	out.svc = &github.IssueService{API: api, Objects: h.Objects, Tables: h.Tables, Clock: h.Clock}
	out.prs = &github.PRService{API: api, Objects: h.Objects, Tables: h.Tables}
	return out, nil
}

// refusal turns the error of an action into the answer of the CLI.
func refusal(err error) localchan.Response {
	var ref *github.Refusal
	var lim *github.LimitError
	var part *github.PartialError
	switch {
	case errors.As(err, &part):
		// the issue exists: say so, so that the command is not repeated
		msg := fmt.Sprintf("the issue #%d was created but its setup did not end (%s); do not create it again", part.Number, setupStage(part))
		return localchan.Fail("github_unavailable", msg, "tell the Grand Master")
	case errors.As(err, &ref):
		code := ref.Code
		switch code {
		case github.CodeOtherTable, github.CodeNotInEpic, github.CodeNotAllowed:
			return localchan.Fail("outside_epic", ref.Message, "ask the Grand Master")
		case github.CodeReservedLabel:
			return localchan.Fail("reserved_label", ref.Message, "")
		}
		return localchan.Fail(code, ref.Message, "")
	case errors.As(err, &lim):
		return localchan.Fail("github_unavailable", lim.Error(), "try again after "+lim.RetryAfter.String())
	case errors.Is(err, github.ErrUnavailable):
		return localchan.Fail("github_unavailable", "GitHub is not answering", "try again later")
	case errors.Is(err, github.ErrNotFound):
		return localchan.Fail("not_found", "GitHub does not have it", "")
	}
	return localchan.Fail(localchan.CodeInternal, "the action on GitHub failed", "")
}

func setupStage(p *github.PartialError) string {
	if !p.Registered {
		return "it is not registered to your table"
	}
	return fmt.Sprintf("it is not linked under #%d", p.Parent)
}

func flagString(c *localchan.Call, name string) (string, bool) {
	v, ok := c.Request.Flags[name].(string)
	return v, ok
}

func flagBool(c *localchan.Call, name string) bool {
	v, _ := c.Request.Flags[name].(bool)
	return v
}

func flagList(c *localchan.Call, name string) []string {
	switch v := c.Request.Flags[name].(type) {
	case []string:
		return v
	case []any:
		var out []string
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func usage(format string, a ...any) localchan.Response {
	return localchan.Fail(localchan.CodeUsage, fmt.Sprintf(format, a...), "")
}

// number reads the text of an argument as the number of an issue.
func number(raw json.RawMessage) (int, bool) {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	return n, err == nil && n > 0
}

func (h *Handlers) issueCreate(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.caller(ctx, c)
	if bad != nil {
		return *bad
	}
	title, _ := flagString(c, "title")
	if strings.TrimSpace(title) == "" {
		return usage("--title is required")
	}
	in := github.IssueCreate{Title: title, Labels: flagList(c, "label"), Op: c.Request.ID}
	in.Body, _ = flagString(c, "body")
	in.Fingerprint, _ = flagString(c, "fingerprint")
	if p, ok := flagString(c, "parent"); ok {
		n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(p), "#"))
		if err != nil || n <= 0 {
			return usage("--parent is the number of an issue")
		}
		in.Parent = n
	}
	made, err := who.svc.Create(ctx, who.actor, who.repo, in)
	if err != nil {
		return refusal(err)
	}
	data := map[string]any{"number": made.Number, "id": made.ID, "url": made.URL, "parent": made.Parent}
	if made.SeenAgain {
		data["seen_again"] = true
	}
	if made.RecurrenceOf != 0 {
		data["recurrence_of"] = made.RecurrenceOf
	}
	return localchan.OK(data)
}

func (h *Handlers) issueUpdate(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.caller(ctx, c)
	if bad != nil {
		return *bad
	}
	args := c.Request.Args
	if len(args) != 1 {
		return usage("issue update takes the number of one issue")
	}
	n, ok := number(args[0])
	if !ok {
		return usage("the issue is a number")
	}
	in := github.IssueUpdate{AddLabels: flagList(c, "add-label"), RemoveLabel: flagList(c, "remove-label")}
	if v, ok := flagString(c, "title"); ok {
		in.Title = &v
	}
	if v, ok := flagString(c, "body"); ok {
		in.Body = &v
	}
	in.State, _ = flagString(c, "state")
	in.Reason, _ = flagString(c, "reason")
	switch in.State {
	case "", "open", "closed":
	default:
		return usage("--state is open or closed")
	}
	switch in.Reason {
	case "", "completed", "not_planned":
	default:
		return usage("--reason is completed or not_planned")
	}
	if in.Reason != "" && in.State != "closed" {
		return usage("--reason goes with --state closed")
	}
	got, err := who.svc.Update(ctx, who.actor, who.repo, n, in)
	if err != nil {
		return refusal(err)
	}
	return localchan.OK(issueView(got))
}

func issueView(d github.IssueData) map[string]any {
	labels := d.Labels
	if labels == nil {
		labels = []string{}
	}
	return map[string]any{"number": d.Number, "title": d.Title, "state": d.State, "labels": labels, "url": d.URL}
}

func (h *Handlers) issueComment(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.caller(ctx, c)
	if bad != nil {
		return *bad
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 2 {
		return usage("issue comment takes the number and the text")
	}
	n, ok := number(c.Request.Args[0])
	if !ok {
		return usage("the issue is a number")
	}
	if strings.TrimSpace(args[1]) == "" {
		return usage("the comment is empty")
	}
	got, err := who.svc.Comment(ctx, who.actor, who.repo, n, args[1], c.Request.ID)
	if err != nil {
		return refusal(err)
	}
	return localchan.OK(map[string]any{"number": n, "comment_id": got.ID})
}

func (h *Handlers) issueList(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.caller(ctx, c)
	if bad != nil {
		return *bad
	}
	state, _ := flagString(c, "state")
	switch state {
	case "":
		state = "open"
	case "open", "closed", "all":
	default:
		return usage("--state is open, closed or all")
	}
	var out []map[string]any
	switch {
	case len(c.Request.Args) > 20:
		return usage("name at most 20 issues")
	case len(c.Request.Args) > 0:
		for _, raw := range c.Request.Args {
			n, ok := number(raw)
			if !ok {
				return usage("the issues are numbers")
			}
			d, err := who.svc.API.IssueData(ctx, who.repo, n)
			if err != nil {
				return refusal(err)
			}
			v := issueView(d)
			v["body"] = github.HideMarkers(d.Body)
			if flagBool(c, "comments") {
				cs, err := who.svc.API.Comments(ctx, who.repo, n)
				if err != nil {
					return refusal(err)
				}
				list := make([]map[string]any, 0, len(cs))
				for _, cm := range cs {
					list = append(list, map[string]any{"id": cm.ID, "body": cm.Body})
				}
				v["comments"] = list
			}
			out = append(out, v)
		}
	case flagBool(c, "all"):
		all, err := who.svc.API.Issues(ctx, who.repo, state)
		if err != nil {
			return refusal(err)
		}
		for _, d := range all {
			out = append(out, issueView(d))
		}
	default:
		if who.actor.Kind != github.ActorTable {
			return usage("the Grand Master names issues or uses --all")
		}
		ref, ok, err := h.Tables.Epic(ctx, who.actor.Table)
		if err != nil || !ok {
			return refusal(github.ErrNotFound)
		}
		tree, err := who.svc.ListTree(ctx, who.repo, ref.Epic)
		if err != nil {
			return refusal(err)
		}
		for _, n := range tree {
			if state != "all" && n.State != state {
				continue
			}
			v := issueView(n.IssueData)
			v["parent"] = n.Parent
			out = append(out, v)
		}
	}
	if out == nil {
		out = []map[string]any{}
	}
	return localchan.OK(map[string]any{"issues": out})
}
