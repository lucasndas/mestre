package guild

import (
	"context"
	"errors"
	"net"
	"sort"
	"strconv"

	"github.com/mestre-ia/mestre/internal/discovery"
)

// ErrCitadelDown is the installer's "a Guilda existe, mas a Cidadela está
// fora; tente de novo depois": only Towers answered, so no second Guild is
// created (docs/guilda/guilda.md#formação).
var ErrCitadelDown = errors.New("a Guilda existe, mas a Cidadela está fora; tente de novo depois")

// PlanAction is what the installer does next.
type PlanAction int

// The three outcomes of the search for a Guild.
const (
	// PlanCreate: no Guild answered (or --new-guild): create one here.
	PlanCreate PlanAction = iota + 1
	// PlanJoin: one Guild answered (or --join): ask to enter it.
	PlanJoin
	// PlanChoose: more than one answered: list them and ask which.
	PlanChoose
)

// FoundGuild is a Guild that answered the search, as the installer lists it:
// the name of its Citadel and how many machines it has.
type FoundGuild struct {
	GuildID     string
	CitadelName string
	Machines    int
	Epoch       uint64
	// Addr is "host:port" of the engine of the Citadel, which answered.
	Addr string
}

// Plan is the decision of the installer.
type Plan struct {
	Action PlanAction
	// Join is the Guild to enter (PlanJoin).
	Join FoundGuild
	// Guilds are the ones to choose from (PlanChoose), by name.
	Guilds []FoundGuild
}

// PlanInput is what the installer knows when it looks for a Guild.
type PlanInput struct {
	// Search is the broadcast and mDNS search; its GuildID is ignored (a new
	// install has none: an empty one asks every Guild).
	Search discovery.SearchConfig
	// JoinAddr is `mestre install --join <endereço>`: only that address.
	JoinAddr string
	// NewGuild is `mestre install --new-guild`: create even if another Guild
	// answers.
	NewGuild bool
}

// InstallConfig is the "enter or create" step of the installer.
type InstallConfig struct {
	Plan PlanInput
	// Create is used when the plan is to create a Guild; its Member is set
	// from Member below.
	Create CreateConfig
	// Join is used when the plan is to enter one; Addr is set from the plan.
	Join   JoinConfig
	Member *Member
	// Choose picks one of several Guilds (the installer asks the user).
	Choose func(guilds []FoundGuild) (FoundGuild, error)
}

// InstallResult is what the step produced: the plan and either the Guild this
// machine founded or the entry it was given.
type InstallResult struct {
	Plan    Plan
	Founded *Founded
	Joined  *Joined
}

// Install runs the step after the discovery: it plans, and then creates the
// Guild (this machine becomes the Citadel of epoch 1) or asks to enter the
// one chosen. With a Guild whose Citadel is away it returns ErrCitadelDown and
// creates nothing.
func Install(ctx context.Context, cfg InstallConfig) (*InstallResult, error) {
	plan, err := PlanInstall(ctx, cfg.Plan)
	if err != nil {
		return nil, err
	}
	res := &InstallResult{Plan: plan}
	switch plan.Action {
	case PlanCreate:
		cfg.Create.Member = cfg.Member
		res.Founded, err = Create(ctx, cfg.Create)
	case PlanChoose:
		if cfg.Choose == nil {
			return res, errors.New("guild: more than one Guild answered and nobody chooses")
		}
		var g FoundGuild
		if g, err = cfg.Choose(plan.Guilds); err != nil {
			return res, err
		}
		res.Plan.Join = g
		cfg.Join.Addr = g.Addr
		res.Joined, err = Enroll(ctx, cfg.Member, cfg.Join)
	default:
		cfg.Join.Addr = plan.Join.Addr
		res.Joined, err = Enroll(ctx, cfg.Member, cfg.Join)
	}
	return res, err
}

// PlanInstall decides what a new install does after the discovery
// (docs/guilda/guilda.md#formação): with --join, enter the Guild at that
// address; with --new-guild, create one; otherwise search with an empty
// guild_id and group the answers by Guild. Nothing answered: create. One
// Guild with its Citadel up: enter. More than one: choose. A Guild whose
// Citadel did not answer, only Towers: ErrCitadelDown, and nothing is created.
func PlanInstall(ctx context.Context, in PlanInput) (Plan, error) {
	switch {
	case in.JoinAddr != "":
		return Plan{Action: PlanJoin, Join: FoundGuild{Addr: in.JoinAddr}}, nil
	case in.NewGuild:
		return Plan{Action: PlanCreate}, nil
	}
	cfg := in.Search
	cfg.GuildID = ""
	seen, err := discovery.Search(ctx, cfg)
	if err != nil {
		return Plan{}, err
	}
	byGuild := map[string]*FoundGuild{}
	var order []string
	anyGuild := false
	for _, s := range seen {
		if s.GuildID == "" {
			continue
		}
		anyGuild = true
		if s.Role != discovery.RoleCitadel || len(s.Addrs) == 0 || s.Port == 0 {
			continue
		}
		g, ok := byGuild[s.GuildID]
		if !ok {
			g = &FoundGuild{GuildID: s.GuildID}
			byGuild[s.GuildID] = g
			order = append(order, s.GuildID)
		}
		if s.Epoch >= g.Epoch {
			g.CitadelName, g.Epoch = s.Name, s.Epoch
			g.Addr = net.JoinHostPort(s.Addrs[0], strconv.Itoa(s.Port))
		}
		g.Machines = max(g.Machines, s.Machines)
	}
	if len(order) == 0 {
		if anyGuild {
			return Plan{}, ErrCitadelDown
		}
		return Plan{Action: PlanCreate}, nil
	}
	guilds := make([]FoundGuild, 0, len(order))
	for _, id := range order {
		guilds = append(guilds, *byGuild[id])
	}
	if len(guilds) == 1 {
		return Plan{Action: PlanJoin, Join: guilds[0]}, nil
	}
	sort.SliceStable(guilds, func(i, j int) bool { return guilds[i].CitadelName < guilds[j].CitadelName })
	return Plan{Action: PlanChoose, Guilds: guilds}, nil
}
