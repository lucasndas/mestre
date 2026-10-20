package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

// DecreeApplier registers the use of a decree (decrees.Service.Applied).
type DecreeApplier interface {
	Applied(ctx context.Context, c characters.Character, number int64, action string) error
}

// decreeUseTimeout bounds the write of a use: the hook answers within its time.
const decreeUseTimeout = 5 * time.Second

// decreeUses records, in the decree, that the hook let a call through on its
// strength (docs/telegram/decretos.md#registro). It looks the character up in
// the registry of the Citadel and gives the decree to Applied, which refuses a
// decree that does not reach the character now, so the number an event carries
// is never trusted alone.
type decreeUses struct {
	reg     func(ctx context.Context, id string) (characters.Character, error)
	applier DecreeApplier
	log     func(msg string, args ...any)
	clk     clock.Clock // nil: the real one
}

// onEvent registers the use of the decree an allow carries, once per event.
func (u decreeUses) onEvent(ev providers.Event, ans hook.Answer) {
	if u.applier == nil || ans.Decree <= 0 || ans.Verdict != hook.VerdictAllow || ev.AgentID == "" || (ev.Event != providers.EventPreTool && ev.Event != providers.EventPermissionRequest) {
		return
	}
	clk := u.clk
	if clk == nil {
		clk = clock.Real{}
	}
	ctx, cancel := clock.WithTimeout(context.Background(), clk, decreeUseTimeout)
	defer cancel()
	c, err := u.reg(ctx, ev.AgentID)
	if err == nil {
		err = u.applier.Applied(ctx, c, ans.Decree, actionOf(ev))
	}
	if err != nil && u.log != nil {
		u.log("app: the use of a decree was not registered", "decree", ans.Decree, "agent", ev.AgentID, "ended", errors.Is(err, decrees.ErrEnded), "err", err)
	}
}

// actionOf says what the call did, for the list of what a decree executed.
func actionOf(ev providers.Event) string {
	if ev.Tool == nil {
		return string(ev.Event)
	}
	switch {
	case ev.Tool.Command != "":
		return ev.Tool.Command
	case len(ev.Tool.Paths) > 0:
		return fmt.Sprintf("%s %s", ev.Tool.NativeName, strings.Join(ev.Tool.Paths, " "))
	case ev.Tool.URL != "":
		return fmt.Sprintf("%s %s", ev.Tool.NativeName, ev.Tool.URL)
	}
	return ev.Tool.NativeName
}

// hostDecreeUses is the use of the decrees in the hook of a period: what is told
// of each event the hooks answered, when the people of the chain keep decrees
// (nil otherwise: no decree has been made).
func hostDecreeUses(integ IntegrationRun, orc *Orchestration, clk clock.Clock, log func(msg string, args ...any)) func(providers.Event, hook.Answer) {
	people, ok := integ.(chainPeople)
	if !ok {
		return nil
	}
	ap, ok := people.CharacterDecrees().(DecreeApplier)
	if !ok {
		return nil
	}
	return decreeUses{reg: orc.Registry.Get, applier: ap, clk: clk, log: log}.onEvent
}

// decreeTargets is the decrees.Targets: the live target of the request an
// exception bound to a PR was made for. The repository of the target is the one
// of the workspace of the decree, never another (an exception made in a
// workspace spends nothing in the repository of another), and the live target is
// given in the spelling of the repository of the decree, which GitHub folds.
type decreeTargets struct {
	live  lateTargets
	repos func(ctx context.Context, workspace string) (string, error)
}

func (t decreeTargets) Current(ctx context.Context, r chain.Request) (json.RawMessage, error) {
	if r.Type != classes.RequestPR && r.Type != classes.RequestMerge {
		return nil, fmt.Errorf("app: no exception is bound to the target of a request of type %q", r.Type)
	}
	want, err := t.repos(ctx, r.Workspace)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", chain.ErrWrongRepo, err)
	}
	if !chain.SameRepo(want, chain.TargetRepo(r)) {
		return nil, fmt.Errorf("%w: %q is not %q", chain.ErrWrongRepo, chain.TargetRepo(r), want)
	}
	cur, err := t.live.Current(ctx, r)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if json.Unmarshal(cur, &m) != nil {
		return cur, nil
	}
	m["repo"] = chain.TargetRepo(r)
	return json.Marshal(m)
}
