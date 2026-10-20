package decrees

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
)

// Source is the message of a user that a proposal is made from: the engine reads it
// from the inbox of the Grand Master, so the text and the author are the ones of
// the channel, never the ones the agent says.
type Source struct {
	Text string
	// User is the id of the user who wrote it; Channel where it came from.
	User, Channel, MessageID string
	// Audio says the text is a transcript.
	Audio bool
}

// Offerer puts a proposal in front of its author with the buttons Confirmar and
// Cancelar (telegram.DecreeChannel).
type Offerer interface {
	Offer(ctx context.Context, p Pending) error
}

// ProposeHandler is `mestre decree propose` and `mestre decree list`
// (docs/cli/grao-mestre.md#mestre-decree): the Grand Master turns the order of a
// user into a structured proposal; the engine checks the level of the author and
// shows him what will hold, and only his click makes the decree.
type ProposeHandler struct {
	Service    *Service
	Characters interface {
		Get(ctx context.Context, id string) (characters.Character, error)
	}
	// Message reads the message of a user (--from-message) of the workspace.
	Message func(ctx context.Context, workspace, id string) (Source, error)
	Offer   Offerer
}

// ErrNoMessage is what Message answers for a message that is not there, or that no
// user wrote.
var ErrNoMessage = errors.New("decrees: no such message of a user")

// Register registers the commands.
func (h *ProposeHandler) Register(fwd *localchan.LocalForwarder) {
	fwd.Handle("decree propose", localchan.HandlerFunc(h.propose))
	fwd.Handle("decree list", localchan.HandlerFunc(h.list))
}

func (h *ProposeHandler) grandMaster(ctx context.Context, c *localchan.Call) (characters.Character, *localchan.Response) {
	fail := func(code, msg string) (characters.Character, *localchan.Response) {
		r := localchan.Fail(code, msg, "")
		if code == "level_denied" {
			r = r.WithExit(localchan.ExitDenied)
		}
		return characters.Character{}, &r
	}
	if !c.Caller.Identified {
		return fail(localchan.CodeNotIdentified, "the engine does not know this process as a process of a character")
	}
	who, err := h.Characters.Get(ctx, c.Caller.AgentID)
	if err != nil {
		return fail(localchan.CodeNotIdentified, "the engine does not know this character")
	}
	if who.Role != characters.RoleGrandMaster {
		return fail("level_denied", "only the Grand Master of the workspace proposes decrees")
	}
	return who, nil
}

// grantFromScope reads "<files|commands|tools|domains>.<allow|deny>:<value>".
func grantFromScope(scope string) (allow, deny *classes.Grant, ok bool) {
	head, value, found := strings.Cut(scope, ":")
	field, side, dotted := strings.Cut(head, ".")
	value = strings.TrimSpace(value)
	if !found || !dotted || value == "" {
		return nil, nil, false
	}
	g := &classes.Grant{}
	switch field {
	case "files":
		g.Files = []string{value}
	case "commands":
		g.Commands = []string{value}
	case "tools":
		g.Tools = []string{value}
	case "domains":
		g.Domains = []string{value}
	default:
		return nil, nil, false
	}
	switch side {
	case "allow":
		return g, nil, true
	case "deny":
		return nil, g, true
	}
	return nil, nil, false
}

func targetOf(s string, workspace string) (Target, bool) {
	kind, name, found := strings.Cut(s, ":")
	if s == "workspace" {
		return Target{Kind: TargetWorkspace, Name: workspace}, true
	}
	if !found || strings.TrimSpace(name) == "" {
		return Target{}, false
	}
	switch kind {
	case "classe":
		return Target{Kind: TargetClass, Name: name}, true
	case "personagem":
		return Target{Kind: TargetCharacter, Name: name}, true
	case "mesa":
		return Target{Kind: TargetTable, Name: name}, true
	}
	return Target{}, false
}

// build turns the flags into the proposal; the message gives the author and the
// original text.
func build(c *localchan.Call, workspace string, src Source) (Proposal, string) {
	kind := Kind(propFlag(c, "kind"))
	if kind == KindException {
		return Proposal{}, "an exception frees an exact action and is born from the approval of a request, not from a proposal: use --kind rule"
	}
	if kind != KindRule {
		return Proposal{}, "--kind is rule"
	}
	effect, scope, target := propFlag(c, "effect"), propFlag(c, "scope"), propFlag(c, "target")
	p := Proposal{Author: src.User, Kind: KindRule, Workspace: workspace, Text: src.Text,
		Origin: Origin{Channel: src.Channel, Audio: src.Audio}}
	if src.Audio {
		p.Origin.Transcript = src.Text
	}
	var ok bool
	switch effect {
	case EffectDelegation:
		p.Target = Target{Kind: TargetWorkspace, Name: workspace}
		p.Effect = Effect{Kind: EffectDelegation, Setting: target}
		if m, found := strings.CutPrefix(scope, "mesa:"); found {
			p.Mesa = m
		} else {
			p.Scope = scope
		}
	case EffectPermission, EffectCeiling:
		if p.Target, ok = targetOf(target, workspace); !ok {
			return Proposal{}, `--target is classe:<name>, personagem:<name>, mesa:<id> or workspace`
		}
		allow, deny, ok := grantFromScope(scope)
		if !ok {
			return Proposal{}, `--scope is "<files|commands|tools|domains>.<allow|deny>:<value>", like "commands.allow:make deploy-teste"`
		}
		if effect == EffectCeiling && allow == nil {
			return Proposal{}, "a ceiling only widens: use .allow in --scope"
		}
		p.Effect = Effect{Kind: effect, Allow: allow, Deny: deny}
	case EffectBehavior:
		if p.Target, ok = targetOf(target, workspace); !ok {
			return Proposal{}, `--target is classe:<name>, personagem:<name>, mesa:<id> or workspace`
		}
		p.Effect = Effect{Kind: EffectBehavior, Instruction: scope}
	case EffectPolicy:
		if p.Target, ok = targetOf(target, workspace); !ok {
			return Proposal{}, `--target is classe:<name>, personagem:<name>, mesa:<id> or workspace`
		}
		setting, how, found := strings.Cut(scope, ":")
		if !found || strings.TrimSpace(setting) == "" || (how != "loosen" && how != "tighten") {
			return Proposal{}, `--scope of a policy is "<type of request or condition>:loosen|tighten"`
		}
		p.Effect = Effect{Kind: EffectPolicy, Setting: setting, Loosens: how == "loosen"}
	default:
		return Proposal{}, "--effect is permission, behavior, policy, ceiling or delegation"
	}
	if p.Target.Kind == TargetTable && p.Mesa == "" {
		p.Mesa = p.Target.Name
	}
	if u := propFlag(c, "until"); u != "" {
		t, err := time.Parse(time.RFC3339, u)
		if err != nil {
			return Proposal{}, `--until is a date and time with its zone, like "2026-11-20T18:00:00-03:00"`
		}
		p.Until = t
	}
	return p, ""
}

func (h *ProposeHandler) propose(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.grandMaster(ctx, c)
	if bad != nil {
		return *bad
	}
	usage := `usage: mestre decree propose --kind rule --effect permission|behavior|policy|ceiling|delegation --target "<target>" --scope "<scope>" --text "<the order>" [--until "<ISO 8601>"] --from-message <id>`
	if args, ok := c.Request.Strings(); !ok || len(args) != 0 {
		return localchan.Fail(localchan.CodeUsage, usage, "")
	}
	from := propFlag(c, "from-message")
	if from == "" || propFlag(c, "text") == "" {
		return localchan.Fail(localchan.CodeUsage, "--from-message and --text are required: a decree is made from the order of a user", usage)
	}
	src, err := h.Message(ctx, who.Workspace, from)
	if errors.Is(err, ErrNoMessage) {
		return localchan.Fail("not_found", fmt.Sprintf("the message %s is not a message of a user of this workspace", from), "see `mestre inbox`").WithExit(localchan.ExitNotFound)
	}
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, "the message could not be read", "")
	}
	// the text is the one of the message, byte for byte: a different --text is
	// not the order of the user
	if strings.TrimSpace(propFlag(c, "text")) != strings.TrimSpace(src.Text) {
		return localchan.Fail(localchan.CodeUsage, "--text is not the text of the message "+from+": the order is kept as the user wrote it", "")
	}
	p, msg := build(c, who.Workspace, src)
	if msg != "" {
		return localchan.Fail(localchan.CodeUsage, msg, usage)
	}
	pending, err := h.Service.Propose(ctx, p)
	if err != nil {
		var ref *Refusal
		if errors.As(err, &ref) {
			code := map[string]string{CodeLevel: "level_denied", CodeWorkspace: "level_denied"}[ref.Code]
			if code != "" {
				return localchan.Fail(code, ref.Message, "").WithExit(localchan.ExitDenied)
			}
			return localchan.Fail(ref.Code, ref.Message, "").WithExit(localchan.ExitUsage)
		}
		return localchan.Fail(localchan.CodeInternal, "the proposal could not be kept", "")
	}
	if err := h.Offer.Offer(ctx, pending); err != nil {
		h.Service.cancelProposal(ctx, pending.ID)
		return localchan.Fail("not_delivered", "the proposal could not be put in front of its author: "+err.Error(), "tell the user another way").WithExit(localchan.ExitConflict)
	}
	r := localchan.Fail("pending", "the proposal waits for the confirmation of its author; no decree holds before the click", "").WithExit(localchan.ExitPending)
	r.Data = localchan.OK(map[string]any{"proposal": pending.ID, "text": pending.Text, "pin": pending.PIN}).Data
	return r
}

func (h *ProposeHandler) list(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.grandMaster(ctx, c)
	if bad != nil {
		return *bad
	}
	if args, ok := c.Request.Strings(); !ok || len(args) != 0 {
		return localchan.Fail(localchan.CodeUsage, "usage: mestre decree list [--all]", "")
	}
	all, _ := c.Request.Flags["all"].(bool)
	views, err := h.Service.ListWorkspace(ctx, who.Workspace, all)
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, "the decrees could not be read", "")
	}
	items := make([]map[string]any, 0, len(views))
	var text strings.Builder
	for _, d := range views {
		until := ""
		if !d.Until.IsZero() {
			until = d.Until.UTC().Format(time.RFC3339)
		}
		items = append(items, map[string]any{"number": d.Number, "author": d.AuthorName, "level": string(d.AuthorLevel), "text": d.Text, "kind": string(d.Kind),
			"effect": d.Effect.Kind, "target": string(d.Target.Kind) + ":" + d.Target.Name, "scope": d.Scope, "mesa": d.Mesa, "state": string(d.State), "until": until})
		fmt.Fprintf(&text, "- decreto %d (%s, %s): %s\n", d.Number, d.State, d.AuthorName, strings.Join(strings.Fields(d.Text), " "))
	}
	if len(items) == 0 {
		text.WriteString("nenhum decreto\n")
	}
	return localchan.OK(map[string]any{"decrees": items, "text": strings.TrimRight(text.String(), "\n")})
}

func propFlag(c *localchan.Call, name string) string {
	v, _ := c.Request.Flags[name].(string)
	return strings.TrimSpace(v)
}
