package scrolls

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
)

// Approvals opens the request of approval that deleting a scroll needs when a
// character asks for it (milestone 5 brings the flow; until then Delete
// answers 9 without a number).
type Approvals interface {
	RequestDelete(ctx context.Context, by Actor, n Note) (request int64, err error)
}

// DeleteGate is what an Approvals may also be: it spends the approval of the
// deletion if there is one for the scroll in the state it is in now (granted),
// or leaves the request open and says which. It replaces RequestDelete, which
// can only open the request.
type DeleteGate interface {
	DeleteAllowed(ctx context.Context, by Actor, n Note) (granted bool, request int64, err error)
}

// Handlers are the commands of the scrolls to register in the local forwarder
// of the Citadel (the "casca" of stage 7: docs/cli/comunicacao.md has the
// syntax, the CLI answers the syntax and the level, the engine does the
// rest). The scroll is always one of the table of the caller, and the
// Grand Master has no table.
func (s *Service) Handlers(reg characters.Registry, ap Approvals) map[string]localchan.Handler {
	h := handlers{s: s, reg: reg, ap: ap}
	return map[string]localchan.Handler{
		"note create": localchan.HandlerFunc(h.create),
		"note read":   localchan.HandlerFunc(h.read),
		"note write":  localchan.HandlerFunc(h.write),
		"note edit":   localchan.HandlerFunc(h.edit),
		"note stack":  localchan.HandlerFunc(h.stack),
		"note delete": localchan.HandlerFunc(h.delete),
	}
}

// Register registers the handlers in the local forwarder of the Citadel.
func (s *Service) Register(fwd *localchan.LocalForwarder, reg characters.Registry, ap Approvals) {
	for cmd, h := range s.Handlers(reg, ap) {
		fwd.Handle(cmd, h)
	}
}

type handlers struct {
	s   *Service
	reg characters.Registry
	ap  Approvals
}

// Refusal turns an error of this package into the answer of the CLI. The
// codes are the ones of docs/cli/comunicacao.md#mestre-note.
func Refusal(err error) localchan.Response {
	var sz *SizeError
	switch {
	case errors.Is(err, ErrNoTable):
		return localchan.Fail("level_denied", "scrolls exist only inside the tables", "the Grand Master has no scrolls")
	case errors.Is(err, ErrNotVisible):
		return localchan.Fail("not_visible", err.Error(), "see `mestre list`")
	case errors.Is(err, ErrRevision):
		return localchan.Fail("rev_conflict", err.Error(), "read it again with `mestre note read` and redo the change").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrEditAmbiguous):
		return localchan.Fail("edit_ambiguous", err.Error(), "use a longer piece, one that appears only once").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrEditNotFound):
		return localchan.Fail("edit_not_found", err.Error(), "check the piece with `mestre note read`").WithExit(localchan.ExitNotFound)
	case errors.Is(err, ErrNameTaken):
		return localchan.Fail("name_taken", err.Error(), "choose another name").WithExit(localchan.ExitConflict)
	case errors.As(err, &sz):
		return localchan.Fail("too_large", sz.Error(), "reduce the content or split it into two scrolls")
	case errors.Is(err, ErrOtherTable):
		return localchan.Fail("different_tables", err.Error(), "a tome groups scrolls of the same table").WithExit(localchan.ExitUsage)
	case errors.Is(err, ErrLevelDenied):
		return localchan.Fail("level_denied", err.Error(), "")
	case errors.Is(err, ErrBadRequest):
		return localchan.Fail(localchan.CodeUsage, err.Error(), "")
	}
	return localchan.Fail(localchan.CodeInternal, err.Error(), "")
}

// scope finds who calls and the table the scrolls are in.
func (h handlers) scope(ctx context.Context, c *localchan.Call) (characters.Character, Actor, *localchan.Response) {
	if !c.Caller.Identified {
		r := localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this process as a process of a character", "run mestre debug")
		return characters.Character{}, Actor{}, &r
	}
	x, err := h.reg.Get(ctx, c.Caller.AgentID)
	if err != nil {
		r := localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this character", "run mestre debug")
		return characters.Character{}, Actor{}, &r
	}
	if x.Role == characters.RoleGrandMaster || x.Table == "" {
		r := Refusal(ErrNoTable)
		return characters.Character{}, Actor{}, &r
	}
	return x, Actor{Kind: ByCharacter, ID: x.ID, Name: x.Name}, nil
}

func flagString(c *localchan.Call, name string) string {
	v, _ := c.Request.Flags[name].(string)
	return v
}

func flagBool(c *localchan.Call, name string) bool {
	v, _ := c.Request.Flags[name].(bool)
	return v
}

// expectRev reads --expect-rev.
func expectRev(c *localchan.Call) (*int64, *localchan.Response) {
	v := flagString(c, "expect-rev")
	if v == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		r := localchan.Fail(localchan.CodeUsage, fmt.Sprintf("--expect-rev %q is not a revision number", v), "")
		return nil, &r
	}
	return &n, nil
}

func noteData(n Note, text string) map[string]any {
	return map[string]any{"text": text, "name": n.Name, "id": n.ID, "stack": n.TomeName, "rev": n.Revision, "updated_at": n.UpdatedAt.Format(time.RFC3339), "updated_by": n.Author}
}

func (h handlers) create(ctx context.Context, c *localchan.Call) localchan.Response {
	x, a, refused := h.scope(ctx, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) > 1 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre note create ["<content>"] [--name "<name>"] [--stack "<tome>"]`, "")
	}
	content := ""
	if len(args) == 1 {
		content = args[0] // verbatim: the text is a Markdown the agent wrote
	}
	n, err := h.s.Create(ctx, CreateRequest{Workspace: x.Workspace, Table: x.Table, Actor: a, Content: content, Name: flagString(c, "name"), Stack: flagString(c, "stack")})
	if err != nil {
		return Refusal(err)
	}
	return localchan.OK(noteData(n, fmt.Sprintf("pergaminho criado: %s (%s, revisão %d), conectado a você", n.Name, n.ID, n.Revision)))
}

func (h handlers) read(ctx context.Context, c *localchan.Call) localchan.Response {
	x, a, refused := h.scope(ctx, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre note read "<name>"`, "")
	}
	n, content, err := h.s.Read(ctx, ReadRequest{Workspace: x.Workspace, Table: x.Table, Actor: a, Name: args[0]})
	if err != nil {
		return Refusal(err)
	}
	d := noteData(n, content)
	d["content"] = content
	return localchan.OK(d)
}

func (h handlers) write(ctx context.Context, c *localchan.Call) localchan.Response {
	x, a, refused := h.scope(ctx, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 2 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre note write "<name>" "<content>" [--append] [--expect-rev <n>]`, "")
	}
	rev, bad := expectRev(c)
	if bad != nil {
		return *bad
	}
	n, err := h.s.Write(ctx, WriteRequest{Workspace: x.Workspace, Table: x.Table, Actor: a, Name: args[0], Content: args[1], Append: flagBool(c, "append"), ExpectRev: rev})
	if err != nil {
		return Refusal(err)
	}
	return localchan.OK(noteData(n, fmt.Sprintf("%s gravado (revisão %d)", n.Name, n.Revision)))
}

func (h handlers) edit(ctx context.Context, c *localchan.Call) localchan.Response {
	x, a, refused := h.scope(ctx, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	_, hasOld := c.Request.Flags["old"]
	_, hasNew := c.Request.Flags["new"]
	if !ok || len(args) != 1 || !hasOld || !hasNew {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre note edit "<name>" --old "<piece>" --new "<piece>" [--expect-rev <n>]`, "")
	}
	rev, bad := expectRev(c)
	if bad != nil {
		return *bad
	}
	n, err := h.s.Edit(ctx, EditRequest{Workspace: x.Workspace, Table: x.Table, Actor: a, Name: args[0], Old: flagString(c, "old"), New: flagString(c, "new"), ExpectRev: rev})
	if err != nil {
		return Refusal(err)
	}
	return localchan.OK(noteData(n, fmt.Sprintf("%s alterado (revisão %d)", n.Name, n.Revision)))
}

func (h handlers) stack(ctx context.Context, c *localchan.Call) localchan.Response {
	x, a, refused := h.scope(ctx, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) < 2 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre note stack "<tome>" "<name>" ["<name>" …]`, "")
	}
	t, ns, err := h.s.Stack(ctx, StackRequest{Workspace: x.Workspace, Table: x.Table, Actor: a, Tome: args[0], Names: args[1:]})
	if err != nil {
		return Refusal(err)
	}
	names := make([]string, len(ns))
	for i, n := range ns {
		names[i] = n.Name
	}
	return localchan.OK(map[string]any{"text": fmt.Sprintf("tomo %s: %s", t.Name, strings.Join(names, ", ")), "stack": t.Name, "notes": names})
}

func (h handlers) delete(ctx context.Context, c *localchan.Call) localchan.Response {
	x, a, refused := h.scope(ctx, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre note delete "<name>"`, "")
	}
	err := h.s.Delete(ctx, DeleteRequest{Workspace: x.Workspace, Table: x.Table, Actor: a, Name: args[0]})
	switch {
	case err == nil:
		return localchan.OK(map[string]any{"text": "pergaminho apagado"})
	case !errors.Is(err, ErrApproval):
		return Refusal(err)
	}
	// A destructive action: nothing is deleted. The request of approval is the
	// flow of milestone 5; without it the answer is still the pending 9.
	n, nerr := h.s.find(ctx, x.Workspace, x.Table, a, args[0])
	if nerr != nil {
		return Refusal(nerr)
	}
	msg, id := "deleting a scroll needs an explicit request of an authorized user; nothing was deleted", int64(0)
	if gate, ok := h.ap.(DeleteGate); ok && h.ap != nil {
		granted, req, aerr := gate.DeleteAllowed(ctx, a, n)
		if aerr != nil {
			return Refusal(aerr)
		}
		if granted {
			if err := h.s.Delete(ctx, DeleteRequest{Workspace: x.Workspace, Table: x.Table, Actor: a, Name: args[0], Approved: true, ExpectKey: n.Key, ExpectRevision: n.Revision}); err != nil {
				return Refusal(err)
			}
			return localchan.OK(map[string]any{"text": "pergaminho apagado"})
		}
		id, msg = req, fmt.Sprintf("approval request #%d is open; nothing was deleted", req)
	} else if h.ap != nil {
		var aerr error
		if id, aerr = h.ap.RequestDelete(ctx, a, n); aerr != nil {
			return Refusal(aerr)
		}
		msg = fmt.Sprintf("approval request #%d created; nothing was deleted", id)
	}
	r := localchan.Fail("approval_required", msg, "the result arrives in your inbox").WithExit(localchan.ExitPending)
	r.Error.Details = map[string]any{"request": id}
	return r
}
