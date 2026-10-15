package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// OrchestrationMachines is how the Citadel reaches the engine of every machine
// that runs characters: the start and the end, the deliveries of the inbox,
// the terminals. characters.MachineSet is one for the machines of this
// process; the Towers over the connection are milestone 5.
type OrchestrationMachines interface {
	characters.Machines
	inbox.Deliverer
	waits.Terminals
}

// OrchestrationOptions are the pieces of the Citadel's orchestration
// (milestone 4): what the services need to be built and joined. The pieces of
// milestones 5 and 6 (GitHub, the Telegram, the users, the approvals, the
// decrees) come in as small interfaces, each with its own document.
type OrchestrationOptions struct {
	// Guild is the main Guild database, Machine the id of this Citadel.
	Guild   *store.DB
	Machine string
	Clock   clock.Clock
	Logger  *slog.Logger
	// WorkspacesDir is <dados>/workspaces; TempDir a private folder for the
	// bundles being sent.
	WorkspacesDir, TempDir string
	// Git is the git of the engine (gitx.Real, or the fake).
	Git tables.Git
	// Machines reaches the engine of each machine; Presets and Builder are
	// what the characters are born from.
	Machines OrchestrationMachines
	Presets  characters.Presets
	Builder  characters.Builder
	Limits   characters.Limits

	// Milestone 5: GitHub and the identity of the App of the Grand Master.
	GitHub      workspaces.GitHub
	Credentials workspaces.Credentials
	Epics       tables.GitHub
	// Alerts reaches the Administrators.
	Alerts workspaces.Alerts
	// Users and Channels reach the people; Messages and Attachments are the
	// messages of the users and what they attach; Destructive is the approval
	// of a destructive action; Approvals the one that deleting a scroll needs.
	Users       inbox.Users
	Channels    inbox.Channels
	Messages    tables.Messages
	Attachments tables.Attachments
	Destructive tables.Destructive
	Approvals   scrolls.Approvals
	Usage       characters.UsageProbe
	// Files is the session zero of the tables on the Citadel.
	Files    tables.Files
	Profiles tables.Profiler
	Settings tables.Settings
	// Peers reaches the Towers for the branches of the tables.
	Peers tables.Peers
	// Rituals deactivates the rituals of a table that ends (stage 10).
	Rituals tables.Rituals
	// Waker: the waits' tie to the characters (the machines).
	Waker waits.Waker

	// The quests (stage 9). QuestTowers is the half of the tables of a Tower,
	// for the done of a character there; QuestGitHub the issues of the quests;
	// QuestApprovals the points of approval (milestone 5); QuestCatalog the
	// check of the sheets and of the classes against the ceiling when a quest
	// is made (nil: the presets answer for the sheets and the ceiling is not
	// checked).
	QuestTowers    quests.Towers
	QuestGitHub    quests.GitHub
	QuestApprovals quests.Approvals
	QuestCatalog   quests.Catalog
}

// Orchestration is the services of milestone 4 of the Citadel, assembled.
type Orchestration struct {
	Workspaces  *workspaces.Service
	Tables      *tables.Service
	Characters  *characters.Service
	Connections *connections.Service
	Inbox       *inbox.Service
	Waits       *waits.Service
	Scrolls     *scrolls.Service
	Quests      *quests.Service
	// Registry is the registry of the characters of the Guild.
	Registry characters.Registry
	// Observers are told of every change of state of a character, in the
	// order the document asks: the inbox first, then the waits, then the
	// tables.
	Observers characters.Observers

	fence atomic.Pointer[guild.Fence]
	log   *slog.Logger
}

// Lazy references break the loops of the construction: the tables are asked by
// the characters that the tables ask for, and the inbox is asked if a table is
// paused by the tables that put items in it.
type (
	tablesRef     struct{ p **tables.Service }
	charactersRef struct{ p **characters.Service }
)

func (r tablesRef) Table(ctx context.Context, ws, id string) (characters.TableInfo, error) {
	return (*r.p).Table(ctx, ws, id)
}

func (r tablesRef) Worktree(ctx context.Context, ws, table, machine string) (string, error) {
	return (*r.p).Worktree(ctx, ws, table, machine)
}

func (r tablesRef) Paused(ctx context.Context, ws, table string) (bool, error) {
	return (*r.p).Paused(ctx, ws, table)
}

func (r charactersRef) Create(ctx context.Context, req characters.CreateRequest) (characters.CreateResult, error) {
	return (*r.p).Create(ctx, req)
}

func (r charactersRef) Dismiss(ctx context.Context, req characters.DismissRequest) (characters.DismissResult, error) {
	return (*r.p).Dismiss(ctx, req)
}

// questsRef is the view of the quests that the waits, the scrolls and the
// characters have of them, since the quests are made after all of them.
type questsRef struct{ p **quests.Service }

func (r questsRef) Quest(ctx context.Context, ws, table, name string) (waits.QuestInfo, bool, error) {
	if *r.p == nil {
		return waits.QuestInfo{}, false, nil
	}
	return (*r.p).Quest(ctx, ws, table, name)
}

func (r questsRef) Responsible(ctx context.Context, stepID string) (string, error) {
	if *r.p == nil {
		return "", nil
	}
	return (*r.p).Responsible(ctx, stepID)
}

func (r questsRef) QuestName(ctx context.Context, id string) (string, bool) {
	if *r.p == nil {
		return "", false
	}
	return (*r.p).QuestName(ctx, id)
}

func (r questsRef) StateChanged(ctx context.Context, c characters.Character, from characters.State) error {
	if *r.p == nil {
		return nil
	}
	return (*r.p).StateChanged(ctx, c, from)
}

func (r questsRef) Done(context.Context, characters.Character, string) error { return nil }

func (r questsRef) TurnEnded(context.Context, characters.Character, string) error { return nil }

// presetsCatalog checks the sheets of a quest against the presets; the ceiling
// is not checked unless a Catalog says how.
type presetsCatalog struct{ characters.Presets }

func (c presetsCatalog) Sheet(ctx context.Context, name string) (quests.SheetInfo, bool, error) {
	snap, err := c.Snapshot(ctx, name)
	if err != nil {
		return quests.SheetInfo{}, false, nil
	}
	return quests.SheetInfo{Name: snap.Sheet.Name, Class: snap.Sheet.Class}, true, nil
}

func (presetsCatalog) ClassAllowed(context.Context, string, string) error { return nil }

// scrollsRef is the connections' view of the scrolls, which exist after them.
type scrollsRef struct{ p **scrolls.Service }

func (r scrollsRef) Resolve(ctx context.Context, ws, table, name string) (connections.ScrollRef, bool, error) {
	return (*r.p).Resolve(ctx, ws, table, name)
}

func (r scrollsRef) Get(ctx context.Context, ref connections.Ref) (connections.ScrollRef, bool, error) {
	return (*r.p).Get(ctx, ref)
}

func (r scrollsRef) Members(ctx context.Context, tome string) ([]string, error) {
	return (*r.p).Members(ctx, tome)
}

// NewOrchestration builds the services of milestone 4 and joins them: the
// workspaces, the tables, the characters, the connections, the inbox, the waits
// and the scrolls. Nothing starts; Register puts their commands in the
// forwarder of the Citadel and External runs their loops.
func NewOrchestration(o OrchestrationOptions) (*Orchestration, error) {
	switch {
	case o.Guild == nil, o.Git == nil, o.Machines == nil, o.Presets == nil, o.Builder == nil:
		return nil, errors.New("app: the orchestration needs the Guild database, git, the machines, the presets and the builder")
	case o.GitHub == nil, o.Epics == nil, o.Alerts == nil, o.Files == nil:
		return nil, errors.New("app: the orchestration needs GitHub, the epics, the alerts and the session zero of the tables")
	case o.Machine == "" || o.WorkspacesDir == "":
		return nil, errors.New("app: the orchestration needs the id of the Citadel and the folder of the workspaces")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	orc := &Orchestration{log: o.Logger}
	gate := func() error {
		if f := orc.fence.Load(); f != nil {
			return f.Allow(guild.ExternalGitHub)
		}
		return nil
	}
	reg := characters.Registry{DB: o.Guild}
	var (
		tbl   *tables.Service
		chars *characters.Service
		scr   *scrolls.Service
		qs    *quests.Service
	)

	conns, err := connections.NewService(connections.Options{Registry: reg, Clock: o.Clock, Scrolls: scrollsRef{&scr}, Tables: tablesRef{&tbl}})
	if err != nil {
		return nil, err
	}
	ib, err := inbox.NewService(inbox.Options{DB: &inbox.Store{DB: o.Guild}, Clock: o.Clock, Registry: reg, Deliverer: o.Machines, Pauses: tablesRef{&tbl},
		Channels: o.Channels, Users: o.Users, Logger: o.Logger})
	if err != nil {
		return nil, err
	}
	ws, err := workspaces.New(workspaces.Options{DB: o.Guild, Git: o.Git, Layout: workspaces.Layout{Root: o.WorkspacesDir}, Clock: o.Clock, GitHub: o.GitHub,
		Credentials: o.Credentials, Alerts: o.Alerts, GrandMaster: inbox.GrandMaster{S: ib}, Log: o.Logger, Gate: gate})
	if err != nil {
		return nil, err
	}
	tbl, err = tables.New(tables.Options{DB: o.Guild, Git: o.Git, Workspaces: ws, Clock: o.Clock, Machine: o.Machine, GitHub: o.Epics, Characters: charactersRef{&chars},
		Registry: reg, Inbox: ib, Notifier: ib, Alerts: o.Alerts, Files: o.Files, Settings: o.Settings, Messages: o.Messages, Attachments: o.Attachments,
		Profiles: o.Profiles, Destructive: o.Destructive, Rituals: o.Rituals, Peers: o.Peers, TempDir: o.TempDir, Log: o.Logger, Gate: gate})
	if err != nil {
		return nil, err
	}
	scr, err = scrolls.NewService(scrolls.Options{Store: &scrolls.Store{DB: o.Guild}, Clock: o.Clock, Registry: reg, Tables: tbl, Connections: conns,
		Principal: ws.Layout().Principal, Quests: questsRef{&qs}, Logger: o.Logger})
	if err != nil {
		return nil, err
	}
	wt, err := waits.NewService(waits.Options{Store: &waits.Store{DB: o.Guild}, Clock: o.Clock, Registry: reg, Inbox: ib, Notifier: ib, Connections: conns,
		Quests: questsRef{&qs}, Waker: o.Waker, Terminals: o.Machines, Logger: o.Logger})
	if err != nil {
		return nil, err
	}
	obs := characters.Observers{ib, wt, tbl, questsRef{&qs}}
	chars, err = characters.NewService(characters.ServiceOptions{Registry: reg, Clock: o.Clock, Presets: o.Presets, Tables: tbl, Machines: o.Machines, Builder: o.Builder,
		Notifier: ib, Scrolls: scr, Inbox: ib, Usage: o.Usage, Connections: conns, Observer: obs, Limits: o.Limits, Logger: o.Logger})
	if err != nil {
		return nil, err
	}
	catalog := o.QuestCatalog
	if catalog == nil {
		catalog = presetsCatalog{o.Presets}
	}
	qs, err = quests.NewService(quests.Options{Store: &quests.Store{DB: o.Guild}, Clock: o.Clock, Registry: reg, Machine: o.Machine, Characters: chars, Inbox: ib,
		Notifier: ib, Scrolls: scr, Connections: conns, Tables: tbl, Waits: wt, Catalog: catalog, Towers: o.QuestTowers, GitHub: o.QuestGitHub,
		Approvals: o.QuestApprovals, Gate: gate, Logger: o.Logger})
	if err != nil {
		return nil, err
	}
	orc.Registry = reg
	orc.Quests = qs
	orc.Workspaces, orc.Tables, orc.Characters, orc.Connections, orc.Inbox, orc.Waits, orc.Scrolls, orc.Observers = ws, tbl, chars, conns, ib, wt, scr, obs
	return orc, nil
}

// Register puts the commands of the orchestration in the forwarder of the
// Citadel (Engine.Commands): the connections, the inbox, the waits, the
// scrolls, the tables and the quests. The commands of later stages and milestones are
// added where they are built.
func (o *Orchestration) Register(fwd *localchan.LocalForwarder, approvals scrolls.Approvals) {
	o.Connections.Register(fwd)
	o.Inbox.Register(fwd)
	o.Waits.Register(fwd)
	o.Scrolls.Register(fwd, o.Registry, approvals)
	o.Tables.Register(fwd)
	o.Quests.Register(fwd)
}

// Intervals of the loops that External runs.
const (
	sweepEvery = time.Minute
)

// External is the ExternalFunc that runs the work of the orchestration while
// the Citadel acts: it brings back what the engine had before it went down
// (the inbox, the waits), and runs the fetch of the mirrors, the lifecycle of
// the characters and the end of the tables until the context ends. It gives
// the fence to the services, which ask it before they touch the outside.
func (o *Orchestration) External() ExternalFunc {
	return func(ctx context.Context, fence *guild.Fence) error {
		o.fence.Store(fence)
		defer o.fence.Store(nil)
		if err := o.Inbox.Recover(ctx); err != nil {
			o.log.Warn("app: the inbox was not recovered", "err", err)
		}
		if err := o.Waits.Recover(ctx); err != nil {
			o.log.Warn("app: the waits were not recovered", "err", err)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		run := func(name string, f func(context.Context) error) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := f(ctx); err != nil && ctx.Err() == nil {
					errs <- fmt.Errorf("%s: %w", name, err)
				}
			}()
		}
		run("workspaces", o.Workspaces.Run)
		run("characters", func(ctx context.Context) error { return o.Characters.Run(ctx, sweepEvery) })
		run("tables", func(ctx context.Context) error { return o.Tables.Run(ctx, sweepEvery) })
		run("quests", func(ctx context.Context) error { return o.Quests.Loop(ctx, sweepEvery) })
		wg.Wait()
		close(errs)
		return errors.Join(drain(errs)...)
	}
}

func drain(c chan error) []error {
	var out []error
	for e := range c {
		out = append(out, e)
	}
	return out
}
