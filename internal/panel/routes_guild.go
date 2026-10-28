package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/users"
)

// The Guild and the health of the machines (docs/painel/administracao.md#guilda-e-saúde-das-máquinas).
//
// For the security review:
//   - every route needs the level of the table (Owner for the machines, the time
//     zone included: manage_machines and guild_settings), checked here at each
//     request; the buttons of the screen are a convenience;
//   - every action asks for the PIN in its own request (Sensitive) and writes
//     panel.guild.* in the audit with the user and the before/after, never the PIN;
//   - the id of a machine or of an entry is looked up in the registry of the
//     engine before anything is done (404 otherwise) and what comes from a machine
//     (name, code) leaves as JSON text;
//   - the refusals of the rules (excluded machine, outside the local network, the
//     Citadel itself, offline) are decided by the engine, never by the browser;
//   - a removal needs "confirm": the warning (how many and which agents run on
//     the machine) comes from GET .../removal, and the PIN is asked after it.

// guildPoll is how often the panel looks at the Guild while somebody follows it.
const guildPoll = 3 * time.Second

// GuildTopic is the topic of the screen of the Guild.
const GuildTopic = "guild:main"

// Errors of a GuildSource.
var (
	// ErrGuildNotFound: the machine or the entry does not exist.
	ErrGuildNotFound = errors.New("panel: no such machine or entry in the Guild")
	// ErrGuildUnavailable: this machine is not the Citadel that acts now.
	ErrGuildUnavailable = errors.New("panel: the Guild is not served by this machine now")
)

// guildView is the view of the engine with the band of the single-machine mode
// decided here: it shows while the Citadel is the only machine (CA-ADM-08).
func guildView(ctx context.Context, src GuildSource) (GuildView, error) {
	v, err := src.View(ctx)
	if err != nil {
		return v, err
	}
	v.Single = len(v.Machines) == 1
	if v.Machines == nil {
		v.Machines = []GuildMachineRow{}
	}
	if v.Pending == nil {
		v.Pending = []GuildPendingJoin{}
	}
	return v, nil
}

// GuildSource is the engine of the Guild, as the screen sees it. A refusal of a
// rule comes as a *Refusal (stable reasons: excluded_machine,
// outside_local_network, citadel_protected, not_active, not_online, no_lock,
// not_pending, suspended, bad_priority, bad_timezone).
type GuildSource interface {
	View(ctx context.Context) (GuildView, error)
	Removal(ctx context.Context, machineID string) (GuildRemoval, error)
	Decide(ctx context.Context, by users.User, joinID string, accept, excluded bool) error
	Remove(ctx context.Context, by users.User, machineID string) (GuildRemoved, error)
	SetExcluded(ctx context.Context, by users.User, machineID string, excluded bool) error
	// PassCitadel is the planned switch to an online machine of the local network.
	PassCitadel(ctx context.Context, by users.User, machineID, approvalID string) error
	SetPriority(ctx context.Context, machineID string, priority int) error
	SetTimezone(ctx context.Context, zone string) error
}

type guildHolder struct{ src GuildSource }

// SetGuild gives the panel the engine of the Guild; until then the screen
// answers that it is not available.
func (p *Panel) SetGuild(src GuildSource) { p.guild.Store(&guildHolder{src}) }

func (p *Panel) guildSource() GuildSource {
	if h := p.guild.Load(); h != nil {
		return h.src
	}
	return nil
}

func init() { RegisterArea("guild", mountGuild) }

// The actions of PIN of this screen.
const (
	pinGuildDecide   = "guild.decide"
	pinGuildRemove   = "guild.remove"
	pinGuildExclude  = "guild.exclude"
	pinGuildPass     = "guild.pass_citadel"
	pinGuildPriority = "guild.priority"
	pinGuildZone     = "guild.timezone"
)

func mountGuild(r *Router) {
	p := r.Panel()
	p.hub.Provide("guild", Topic{
		Authorize: func(u users.User, id string) bool { return id == "main" && users.Can(u.Level, users.ManageMachines) },
		Snapshot: func(ctx context.Context, _ users.User, _ string) (any, error) {
			if src := p.guildSource(); src != nil {
				return guildView(ctx, src)
			}
			return struct{}{}, nil
		},
		Open: p.followGuild,
	})
	r.Require("GET /api/guild", p.handleGuild, users.ManageMachines)
	r.Require("GET /api/guild/machines/{id}/removal", p.handleGuildRemoval, users.ManageMachines)
	r.Sensitive("POST /api/guild/joins/{id}/decide", pinGuildDecide, users.ManageMachines, p.handleGuildDecide)
	r.Sensitive("POST /api/guild/machines/{id}/remove", pinGuildRemove, users.ManageMachines, p.handleGuildRemove)
	r.Sensitive("POST /api/guild/machines/{id}/exclusion", pinGuildExclude, users.ManageMachines, p.handleGuildExclude)
	r.Sensitive("POST /api/guild/machines/{id}/pass-citadel", pinGuildPass, users.ManageMachines, p.handleGuildPass)
	r.Sensitive("PUT /api/guild/machines/{id}/priority", pinGuildPriority, users.ManageMachines, p.handleGuildPriority)
	r.Sensitive("PUT /api/guild/timezone", pinGuildZone, users.GuildSettings, p.handleGuildTimezone)
}

// guildFollow runs the polling of the view only while somebody follows the Guild:
// the first follower starts it and the last one stops it, so an idle panel holds no
// timer.
type guildFollow struct {
	mu     sync.Mutex
	n      int
	cancel context.CancelFunc
}

// followGuild is Topic.Open of the guild topic: it returns what ends this follower.
func (p *Panel) followGuild(ctx context.Context, _ *Subscription, _ users.User, _ string) func() {
	f := &p.guildFollow
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n++; f.n == 1 {
		run, cancel := context.WithCancel(context.WithoutCancel(ctx))
		f.cancel = cancel
		go p.watchGuild(run)
	}
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.n--; f.n == 0 && f.cancel != nil {
			f.cancel()
			f.cancel = nil
		}
	}
}

// watchGuild publishes the view to the followers of the screen when it changes.
func (p *Panel) watchGuild(ctx context.Context) {
	t := p.clk.NewTicker(guildPoll)
	defer t.Stop()
	var last []byte
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
		}
		src := p.guildSource()
		if src == nil {
			continue
		}
		v, err := guildView(ctx, src)
		if err != nil {
			continue
		}
		b, err := json.Marshal(v)
		if err != nil || bytes.Equal(b, last) {
			continue
		}
		last = b
		_ = p.hub.Publish(GuildTopic, EventGuild, v)
	}
}

func (p *Panel) needGuild(w http.ResponseWriter) (GuildSource, bool) {
	src := p.guildSource()
	if src == nil {
		WriteError(w, Err(CodeUnavailable, "api.guild_unavailable", nil))
		return nil, false
	}
	return src, true
}

// failGuild answers the error of the engine: a refusal keeps its reason.
func (p *Panel) failGuild(w http.ResponseWriter, err error) {
	var ref *Refusal
	switch {
	case errors.As(err, &ref):
		WriteError(w, ErrMsg(ref.Msg))
	case errors.Is(err, ErrGuildUnavailable):
		WriteError(w, Err(CodeUnavailable, "api.guild_unavailable", nil))
	case errors.Is(err, ErrGuildNotFound):
		WriteError(w, Err(CodeNotFound, "api.machine_not_found", nil))
	default:
		p.log.Warn("panel: an action of the Guild failed", "err", err)
		WriteError(w, errInternal())
	}
}

func (p *Panel) handleGuild(w http.ResponseWriter, r *http.Request) {
	src, ok := p.needGuild(w)
	if !ok {
		return
	}
	v, err := guildView(r.Context(), src)
	if err != nil {
		p.failGuild(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, v)
}

func (p *Panel) handleGuildRemoval(w http.ResponseWriter, r *http.Request) {
	src, ok := p.needGuild(w)
	if !ok {
		return
	}
	rm, err := src.Removal(r.Context(), r.PathValue("id"))
	if err != nil {
		p.failGuild(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, rm)
}

// machineOf finds the machine in the registry, before an action touches it.
func (p *Panel) machineOf(ctx context.Context, src GuildSource, id string) (GuildMachineRow, error) {
	v, err := guildView(ctx, src)
	if err != nil {
		return GuildMachineRow{}, err
	}
	for _, m := range v.Machines {
		if m.ID == id {
			return m, nil
		}
	}
	return GuildMachineRow{}, ErrGuildNotFound
}

func (p *Panel) auditGuild(r *http.Request, action string, detail map[string]any) {
	detail["user_id"], detail["ip"] = UserOf(r).ID, clientIP(r)
	p.audit(r.Context(), "panel.guild."+action, detail)
}

func (p *Panel) handleGuildDecide(w http.ResponseWriter, r *http.Request) {
	src, ok := p.needGuild(w)
	if !ok {
		return
	}
	var in GuildDecideRequest
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if err := src.Decide(r.Context(), UserOf(r), id, in.Accept, in.Excluded); err != nil {
		p.failGuild(w, err)
		return
	}
	p.auditGuild(r, "entry_decided", map[string]any{"join": id, "accepted": in.Accept, "excluded": in.Excluded})
	w.WriteHeader(http.StatusNoContent)
}

func (p *Panel) handleGuildRemove(w http.ResponseWriter, r *http.Request) {
	src, ok := p.needGuild(w)
	if !ok {
		return
	}
	var in GuildRemoveRequest
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	m, err := p.machineOf(r.Context(), src, id)
	if err != nil {
		p.failGuild(w, err)
		return
	}
	if !in.Confirm {
		WriteError(w, ErrMsg(i18n.M("guild.confirm_required")))
		return
	}
	res, err := src.Remove(r.Context(), UserOf(r), id)
	if err != nil {
		p.failGuild(w, err)
		return
	}
	p.auditGuild(r, "machine_removed", map[string]any{"machine": id, "name": m.Name, "agents": len(m.Agents), "notified": res.Notified})
	WriteJSON(w, http.StatusOK, res)
}

func (p *Panel) handleGuildExclude(w http.ResponseWriter, r *http.Request) {
	src, ok := p.needGuild(w)
	if !ok {
		return
	}
	var in GuildExcludeRequest
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	m, err := p.machineOf(r.Context(), src, id)
	if err != nil {
		p.failGuild(w, err)
		return
	}
	if err := src.SetExcluded(r.Context(), UserOf(r), id, in.Excluded); err != nil {
		p.failGuild(w, err)
		return
	}
	p.auditGuild(r, "exclusion", map[string]any{"machine": id, "before": map[string]any{"excluded": m.Excluded}, "after": map[string]any{"excluded": in.Excluded}})
	w.WriteHeader(http.StatusNoContent)
}

func (p *Panel) handleGuildPass(w http.ResponseWriter, r *http.Request) {
	src, ok := p.needGuild(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	m, err := p.machineOf(r.Context(), src, id)
	if err != nil {
		p.failGuild(w, err)
		return
	}
	approval := "painel-" + p.ids.NewString()
	err = src.PassCitadel(r.Context(), UserOf(r), id, approval)
	if err != nil {
		// the refusal is recorded too: a switch that was asked for and not done
		p.auditGuild(r, "citadel_pass_refused", map[string]any{"machine": id, "name": m.Name, "error": err.Error()})
		p.failGuild(w, err)
		return
	}
	p.auditGuild(r, "citadel_pass_started", map[string]any{"machine": id, "name": m.Name, "approval": approval})
	w.WriteHeader(http.StatusNoContent)
}

func (p *Panel) handleGuildPriority(w http.ResponseWriter, r *http.Request) {
	src, ok := p.needGuild(w)
	if !ok {
		return
	}
	var in GuildPriorityRequest
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	m, err := p.machineOf(r.Context(), src, id)
	if err != nil {
		p.failGuild(w, err)
		return
	}
	if err := src.SetPriority(r.Context(), id, in.Priority); err != nil {
		p.failGuild(w, err)
		return
	}
	p.auditGuild(r, "priority", map[string]any{"machine": id, "before": map[string]any{"priority": m.Priority}, "after": map[string]any{"priority": in.Priority}})
	w.WriteHeader(http.StatusNoContent)
}

func (p *Panel) handleGuildTimezone(w http.ResponseWriter, r *http.Request) {
	src, ok := p.needGuild(w)
	if !ok {
		return
	}
	var in GuildTimezoneRequest
	if !decode(w, r, &in) {
		return
	}
	before, err := guildView(r.Context(), src)
	if err != nil {
		p.failGuild(w, err)
		return
	}
	if err := src.SetTimezone(r.Context(), in.Timezone); err != nil {
		p.failGuild(w, err)
		return
	}
	p.auditGuild(r, "timezone", map[string]any{"before": map[string]any{"timezone": before.Timezone}, "after": map[string]any{"timezone": in.Timezone}})
	w.WriteHeader(http.StatusNoContent)
}
