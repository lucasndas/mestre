package ghcli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
)

// AppStore is the Apps of the Mestre (*github.Registry).
type AppStore interface {
	List(ctx context.Context) ([]github.App, error)
	Get(ctx context.Context, name string) (github.App, error)
	ForClass(ctx context.Context, class string) (github.App, error)
	Fixed(ctx context.Context, role string) (github.App, error)
	Request(ctx context.Context, a github.App) (github.App, error)
	Taken(ctx context.Context, name string) bool
	Count(ctx context.Context) (int, error)
	Repos(ctx context.Context, name string) ([]string, error)
	OpenRequest(ctx context.Context, name string) (bool, error)
}

var _ AppStore = (*github.Registry)(nil)

// AppLink is the link the Owner opens to create an App.
type AppLink struct {
	ExpiresAt time.Time
	// ID identifies the request; the link itself goes only to the Owner.
	ID string
}

// AppCreator asks the Owner to create an App: it makes the link and sends it to
// the Owner, by the Telegram and the panel (*github.Creation). The link never
// returns to the Grand Master.
type AppCreator interface {
	// Available says why the creation can not be asked now ("" when it can).
	Available(ctx context.Context) string
	Begin(ctx context.Context, app, requestedBy string) (AppLink, error)
}

// Apps is `mestre app list|status|create` (docs/cli/grao-mestre.md#mestre-app):
// only the Grão-Mestre. Nothing here hands a token or a key to the caller.
type Apps struct {
	Characters Characters
	Store      AppStore
	Creator    AppCreator
}

// Register registers the commands.
func (a *Apps) Register(fwd *localchan.LocalForwarder) {
	fwd.Handle("app list", localchan.HandlerFunc(a.list))
	fwd.Handle("app status", localchan.HandlerFunc(a.status))
	fwd.Handle("app create", localchan.HandlerFunc(a.create))
}

func (a *Apps) grandMaster(ctx context.Context, c *localchan.Call) (string, *localchan.Response) {
	fail := func(r localchan.Response) (string, *localchan.Response) { return "", &r }
	if !c.Caller.Identified {
		return fail(localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this process as a process of a character", "run mestre debug"))
	}
	who, err := a.Characters.Get(ctx, c.Caller.AgentID)
	if err != nil {
		return fail(localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this character", "run mestre debug"))
	}
	if who.Role != characters.RoleGrandMaster {
		return fail(localchan.Fail("level_denied", "only the Grand Master of the workspace works on the Apps", "").WithExit(localchan.ExitDenied))
	}
	return who.ID, nil
}

func appView(a github.App) map[string]any {
	return map[string]any{"name": a.Name, "role": a.Role, "class": a.Class, "state": string(a.State), "state_label": a.State.Label(), "slug": a.Slug}
}

func (a *Apps) list(ctx context.Context, c *localchan.Call) localchan.Response {
	if _, bad := a.grandMaster(ctx, c); bad != nil {
		return *bad
	}
	if args, ok := c.Request.Strings(); !ok || len(args) != 0 {
		return usage("usage: mestre app list")
	}
	apps, err := a.Store.List(ctx)
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, "the Apps could not be read", "")
	}
	count, err := a.Store.Count(ctx)
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, "the Apps could not be counted", "")
	}
	items := make([]map[string]any, 0, len(apps))
	var text strings.Builder
	fmt.Fprintf(&text, "%d/%d Apps da conta", count, github.AppLimit)
	for _, ap := range apps {
		v := appView(ap)
		repos, err := a.Store.Repos(ctx, ap.Name)
		if err != nil {
			return localchan.Fail(localchan.CodeInternal, "the installations could not be read", "")
		}
		if repos == nil {
			repos = []string{}
		}
		v["repos"] = repos
		items = append(items, v)
		fmt.Fprintf(&text, "\n- %s (%s): %s", ap.Name, ap.Role, ap.State.Label())
	}
	return localchan.OK(map[string]any{"apps": items, "count": count, "limit": github.AppLimit, "text": text.String()})
}

func (a *Apps) status(ctx context.Context, c *localchan.Call) localchan.Response {
	if _, bad := a.grandMaster(ctx, c); bad != nil {
		return *bad
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 {
		return usage(`usage: mestre app status "<app>"`)
	}
	app, err := a.Store.Get(ctx, args[0])
	if errors.Is(err, github.ErrNoApp) {
		return localchan.Fail("not_found", fmt.Sprintf("no App named %q", args[0]), "see `mestre app list`").WithExit(localchan.ExitNotFound)
	}
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, "the App could not be read", "")
	}
	repos, err := a.Store.Repos(ctx, app.Name)
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, "the installations could not be read", "")
	}
	pending, err := a.Store.OpenRequest(ctx, app.Name)
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, "the request could not be read", "")
	}
	v := appView(app)
	if repos == nil {
		repos = []string{}
	}
	// the key never leaves the vault: only whether the App went past its creation
	v["repos"], v["workflows"], v["creation_open"] = repos, app.Workflows, pending
	v["key_in_vault"] = app.Slug != "" && app.AppID != 0
	v["text"] = fmt.Sprintf("App %s (%s): %s", app.Name, app.Role, app.State.Label())
	return localchan.OK(v)
}

func (a *Apps) create(ctx context.Context, c *localchan.Call) localchan.Response {
	by, bad := a.grandMaster(ctx, c)
	if bad != nil {
		return *bad
	}
	if args, ok := c.Request.Strings(); !ok || len(args) != 0 {
		return usage(`usage: mestre app create --class "<class>" [--name "<name>"]`)
	}
	class, _ := flagString(c, "class")
	if strings.TrimSpace(class) == "" {
		return usage("--class is required")
	}
	if why := a.Creator.Available(ctx); why != "" {
		return unavailable(why)
	}
	if existing, err := a.Store.ForClass(ctx, class); err == nil {
		if open, _ := a.Store.OpenRequest(ctx, existing.Name); open {
			return localchan.Fail("app_pending", fmt.Sprintf("the App %s of the class %q already waits for the Owner", existing.Name, class), "the link is with the Owner").WithExit(localchan.ExitConflict)
		}
		return localchan.Fail("app_exists", fmt.Sprintf("the class %q already has the App %s", class, existing.Name), "see `mestre app status`").WithExit(localchan.ExitConflict)
	} else if !errors.Is(err, github.ErrNoApp) {
		return localchan.Fail(localchan.CodeInternal, "the Apps could not be read", "")
	}
	name, _ := flagString(c, "name")
	if name = strings.TrimSpace(name); name == "" {
		fixed, err := a.Store.Fixed(ctx, github.RoleGrandMaster)
		if err != nil {
			return localchan.Fail(localchan.CodeInternal, "the prefix of the names of the Apps is not known: the App of the Grand Master does not exist yet", "")
		}
		name = github.ClassName(strings.TrimSuffix(fixed.Name, "-grao-mestre"), class, func(n string) bool { return a.Store.Taken(ctx, n) })
	}
	if len([]rune(name)) > github.MaxNameLength {
		return localchan.Fail("name_too_long", fmt.Sprintf("the name %q has more than %d characters", name, github.MaxNameLength), "").WithExit(localchan.ExitUsage)
	}
	app, err := a.Store.Request(ctx, github.App{Name: name, Role: github.RoleClass, Class: class})
	switch {
	case errors.Is(err, github.ErrAppLimit):
		return localchan.Fail("app_limit", "the account has the 100 Apps GitHub allows", "bind the class to an App that exists").WithExit(8)
	case errors.Is(err, github.ErrNameTaken):
		return localchan.Fail("app_exists", fmt.Sprintf("an App named %q is already registered", name), "").WithExit(localchan.ExitConflict)
	case err != nil:
		return localchan.Fail(localchan.CodeInternal, "the App could not be prepared", "")
	}
	link, err := a.Creator.Begin(ctx, app.Name, by)
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, "the link of creation could not be made", "")
	}
	r := localchan.Fail("pending", fmt.Sprintf("the App %s waits for the Owner, who got the link; the App ready arrives in your inbox", app.Name), "").WithExit(localchan.ExitPending)
	r.Data = localchan.OK(map[string]any{"request_id": link.ID, "app": app.Name, "link_expires_at": link.ExpiresAt.UTC().Format(time.RFC3339)}).Data
	return r
}
