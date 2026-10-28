package panel

import (
	"errors"
	"net/http"

	"github.com/mestre-ia/mestre/internal/orchestration/accounts"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/users"
)

func init() { RegisterArea("profiles", mountProfiles) }

const (
	pinProfileAdd    = "profile.add"
	pinProfileRemove = "profile.remove"
	pinProfileInit   = "profile.default"
)

func mountProfiles(r *Router) {
	p := r.Panel()
	// the logins of the agents' accounts are the Owner's (tabela de níveis)
	r.Require("GET /api/profiles", p.handleProfiles, users.AccountProfiles)
	r.Sensitive("POST /api/profiles", pinProfileAdd, users.AccountProfiles, p.handleAddProfile)
	r.Sensitive("DELETE /api/profiles/{name}", pinProfileRemove, users.AccountProfiles, p.handleRemoveProfile)
	r.Sensitive("POST /api/profiles/{name}/default", pinProfileInit, users.AccountProfiles, p.handleDefaultProfile)
	r.Require("POST /api/profiles/{name}/advance", p.handleAdvanceProfile, users.AccountProfiles)
}

func (p *Panel) profiles(w http.ResponseWriter) (ProfileSource, bool) {
	src := p.mg().Profiles
	if src == nil {
		WriteError(w, Err(CodeUnavailable, "api.profiles_unavailable", nil))
		return nil, false
	}
	return src, true
}

func profileRow(machine string, pr accounts.Profile) ProfileRow {
	row := ProfileRow{Name: pr.Name, Provider: string(pr.Provider), Machine: machine, Dir: pr.Dir, Default: pr.Default, Step: string(pr.Step),
		Detail: pr.Detail, CreatedAt: pr.CreatedAt}
	if !pr.SetupDoneAt.IsZero() {
		t := pr.SetupDoneAt
		row.SetupDoneAt = &t
	}
	return row
}

// handleProfiles lists the account profiles of this machine with where the guided setup
// of each stands (the login and the trust of the hooks are its steps).
func (p *Panel) handleProfiles(w http.ResponseWriter, r *http.Request) {
	src, ok := p.profiles(w)
	if !ok {
		return
	}
	list, err := src.Profiles(r.Context())
	if err != nil {
		p.log.Error("panel: listing the account profiles", "err", err)
		WriteError(w, errInternal())
		return
	}
	page := ProfilesPage{Machine: p.machine, Providers: orEmpty(src.Providers()), Profiles: []ProfileRow{}, Setup: src.SetupAvailable()}
	for _, pr := range list {
		page.Profiles = append(page.Profiles, profileRow(p.machine, pr))
	}
	WriteJSON(w, http.StatusOK, page)
}

func (p *Panel) profileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, accounts.ErrUnknown):
		WriteError(w, Err(CodeNotFound, "api.profile_not_found", nil))
	case errors.Is(err, accounts.ErrName), errors.Is(err, accounts.ErrExists):
		WriteError(w, Err(CodeProfileInvalid, "api.detail", map[string]any{"detail": err.Error()}))
	default:
		p.log.Error("panel: the account profiles", "err", err)
		WriteError(w, errInternal())
	}
}

func (p *Panel) handleAddProfile(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), UserOf(r)
	src, ok := p.profiles(w)
	if !ok {
		return
	}
	var in ProfileAdd
	if !decode(w, r, &in) {
		return
	}
	pr, err := src.Add(ctx, in.Name, providers.ID(in.Provider))
	if err != nil {
		if errors.Is(err, accounts.ErrName) || errors.Is(err, accounts.ErrExists) {
			p.profileError(w, err)
			return
		}
		// a provider that is not known is the user's slip, not a failure
		WriteError(w, Err(CodeProfileInvalid, "api.detail", map[string]any{"detail": err.Error()}))
		return
	}
	p.audit(ctx, "panel.profile.added", map[string]any{"user_id": me.ID, "profile": pr.Name, "provider": string(pr.Provider), "machine": p.machine, "ip": clientIP(r)})
	WriteJSON(w, http.StatusCreated, profileRow(p.machine, pr))
}

func (p *Panel) handleRemoveProfile(w http.ResponseWriter, r *http.Request) {
	ctx, me, name := r.Context(), UserOf(r), r.PathValue("name")
	src, ok := p.profiles(w)
	if !ok {
		return
	}
	if err := src.Remove(ctx, name); err != nil {
		p.profileError(w, err)
		return
	}
	// the folder, with the login, is kept (docs/orquestracao/bases-e-fichas.md#no-código)
	p.audit(ctx, "panel.profile.removed", map[string]any{"user_id": me.ID, "profile": name, "machine": p.machine, "ip": clientIP(r)})
	WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (p *Panel) handleDefaultProfile(w http.ResponseWriter, r *http.Request) {
	ctx, me, name := r.Context(), UserOf(r), r.PathValue("name")
	src, ok := p.profiles(w)
	if !ok {
		return
	}
	if err := src.SetDefault(ctx, name); err != nil {
		p.profileError(w, err)
		return
	}
	p.audit(ctx, "panel.profile.default", map[string]any{"user_id": me.ID, "profile": name, "machine": p.machine, "ip": clientIP(r)})
	WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdvanceProfile goes as far as the guided setup of the profile can and says
// what it waits for; it is called again after each thing the Owner does (the login in
// the terminal that opened, the approval of the hooks).
func (p *Panel) handleAdvanceProfile(w http.ResponseWriter, r *http.Request) {
	ctx, me, name := r.Context(), UserOf(r), r.PathValue("name")
	src, ok := p.profiles(w)
	if !ok {
		return
	}
	st, err := src.Advance(ctx, name)
	if errors.Is(err, accounts.ErrNoProfile) {
		WriteError(w, Err(CodeUnavailable, "api.guided_setup_unavailable", nil))
		return
	}
	if err != nil {
		p.profileError(w, err)
		return
	}
	out := ProfileSetupStatus{Step: string(st.Step), Done: st.Done, Waiting: st.Waiting, Conformant: st.Conformant, Reason: st.Reason}
	if st.Terminal != nil {
		out.Terminal = st.Terminal.ID
	}
	p.audit(ctx, "panel.profile.setup", map[string]any{"user_id": me.ID, "profile": name, "step": out.Step, "done": out.Done, "machine": p.machine, "ip": clientIP(r)})
	WriteJSON(w, http.StatusOK, out)
}
