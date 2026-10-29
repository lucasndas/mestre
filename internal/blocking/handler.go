package blocking

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
)

// Characters finds the character that asks (characters.Registry).
type Characters interface {
	Get(ctx context.Context, id string) (characters.Character, error)
}

// Channel sends a notice to the users (telegram.BlockChannel): it returns the
// number of the notice and how many users it was sent to.
type Channel interface {
	Notify(ctx context.Context, n Notice) (id int64, sent int, err error)
	// Say tells the users of the workspace a plain message of the Grand Master (the
	// text of an agent, shown as such), each one by his level; it returns how many
	// were told.
	Say(ctx context.Context, workspace, mesa, text string) (sent int, err error)
}

type panelKey struct{}

// PanelCount counts the users the panel actually shows a notice to, as the channels
// stored it: a channel that keeps the notice for the panel (notices.Feed.Add) tells
// how many users see it with Stored, and a write that failed tells nothing, so a
// failed panel write can not hide `not_delivered`.
type PanelCount struct{ n atomic.Int64 }

// WithPanelCount puts a counter in the context the channel is called with.
func WithPanelCount(ctx context.Context) (context.Context, *PanelCount) {
	c := &PanelCount{}
	return context.WithValue(ctx, panelKey{}, c), c
}

// Stored adds the users a notice stored for the panel reaches (nothing in a context
// that has no counter).
func Stored(ctx context.Context, users int) {
	if c, ok := ctx.Value(panelKey{}).(*PanelCount); ok {
		c.n.Add(int64(users))
	}
}

// N is how many users the panel shows the notice to.
func (c *PanelCount) N() int { return int(c.n.Load()) }

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
	// Evidence is what the engine can show of the table that stopped: built
	// from its last test that failed (TestEvidence); "" when it has none, and
	// then the notice says it has no verified evidence. Nil: none does.
	Evidence func(ctx context.Context, workspace, mesa string) string
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
		mesa := ""
		if name := flagText(c, "table"); name != "" {
			t, err := h.Table(ctx, who.Workspace, name)
			if errors.Is(err, tables.ErrNoTable) {
				return localchan.Fail("not_found", err.Error(), "see `mestre table list`").WithExit(localchan.ExitNotFound)
			}
			if err != nil {
				return localchan.Fail(localchan.CodeInternal, "the table could not be read", "")
			}
			mesa = t.ID
		}
		cctx, stored := WithPanelCount(ctx)
		sent, err := h.Channel.Say(cctx, who.Workspace, mesa, args[0])
		if err != nil {
			return localchan.Fail(localchan.CodeInternal, "the notice was not sent: "+err.Error(), "")
		}
		panel := stored.N()
		if sent == 0 && panel == 0 {
			return localchan.Fail("not_delivered", "no user of this workspace could be told now", "try again, or tell the user in the channel of his message").WithExit(localchan.ExitConflict)
		}
		return localchan.OK(map[string]any{"text": fmt.Sprintf("notice sent to %d user(s) on Telegram and %d on the panel", sent, panel), "delivered": map[string]any{"panel": panel, "telegram": sent}, "queued": false})
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
	if n.Mesa != "" && h.Evidence != nil && (motive == Credential || motive == Permission) {
		n.Evidence = h.Evidence(ctx, who.Workspace, n.Mesa)
	}
	var owner string
	if h.Owner != nil {
		owner = h.Owner(ctx, who.Workspace)
	}
	n.Owner = NoticeOwner(motive, owner)
	if motive == Credential {
		n.Steps = []Step{{Text: i18n.M("block.step_vault"), Kind: StepVault}}
	}
	cctx, stored := WithPanelCount(ctx)
	id, sent, err := h.Channel.Notify(cctx, n)
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, fmt.Sprintf("the notice was not sent: %v", err), "")
	}
	panel := stored.N()
	if sent == 0 && panel == 0 {
		return localchan.Fail("not_delivered", "no user who acts in this workspace could be told (the notice #"+strconv.FormatInt(id, 10)+" is kept for the panel)", "tell a user through another way, or try again").WithExit(localchan.ExitConflict)
	}
	return localchan.OK(map[string]any{
		"text": fmt.Sprintf("notice %d sent to %d user(s) on Telegram and %d on the panel", id, sent, panel), "id": id,
		"delivered": map[string]any{"panel": panel, "telegram": sent}, "queued": false, "blocked_by": orEmpty(n.BlockedBy),
	})
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Asker takes a request to the people (*chain.Service).
type Asker interface {
	Ask(ctx context.Context, a chain.AskRequest) (chain.Outcome, error)
	// AskAction opens a question about an action that is not a request yet.
	AskAction(ctx context.Context, by characters.Character, table string, note chain.AskNote) (int64, error)
}

// AskHandler is the command `mestre approval ask` (docs/cli/grao-mestre.md#mestre-approval):
// the Grão-Mestre takes to the users the request that waits at its level and that
// the policy does not let it decide, with the fields of the request that blocks
// (--motive, --checked, --why-not, --step). The level is checked by the chain.
type AskHandler struct {
	Characters Characters
	Asker      Asker
	// Requests reads a request, to see that it is of the workspace of who asks.
	Requests interface {
		Get(ctx context.Context, id int64) (chain.Request, error)
	}
	// PanelReach is how many users the central of the panel shows the request to
	// (approvals.Flow.Eligible): the `delivered.panel` of the answer. Nil: 0.
	PanelReach func(ctx context.Context, request int64) int
}

// reach is how many users see the request in the panel.
func (h *AskHandler) reach(ctx context.Context, request int64) int {
	if h.PanelReach == nil {
		return 0
	}
	return h.PanelReach(ctx, request)
}

// Register registers the command.
func (h *AskHandler) Register(fwd *localchan.LocalForwarder) {
	fwd.Handle("approval ask", localchan.HandlerFunc(h.handle))
}

func (h *AskHandler) handle(ctx context.Context, c *localchan.Call) localchan.Response {
	if !c.Caller.Identified {
		return localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this process as a process of a character", "run mestre debug")
	}
	who, err := h.Characters.Get(ctx, c.Caller.AgentID)
	if err != nil {
		return localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this character", "run mestre debug")
	}
	if who.Role != characters.RoleGrandMaster {
		return localchan.Fail("level_denied", "only the Grand Master of the workspace takes a request to the users", "")
	}
	usageMsg := `usage: mestre approval ask --action <request id> --summary "<summary>" (--details "<text>" | --details-file <file>) --recommend approve|deny --reason "<why you do not decide>" [--motive <value>] [--checked "<text>"] [--why-not "<text>"] [--step "<text>"]…`
	if args, ok := c.Request.Strings(); !ok || len(args) != 0 {
		return localchan.Fail(localchan.CodeUsage, usageMsg, "")
	}
	action := strings.TrimPrefix(strings.TrimSpace(flagText(c, "action")), "#")
	id, err := strconv.ParseInt(action, 10, 64)
	asksAction := err != nil || id <= 0
	if asksAction && (err == nil || strings.IndexFunc(action, func(r rune) bool { return r >= '0' && r <= '9' }) == 0) {
		// something that starts as a number of a request but is not one
		return localchan.Fail(localchan.CodeUsage, fmt.Sprintf("--action %q is not the number of a request; an action to ask about starts with a word", flagText(c, "action")), "")
	}
	if asksAction && len([]rune(action)) > 1000 {
		return localchan.Fail(localchan.CodeUsage, "--action has at most 1000 characters; the rest goes in --details", "")
	}
	if asksAction && strings.TrimSpace(action) == "" {
		return localchan.Fail(localchan.CodeUsage, "--action is the number of the request that waits at your level (see `mestre approval list --mine`), or the action you want to do", "")
	}
	note := chain.AskNote{Summary: flagText(c, "summary"), Details: flagText(c, "details"), Recommend: flagText(c, "recommend"), Reason: flagText(c, "reason"),
		Checked: flagText(c, "checked"), WhyNot: flagText(c, "why-not"), Steps: flagTexts(c, "step")}
	for _, f := range []struct{ name, v string }{{"summary", note.Summary}, {"details", note.Details}, {"reason", note.Reason}, {"recommend", note.Recommend}} {
		if strings.TrimSpace(f.v) == "" {
			return localchan.Fail(localchan.CodeUsage, "--"+f.name+" is required", usageMsg)
		}
	}
	if note.Recommend != "approve" && note.Recommend != "deny" {
		return localchan.Fail(localchan.CodeUsage, "--recommend is approve or deny", "")
	}
	if m := flagText(c, "motive"); m != "" {
		motive, err := ParseMotive(m)
		if err != nil {
			return localchan.Fail(localchan.CodeUsage, err.Error(), "")
		}
		note.Motive = string(motive)
	}
	if asksAction {
		// not a request yet: the question goes to the people as the type acao_do_grao_mestre
		note.Action = flagText(c, "action")
		rid, err := h.Asker.AskAction(ctx, who, "", note)
		if err != nil {
			return localchan.Fail(localchan.CodeInternal, "the question could not be put to the users", "")
		}
		r := localchan.Fail("pending", fmt.Sprintf("the question #%d was put to the users; their answer arrives in your inbox and the engine does nothing else", rid), "").WithExit(localchan.ExitPending)
		r.Data = localchan.OK(map[string]any{"request": rid, "level": "das pessoas", "delivered": map[string]any{"panel": h.reach(ctx, rid)}}).Data
		return r
	}
	// a request of another workspace is not there for this caller
	if r, err := h.Requests.Get(ctx, id); err != nil || r.Workspace != who.Workspace {
		return localchan.Fail("not_found", fmt.Sprintf("no request #%d", id), "see `mestre approval list`").WithExit(localchan.ExitNotFound)
	}
	out, err := h.Asker.Ask(ctx, chain.AskRequest{Request: id, By: who.ID, Note: note})
	if err != nil {
		var ref *chain.Refusal
		switch {
		case errors.As(err, &ref) && ref.Code == chain.CodeNotAtLevel:
			return localchan.FailMsg("not_at_level", ref.Msg, "see `mestre approval list --mine`").WithExit(localchan.ExitDenied)
		case errors.As(err, &ref) && (ref.Code == chain.CodeAlreadyAsked || ref.Code == chain.CodeNoRequest || ref.Code == chain.CodeBusy):
			code := map[string]string{chain.CodeAlreadyAsked: "already_asked", chain.CodeNoRequest: "already_decided", chain.CodeBusy: "busy"}[ref.Code]
			return localchan.FailMsg(code, ref.Msg, "").WithExit(localchan.ExitConflict)
		}
		return localchan.Fail(localchan.CodeInternal, "the request could not be taken to the users: "+err.Error(), "")
	}
	r := localchan.Fail("pending", fmt.Sprintf("the request #%d was taken to the users; the decision arrives in your inbox", out.Request), "").WithExit(localchan.ExitPending)
	r.Data = localchan.OK(map[string]any{"request": out.Request, "level": out.Level.Label(), "delivered": map[string]any{"panel": h.reach(ctx, out.Request)}}).Data
	return r
}

// The refusals of a question to a user.
var (
	// ErrNoLevel: the user has no level to give orders.
	ErrNoLevel = errors.New("blocking: the user has no level to give orders")
	// ErrNotReachable: the user can not be asked (no Telegram, suspended, or not of
	// the workspace).
	ErrNotReachable = errors.New("blocking: the user can not be reached")
)

// Chooser asks a user a question with a button for each option
// (telegram.ChooseChannel, through an adapter).
type Chooser interface {
	Ask(ctx context.Context, workspace, mesa, asker, userID, question string, options []string, recommend int) (int64, error)
}

// ChannelChooser is a Chooser that takes the question to a channel: the one of the message that
// caused it ("telegram" or "panel").
type ChannelChooser interface {
	AskVia(ctx context.Context, channel, workspace, mesa, asker, userID, question string, options []string, recommend int) (int64, error)
}

// ChooseHandler is the command `mestre choose` (docs/cli/grao-mestre.md#mestre-choose).
// It asks the user of the message in handling, or of --from-message, and passes on
// nothing before the answer; the answer comes to the inbox of the Grand Master as a
// message of the user who clicked.
type ChooseHandler struct {
	Characters Characters
	Chooser    Chooser
	// User gives the user who wrote a message of the workspace (--from-message).
	User func(ctx context.Context, workspace, message string) (string, error)
	// Current gives the user of the message the Grand Master is handling.
	Current func(ctx context.Context, workspace, grandMaster string) (string, error)
	// Table finds a table of the workspace by its name or the number of its epic.
	Table func(ctx context.Context, workspace, name string) (tables.Table, error)
	// ChannelOf gives the channel of the message the question follows (message "" is the one in
	// handling): the question goes only to that channel. Nil, or a Chooser that is no
	// ChannelChooser: the Telegram.
	ChannelOf func(ctx context.Context, workspace, grandMaster, message string) string
}

// Register registers the command.
func (h *ChooseHandler) Register(fwd *localchan.LocalForwarder) {
	fwd.Handle("choose", localchan.HandlerFunc(h.handle))
}

func (h *ChooseHandler) handle(ctx context.Context, c *localchan.Call) localchan.Response {
	if !c.Caller.Identified {
		return localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this process as a process of a character", "run mestre debug")
	}
	who, err := h.Characters.Get(ctx, c.Caller.AgentID)
	if err != nil {
		return localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this character", "run mestre debug")
	}
	if who.Role != characters.RoleGrandMaster {
		return localchan.Fail("level_denied", "only the Grand Master of the workspace asks the users", "").WithExit(localchan.ExitDenied)
	}
	usageMsg := `usage: mestre choose "<question>" --option "<label>" --option "<label>"… [--recommend <n>] [--from-message <id>] [--table "<table>"]`
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return localchan.Fail(localchan.CodeUsage, usageMsg, "")
	}
	options := flagTexts(c, "option")
	if len(options) < 2 || len(options) > 6 {
		return localchan.Fail("bad_options", fmt.Sprintf("a question has 2 to 6 options, not %d", len(options)), "").WithExit(localchan.ExitUsage)
	}
	for i, o := range options {
		if strings.TrimSpace(o) == "" {
			return localchan.Fail("bad_options", fmt.Sprintf("the option %d is empty", i+1), "").WithExit(localchan.ExitUsage)
		}
	}
	recommend := 1
	if r := flagText(c, "recommend"); r != "" {
		n, err := strconv.Atoi(r)
		if err != nil || n < 1 || n > len(options) {
			return localchan.Fail(localchan.CodeUsage, fmt.Sprintf("--recommend is the number of an option, from 1 to %d", len(options)), "")
		}
		recommend = n
	}
	var user string
	if from := flagText(c, "from-message"); from != "" {
		user, err = h.User(ctx, who.Workspace, from)
		if err != nil {
			return localchan.Fail("not_found", fmt.Sprintf("the message %s is not a message of a user of this workspace", from), "see `mestre inbox`").WithExit(localchan.ExitNotFound)
		}
	} else if user, err = h.Current(ctx, who.Workspace, who.ID); err != nil || user == "" {
		return localchan.Fail(localchan.CodeUsage, "there is no message of a user in handling: say which one with --from-message", usageMsg)
	}
	mesa := ""
	if name := flagText(c, "table"); name != "" {
		t, err := h.Table(ctx, who.Workspace, name)
		if errors.Is(err, tables.ErrNoTable) {
			return localchan.Fail("not_found", err.Error(), "see `mestre table list`").WithExit(localchan.ExitNotFound)
		}
		if err != nil {
			return localchan.Fail(localchan.CodeInternal, "the table could not be read", "")
		}
		mesa = t.ID
	}
	var id int64
	if cc, ok := h.Chooser.(ChannelChooser); ok && h.ChannelOf != nil {
		id, err = cc.AskVia(ctx, h.ChannelOf(ctx, who.Workspace, who.ID, flagText(c, "from-message")), who.Workspace, mesa, who.ID, user, args[0], options, recommend)
	} else {
		id, err = h.Chooser.Ask(ctx, who.Workspace, mesa, who.ID, user, args[0], options, recommend)
	}
	switch {
	case errors.Is(err, ErrNoLevel):
		return localchan.Fail("level_denied", "the user has no level to give orders to the Grand Master", "").WithExit(localchan.ExitDenied)
	case errors.Is(err, ErrNotReachable):
		return localchan.Fail("not_delivered", "the user can not be asked now (no Telegram, suspended or not of this workspace)", "ask in the channel of the message").WithExit(localchan.ExitConflict)
	case err != nil:
		return localchan.Fail(localchan.CodeInternal, "the question was not sent", "")
	}
	r := localchan.Fail("pending", fmt.Sprintf("the question %d was sent; nothing goes on before the answer, which arrives in your inbox", id), "").WithExit(localchan.ExitPending)
	r.Data = localchan.OK(map[string]any{"question": id}).Data
	return r
}
