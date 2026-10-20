package blocking

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

// Characters finds the character that asks (characters.Registry).
type Characters interface {
	Get(ctx context.Context, id string) (characters.Character, error)
}

// Channel sends a notice to the users (telegram.BlockChannel): it returns the
// number of the notice and how many users it was sent to.
type Channel interface {
	Notify(ctx context.Context, n Notice) (id int64, sent int, err error)
}

// Handler is the command `mestre notify "<message>" --blocked <motive>`
// (docs/cli/grao-mestre.md#mestre-notify): the Grand Master tells the users that a
// table can not go on without them, in the fields of the document of the
// request that blocks. The level is checked here too, because the identity of the
// caller is the engine's own.
type Handler struct {
	Characters Characters
	Channel    Channel
	// Table finds a table of the workspace by its name or by the number of its
	// epic (tables.Service.Find).
	Table func(ctx context.Context, workspace, name string) (tables.Table, error)
	// BlockedBy gives the epics the epic of a table waits for
	// (epicdeps.Service.Waiting); nil: none does.
	BlockedBy func(ctx context.Context, workspace string, epic int) ([]int, error)
	// Owner is the name of the Owner, who acts on a credential.
	Owner func(ctx context.Context, workspace string) string
}

// Register registers the command.
func (h *Handler) Register(fwd *localchan.LocalForwarder) {
	fwd.Handle("notify", localchan.HandlerFunc(h.handle))
}

func flagText(c *localchan.Call, name string) string {
	v, _ := c.Request.Flags[name].(string)
	return v
}

func flagTexts(c *localchan.Call, name string) []string {
	switch l := c.Request.Flags[name].(type) {
	case []string:
		return l
	case []any:
		out := make([]string, 0, len(l))
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func (h *Handler) handle(ctx context.Context, c *localchan.Call) localchan.Response {
	if !c.Caller.Identified {
		return localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this process as a process of a character", "run mestre debug")
	}
	who, err := h.Characters.Get(ctx, c.Caller.AgentID)
	if err != nil {
		return localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this character", "run mestre debug")
	}
	if who.Role != characters.RoleGrandMaster {
		return localchan.Fail("level_denied", "only the Grand Master of the workspace notifies the users", "")
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre notify "<message>" [--table "<table>"] [--blocked <motive> [--checked …] [--why-not …] [--step …]…]`, "")
	}
	blocked := flagText(c, "blocked")
	if blocked == "" {
		for _, f := range []string{"checked", "why-not", "step"} {
			if flagText(c, f) != "" || len(flagTexts(c, f)) > 0 {
				return localchan.Fail(localchan.CodeUsage, "--"+f+" goes with --blocked", "")
			}
		}
		// The plain notice to the panel and the Telegram has no sender yet
		// (docs/desenvolvimento/ligacao-de-producao.md): nobody is told, and the
		// Grand Master hears it.
		return localchan.Fail("unavailable", "notify without --blocked is not available yet: nobody was told", "use --blocked <motive> for a table that can not go on, or `mestre reply` to answer a user")
	}
	motive, err := ParseMotive(blocked)
	if err != nil {
		return localchan.Fail(localchan.CodeUsage, err.Error(), "")
	}
	n := Notice{Workspace: who.Workspace, Motive: motive, Message: args[0], Checked: flagText(c, "checked"), WhyNot: flagText(c, "why-not"), AgentSteps: flagTexts(c, "step")}
	if name := flagText(c, "table"); name != "" {
		t, err := h.Table(ctx, who.Workspace, name)
		switch {
		case errors.Is(err, tables.ErrNoTable):
			return localchan.Fail("not_found", err.Error(), "see `mestre table list`").WithExit(localchan.ExitNotFound)
		case err != nil:
			return localchan.Fail(localchan.CodeInternal, "the table could not be read", "")
		}
		n.Mesa = t.ID
		if h.BlockedBy != nil {
			on, err := h.BlockedBy(ctx, who.Workspace, t.Epic)
			if err != nil {
				return localchan.Fail(localchan.CodeInternal, "the dependencies of the table could not be read", "")
			}
			for _, e := range on {
				n.BlockedBy = append(n.BlockedBy, strconv.Itoa(e))
			}
		}
	}
	var owner string
	if h.Owner != nil {
		owner = h.Owner(ctx, who.Workspace)
	}
	n.Owner = NoticeOwner(motive, owner)
	if motive == Credential {
		n.Steps = []Step{{Text: "cadastrar a chave no cofre", Kind: StepVault}}
	}
	id, sent, err := h.Channel.Notify(ctx, n)
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, fmt.Sprintf("the notice was not sent: %v", err), "")
	}
	if sent == 0 {
		return localchan.Fail("not_delivered", "no user who acts in this workspace could be told (the notice #"+strconv.FormatInt(id, 10)+" is kept for the panel)", "tell a user through another way, or try again").WithExit(localchan.ExitConflict)
	}
	return localchan.OK(map[string]any{
		"text": fmt.Sprintf("aviso %d enviado a %d usuário(s)", id, sent), "id": id,
		"delivered": map[string]any{"panel": 0, "telegram": sent}, "queued": false, "blocked_by": orEmpty(n.BlockedBy),
	})
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
