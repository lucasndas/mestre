package main

import (
	"context"
	"sync"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// fakeGuild is the Guild the panel shows with `-rich`: the Citadel, a Tower that is connected (so
// "Passar a Cidadela" is on) and a request to enter that is accepted, refused and passed in memory,
// for who walks the whole journey in a browser. It keeps nothing: the real handshake of a machine
// needs a machine (docs/painel/crescer-a-guilda.md), and the smoke and the journey run without it.
type fakeGuild struct {
	mu       sync.Mutex
	machines []panel.GuildMachineRow
	pending  []panel.GuildPendingJoin
	zone     string
}

var _ panel.GuildSource = (*fakeGuild)(nil)

func newFakeGuild() *fakeGuild {
	now := clock.Real{}.Now().UTC()
	sheet := func(os string) panel.GuildSheet {
		return panel.GuildSheet{OS: os, Providers: []string{}, GPUs: []string{}, Operator: "off", CPUCores: 8, MemBytes: 16 << 30}
	}
	return &fakeGuild{
		zone: "Etc/UTC",
		machines: []panel.GuildMachineRow{
			{ID: "e2e", Name: "e2e", Citadel: true, Self: true, Online: true, EngineVersion: "e2e", Sheet: sheet("linux"), Agents: []panel.GuildAgent{}, Load: panel.GuildLoad{DiskFreeBytes: 80 << 30}},
			{ID: "e2e-torre", Name: "notebook-ana", Instance: "torre", Online: true, Protocol: 1, Priority: 1, Sheet: sheet("windows"), Agents: []panel.GuildAgent{},
				Replica: &panel.GuildReplica{State: "streaming"}, Load: panel.GuildLoad{DiskFreeBytes: 50 << 30}},
		},
		pending: []panel.GuildPendingJoin{{ID: "e2e-join", Name: "pc-da-sala", Code: "482 917", CreatedAt: now}},
	}
}

func (g *fakeGuild) find(id string) int {
	for i, m := range g.machines {
		if m.ID == id {
			return i
		}
	}
	return -1
}

func (g *fakeGuild) View(context.Context) (panel.GuildView, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	v := panel.GuildView{Epoch: 1, Timezone: g.zone, Machines: append([]panel.GuildMachineRow{}, g.machines...), Pending: append([]panel.GuildPendingJoin{}, g.pending...)}
	v.Single = len(v.Machines) == 1
	return v, nil
}

func (g *fakeGuild) Removal(_ context.Context, id string) (panel.GuildRemoval, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	i := g.find(id)
	if i < 0 {
		return panel.GuildRemoval{}, panel.ErrGuildNotFound
	}
	m := g.machines[i]
	return panel.GuildRemoval{Machine: m.ID, Name: m.Name, Online: m.Online, Agents: m.Agents}, nil
}

// Decide takes the request out; an accepted one joins as a Tower that is connected.
func (g *fakeGuild) Decide(_ context.Context, _ users.User, joinID string, accept, excluded bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, j := range g.pending {
		if j.ID != joinID {
			continue
		}
		g.pending = append(g.pending[:i], g.pending[i+1:]...)
		if accept {
			g.machines = append(g.machines, panel.GuildMachineRow{ID: "e2e-" + j.ID, Name: j.Name, Instance: "torre", Online: true, Protocol: 1, Excluded: excluded,
				Sheet: panel.GuildSheet{OS: "windows", Providers: []string{}, GPUs: []string{}, Operator: "off"}, Agents: []panel.GuildAgent{}, Replica: &panel.GuildReplica{State: "snapshot"}})
		}
		return nil
	}
	return panel.ErrGuildNotFound
}

func (g *fakeGuild) Remove(_ context.Context, _ users.User, id string) (panel.GuildRemoved, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	i := g.find(id)
	if i < 0 {
		return panel.GuildRemoved{}, panel.ErrGuildNotFound
	}
	online := g.machines[i].Online
	g.machines = append(g.machines[:i], g.machines[i+1:]...)
	return panel.GuildRemoved{Notified: online}, nil
}

func (g *fakeGuild) SetExcluded(_ context.Context, _ users.User, id string, excluded bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	i := g.find(id)
	if i < 0 {
		return panel.ErrGuildNotFound
	}
	g.machines[i].Excluded = excluded
	return nil
}

// PassCitadel makes the machine the Citadel and the old one a Tower; this one still answers.
func (g *fakeGuild) PassCitadel(_ context.Context, _ users.User, id, _ string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	i := g.find(id)
	if i < 0 {
		return panel.ErrGuildNotFound
	}
	for k := range g.machines {
		g.machines[k].Citadel = k == i
	}
	return nil
}

func (g *fakeGuild) SetPriority(_ context.Context, id string, priority int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	i := g.find(id)
	if i < 0 {
		return panel.ErrGuildNotFound
	}
	g.machines[i].Priority = priority
	return nil
}

func (g *fakeGuild) SetTimezone(_ context.Context, zone string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.zone = zone
	return nil
}
