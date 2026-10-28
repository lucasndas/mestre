package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// guildHost is the panel.GuildSource over the Citadel that acts: the screen of
// the Guild reads the registry, the connections and the replication, and acts
// through the same calls the bot's /guilda uses (docs/painel/administracao.md).
type guildHost struct {
	e *Engine
	// local gives the networks of this machine, where "the local network" is told
	// from (nil: those of its interfaces).
	local func() []*net.IPNet

	mu sync.Mutex
	// seen is when each machine was last seen online since this period began.
	seen map[string]time.Time
}

var _ panel.GuildSource = (*guildHost)(nil)

func newGuildHost(e *Engine) *guildHost {
	return &guildHost{e: e, local: e.deps.LocalNetworks, seen: map[string]time.Time{}}
}

func (g *guildHost) networks() []*net.IPNet {
	if g.local != nil {
		return g.local()
	}
	return guild.LocalNetworks()
}

// acting is the Citadel of the period that acts, with the period; a Tower and a
// Citadel that does not act answer that the Guild is not served here.
func (g *guildHost) acting() (*guild.Citadel, *citadelRun, error) {
	c, err := g.e.actingCitadel()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", panel.ErrGuildUnavailable, err)
	}
	g.e.mu.Lock()
	run := g.e.cit
	g.e.mu.Unlock()
	return c, run, nil
}

// refusalOf turns what the registry refuses into the refusal the screen shows.
func refusalOf(err error) error {
	refuse := func(code string) error { return &panel.Refusal{Msg: i18n.M(code)} }
	switch {
	case err == nil:
		return nil
	case errors.Is(err, guild.ErrNoMachine):
		return panel.ErrGuildNotFound
	case errors.Is(err, guild.ErrCitadelProtected):
		return refuse("guild.citadel_protected")
	case errors.Is(err, guild.ErrExcludedMachine):
		return refuse("guild.excluded_machine")
	case errors.Is(err, guild.ErrOutsideLocalNetwork):
		return refuse("guild.outside_local_network")
	case errors.Is(err, guild.ErrNotActive):
		return refuse("guild.not_active")
	case errors.Is(err, guild.ErrNoLock):
		return refuse("guild.no_lock")
	case errors.Is(err, guild.ErrSuspended):
		return refuse("guild.suspended")
	case errors.Is(err, guild.ErrNotPending), errors.Is(err, guild.ErrExpired):
		return refuse("guild.not_pending")
	case errors.Is(err, guild.ErrNotRevealed):
		return refuse("guild.not_revealed")
	case errors.Is(err, guild.ErrBadPriority):
		return refuse("guild.bad_priority")
	case errors.Is(err, guild.ErrBadTimezone):
		return refuse("guild.bad_timezone")
	}
	return err
}

// fenceState is what the Guild screen reads of the fence of the Citadel (guild.Fence).
type fenceState interface {
	Acting() bool
	State() guild.FenceState
	TelegramStopped() bool
}

// fenceFlags are the marks of the row of the Citadel: suspended (the fence does not let it act, or
// it drains), "GitHub fora" (the lock is down) and the Telegram stopped by a 409.
func fenceFlags(f fenceState, draining bool) (suspended, githubDown, telegramStopped bool) {
	return !f.Acting() || draining, f.State() == guild.FenceLockDown, f.TelegramStopped()
}

func (g *guildHost) View(ctx context.Context) (panel.GuildView, error) {
	c, run, err := g.acting()
	if err != nil {
		return panel.GuildView{}, err
	}
	all, err := c.Machines(ctx)
	if err != nil {
		return panel.GuildView{}, err
	}
	citadel, err := c.CitadelID(ctx)
	if err != nil {
		return panel.GuildView{}, err
	}
	zone, err := c.Timezone(ctx)
	if err != nil {
		return panel.GuildView{}, err
	}
	sheets := map[string]machinesheet.Sheet{}
	if reg := g.e.sheets.Load(); reg != nil {
		if ss, err := reg.Sheets(ctx); err == nil {
			for _, s := range ss {
				sheets[s.MachineID] = s
			}
		}
	}
	replicas := map[string]panel.GuildReplica{}
	if run != nil && run.primary != nil {
		if st, err := run.primary.Status(ctx); err == nil {
			for _, s := range st {
				replicas[s.Machine] = panel.GuildReplica{State: s.State, Behind: s.Behind, LagMS: s.Lag.Milliseconds()}
			}
		}
	}
	agents := map[string][]panel.GuildAgent{}
	if orc := g.e.orc.Load(); orc != nil {
		if cs, err := orc.Registry.Alive(ctx); err == nil {
			for _, ch := range cs {
				agents[ch.Machine] = append(agents[ch.Machine], panel.GuildAgent{ID: ch.ID, Name: ch.Name, Workspace: ch.Workspace, Table: ch.Table, State: string(ch.State)})
			}
		}
	}
	now, local := g.e.clk.Now(), g.networks()
	v := panel.GuildView{Epoch: g.e.epochs.Epoch(), Timezone: zone, Machines: []panel.GuildMachineRow{}, Pending: []panel.GuildPendingJoin{}}
	for _, m := range all {
		if m.State != guild.MachineActive {
			continue
		}
		row := panel.GuildMachineRow{ID: m.ID, Name: plainText(m.Name), Instance: plainText(m.Instance), Citadel: m.ID == citadel, Self: m.ID == g.e.id.MachineID,
			Excluded: m.Excluded, Priority: m.Priority, OutsideNetwork: m.OutsideLocalNetwork(local), Agents: agents[m.ID]}
		if row.Agents == nil {
			row.Agents = []panel.GuildAgent{}
		}
		row.Load = panel.GuildLoad{Running: len(row.Agents)}
		switch {
		case row.Citadel:
			row.Online = true
			if run != nil && run.fence != nil {
				row.Suspended, row.GitHubDown, row.TelegramStopped = fenceFlags(run.fence, run.gate.isDraining())
			}
		case run != nil && run.srv != nil:
			link := run.srv.Link(m.ID)
			if row.Online = link.Connected(); row.Online {
				if s := link.Session(); s != nil {
					row.Protocol = s.Version()
				}
			}
		}
		if row.Self {
			row.EngineVersion = g.e.deps.Version
		}
		g.mu.Lock()
		if row.Online {
			g.seen[m.ID] = now
		}
		if t, ok := g.seen[m.ID]; ok && !row.Online {
			t := t
			row.LastSeen = &t
		}
		g.mu.Unlock()
		if r, ok := replicas[m.ID]; ok {
			row.Replica = &r
		}
		if s, ok := sheets[m.ID]; ok {
			row.Sheet = sheetSummary(s)
			row.Load.DiskFreeBytes = s.Resources.DiskFreeBytes
		}
		v.Machines = append(v.Machines, row)
	}
	joins, err := c.PendingJoins(ctx)
	if err != nil {
		return panel.GuildView{}, err
	}
	for _, j := range joins {
		v.Pending = append(v.Pending, panel.GuildPendingJoin{ID: j.ID, Name: plainText(j.Name), Code: j.Code, CreatedAt: j.CreatedAt})
	}
	return v, nil
}

// sheetSummary is what the screen shows of a sheet; the names in it are the
// machine's own words, so the marks of the Telegram are dropped and the screen
// shows them as text.
func sheetSummary(s machinesheet.Sheet) panel.GuildSheet {
	out := panel.GuildSheet{OS: s.OS, GUI: s.GUI, CPUCores: s.Resources.CPUCores, MemBytes: s.Resources.MemBytes, Transcribes: s.Transcricao,
		Providers: []string{}, GPUs: []string{}, Operator: "off"}
	for _, p := range s.Providers {
		var in []string
		for _, pr := range p.Profiles {
			if pr.LoggedIn {
				in = append(in, plainText(pr.Name))
			}
		}
		sort.Strings(in)
		out.Providers = append(out.Providers, p.Provider+": "+strings.Join(in, ", "))
	}
	for _, gpu := range s.Resources.GPUs {
		out.GPUs = append(out.GPUs, plainText(gpu.Model))
	}
	if b := s.Browser; b != nil {
		out.Browser = plainText(strings.TrimSpace(b.Name + " " + b.Version))
	}
	if s.Operador.Enabled {
		out.Operator = "not_ready"
		if s.Operador.Ready {
			out.Operator = "ready"
		}
		out.OperatorReason = s.Operador.Reason
	}
	return out
}

func (g *guildHost) Removal(ctx context.Context, machineID string) (panel.GuildRemoval, error) {
	v, err := g.View(ctx)
	if err != nil {
		return panel.GuildRemoval{}, err
	}
	for _, m := range v.Machines {
		if m.ID == machineID {
			return panel.GuildRemoval{Machine: m.ID, Name: m.Name, Online: m.Online, Agents: m.Agents}, nil
		}
	}
	return panel.GuildRemoval{}, panel.ErrGuildNotFound
}

func (g *guildHost) Decide(ctx context.Context, by users.User, joinID string, accept, excluded bool) error {
	if _, _, err := g.acting(); err != nil {
		return err
	}
	return refusalOf(g.e.DecideEntry(ctx, joinID, guild.Decision{Accept: accept, ApprovedBy: by.ID, Excluded: excluded}))
}

func (g *guildHost) Remove(ctx context.Context, by users.User, machineID string) (panel.GuildRemoved, error) {
	c, _, err := g.acting()
	if err != nil {
		return panel.GuildRemoved{}, err
	}
	res, err := c.Remove(ctx, machineID, by.ID)
	if err != nil {
		return panel.GuildRemoved{}, refusalOf(err)
	}
	return panel.GuildRemoved{Notified: res.Notified}, nil
}

func (g *guildHost) SetExcluded(ctx context.Context, _ users.User, machineID string, excluded bool) error {
	_, _, err := g.acting()
	if err != nil {
		return err
	}
	return refusalOf(g.e.SetExcluded(ctx, machineID, excluded))
}

// PassCitadel checks the target and starts the planned switch
// (docs/guilda/cidadela-e-torres.md#troca-planejada): the switch itself takes up
// to a minute, so it runs on, and the screen follows it by the view (the Citadel
// turns "suspensa" and then a Tower).
func (g *guildHost) PassCitadel(ctx context.Context, by users.User, machineID, approvalID string) error {
	c, run, err := g.acting()
	if err != nil {
		return err
	}
	if err := c.PassTarget(ctx, machineID, g.networks()); err != nil {
		return refusalOf(err)
	}
	switch {
	case run == nil || run.srv == nil || !run.srv.Link(machineID).Connected():
		return &panel.Refusal{Msg: i18n.M("guild.not_online")}
	case g.e.lockOf(run) == nil:
		return refusalOf(guild.ErrNoLock)
	}
	if err := run.claimHandover(run.fence != nil && run.fence.Acting()); err != nil {
		return err
	}
	go func() {
		defer run.handing.Store(false)
		if err := g.e.Handover(context.WithoutCancel(ctx), machineID, approvalID, by.ID, 0); err != nil {
			g.e.log.Warn("app: the planned switch asked in the panel did not end", "target", machineID, "approval", approvalID, "err", err)
		}
	}()
	return nil
}

func (g *guildHost) SetPriority(ctx context.Context, machineID string, priority int) error {
	c, run, err := g.acting()
	if err != nil {
		return err
	}
	if err := c.SetPriority(ctx, machineID, priority); err != nil {
		return refusalOf(err)
	}
	g.e.sendConfig(context.WithoutCancel(ctx), run)
	return nil
}

func (g *guildHost) SetTimezone(ctx context.Context, zone string) error {
	c, run, err := g.acting()
	if err != nil {
		return err
	}
	if err := c.SetTimezone(ctx, zone); err != nil {
		return refusalOf(err)
	}
	g.e.sendConfig(context.WithoutCancel(ctx), run)
	return nil
}

// claimHandover takes the only place of a planned handover asked in the panel: a
// second one while the first is under way, and one on a Citadel that drains or does
// not act, are refused with their reason. The caller frees it when the handover ends.
func (r *citadelRun) claimHandover(acting bool) error {
	if !r.handing.CompareAndSwap(false, true) {
		return &panel.Refusal{Msg: i18n.M("guild.handover_in_progress")}
	}
	if r.gate.isDraining() || !acting {
		r.handing.Store(false)
		return refusalOf(guild.ErrSuspended)
	}
	return nil
}
