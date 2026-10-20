package ghcli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
)

// tablePR finds the PR of the table of who asks; a table with none, or the
// Grand Master (who has no table), is refused with no_pr.
func (h *Handlers) tablePR(ctx context.Context, who caller) (int, *localchan.Response) {
	fail := func(code, msg, hint string) (int, *localchan.Response) {
		r := localchan.Fail(code, msg, hint)
		return 0, &r
	}
	if who.actor.Kind != github.ActorTable {
		return fail("table_required", "the pull request commands are for the characters of a table", "")
	}
	if h.PRs == nil {
		return fail("no_pr", "your table has no pull request", "open it with `mestre pr open`")
	}
	n, ok, err := h.PRs.PRNumber(ctx, who.actor.Table)
	switch {
	case err != nil:
		return fail(localchan.CodeInternal, "the pull request of the table could not be read", "")
	case !ok || n == 0:
		return fail("no_pr", "your table has no pull request", "open it with `mestre pr open`")
	}
	return n, nil
}

func (h *Handlers) prStatus(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.caller(ctx, c)
	if bad != nil {
		return *bad
	}
	n, bad := h.tablePR(ctx, who)
	if bad != nil {
		return *bad
	}
	st, err := who.prs.Status(ctx, who.actor, who.repo, n)
	if err != nil {
		return refusal(err)
	}
	checks := make([]map[string]any, 0, len(st.Checks))
	for _, ch := range st.Checks {
		checks = append(checks, map[string]any{"name": ch.Name, "state": ch.State})
	}
	reviews := make([]map[string]any, 0, len(st.Reviews))
	for _, r := range st.Reviews {
		reviews = append(reviews, map[string]any{"by": r.By, "state": r.State})
	}
	return localchan.OK(map[string]any{"number": st.Number, "state": st.State, "draft": st.Draft, "head_sha": st.HeadSHA, "checks": checks, "reviews": reviews,
		"mergeable": st.Mergeable, "blocked_by": st.BlockedBy, "unresolved_comments": st.Unresolved, "url": st.URL})
}

func (h *Handlers) prComment(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.caller(ctx, c)
	if bad != nil {
		return *bad
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return usage("pr comment takes the text")
	}
	n, bad := h.tablePR(ctx, who)
	if bad != nil {
		return *bad
	}
	got, err := who.prs.Comment(ctx, who.actor, who.repo, n, args[0], c.Request.ID)
	if err != nil {
		return refusal(err)
	}
	return localchan.OK(map[string]any{"number": n, "comment_id": got.ID})
}

func (h *Handlers) prReply(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.caller(ctx, c)
	if bad != nil {
		return *bad
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 2 || strings.TrimSpace(args[1]) == "" {
		return usage("pr reply takes the id of the comment and the text")
	}
	id, err := strconv.ParseInt(strings.TrimSpace(args[0]), 10, 64)
	if err != nil || id <= 0 {
		return usage("the comment is a number (see `mestre pr status --json`)")
	}
	n, bad := h.tablePR(ctx, who)
	if bad != nil {
		return *bad
	}
	got, err := who.prs.Reply(ctx, who.actor, who.repo, n, id, args[1])
	if err != nil {
		return refusal(err)
	}
	return localchan.OK(map[string]any{"number": n, "comment_id": got})
}

func (h *Handlers) prNote(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.caller(ctx, c)
	if bad != nil {
		return *bad
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 2 || strings.TrimSpace(args[1]) == "" {
		return usage("pr note takes <file>:<line> and the text")
	}
	i := strings.LastIndexByte(args[0], ':')
	if i <= 0 {
		return usage("pr note takes <file>:<line>")
	}
	line, err := strconv.Atoi(args[0][i+1:])
	if err != nil || line <= 0 {
		return usage("the line is a number")
	}
	n, bad := h.tablePR(ctx, who)
	if bad != nil {
		return *bad
	}
	id, err := who.prs.Note(ctx, who.actor, who.repo, n, args[0][:i], line, args[1])
	switch {
	case errors.Is(err, github.ErrOutsideDiff):
		return usage("%s is not in the diff of the pull request", args[0])
	case errors.Is(err, github.ErrHeadChanged):
		return localchan.Fail("head_changed", "the head of the pull request changed while the note was checked", "read the pull request again and repeat")
	case err != nil:
		return refusal(err)
	}
	return localchan.OK(map[string]any{"number": n, "comment_id": id})
}

func (h *Handlers) prOpen(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.caller(ctx, c)
	if bad != nil {
		return *bad
	}
	if who.actor.Kind != github.ActorTable {
		return localchan.Fail("table_required", "the pull request commands are for the characters of a table", "")
	}
	in := github.POpen{Class: who.char.Class, Draft: flagBool(c, "draft"), Op: c.Request.ID}
	in.Title, _ = flagString(c, "title")
	in.Body, _ = flagString(c, "body")
	if h.PRs != nil {
		n, ok, err := h.PRs.PRNumber(ctx, who.actor.Table)
		if err != nil {
			return localchan.Fail(localchan.CodeInternal, "the pull request of the table could not be read", "")
		}
		if ok {
			in.Existing = n
		}
	}
	got, err := who.prs.Open(ctx, who.actor, who.repo, in)
	var part *github.PartialError
	if errors.As(err, &part) && part.Kind == "pull request" && h.Opened != nil {
		// the PR exists: the table must know it, so that it is not opened twice
		_ = h.Opened(ctx, who.actor.Table, part.Number, "")
	}
	if err != nil {
		h.refused(ctx, who.char, err)
		return refusal(err)
	}
	if h.Opened != nil {
		if err := h.Opened(ctx, who.actor.Table, got.Number, got.HeadSHA); err != nil {
			return localchan.Fail(localchan.CodeInternal, fmt.Sprintf("the pull request #%d was opened but the table was not told", got.Number), "tell the Grand Master")
		}
	}
	h.executed(ctx, who.char, "mestre pr open")
	return localchan.OK(map[string]any{"number": got.Number, "url": got.URL, "head_sha": got.HeadSHA})
}

// executed tells the decrees that the engine sent the branch for the character;
// a failure to register is not a failure of the command, which was done.
func (h *Handlers) executed(ctx context.Context, c characters.Character, action string) {
	if h.Executed == nil {
		return
	}
	_ = h.Executed(ctx, c, action)
}

func (h *Handlers) prUpdate(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.caller(ctx, c)
	if bad != nil {
		return *bad
	}
	n, bad := h.tablePR(ctx, who)
	if bad != nil {
		return *bad
	}
	in := github.PUpdate{Class: who.char.Class, Force: flagBool(c, "force"), Ready: flagBool(c, "ready")}
	if v, ok := flagString(c, "title"); ok {
		in.Title = &v
	}
	if v, ok := flagString(c, "body"); ok {
		in.Body = &v
	}
	if name, ok := flagString(c, "block"); ok {
		args, _ := c.Request.Strings()
		if len(args) != 1 {
			return usage("--block takes the text of the block")
		}
		in.Block, in.BlockText = name, args[0]
		in.ExpectHead, _ = flagString(c, "expect-head")
		if in.ExpectHead == "" {
			return usage("--block takes --expect-head <sha>")
		}
		if in.Title != nil || in.Body != nil || in.Force || in.Ready {
			return usage("--block is used alone")
		}
	}
	got, err := who.prs.Update(ctx, who.actor, who.repo, n, in)
	if err != nil {
		h.refused(ctx, who.char, err)
		return refusal(err)
	}
	if in.Block == "" {
		h.executed(ctx, who.char, "mestre pr update")
	}
	return localchan.OK(map[string]any{"number": got.Number, "url": got.URL, "head_sha": got.HeadSHA})
}
