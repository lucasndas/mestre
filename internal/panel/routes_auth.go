package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/mestre-ia/mestre/internal/users"
)

func init() { RegisterArea("auth", mountAuth) }

func mountAuth(r *Router) {
	p := r.Panel()
	r.Public("GET /api/status", p.handleStatus)
	r.Public("POST /api/setup", p.handleSetup)
	r.Public("POST /api/login", p.handleLogin)
	r.Public("POST /api/first-access", p.handleFirstAccess)
	r.Auth("POST /api/logout", p.handleLogout)
	r.Auth("PUT /api/me/language", p.handleLanguage)
	r.Auth("PUT /api/me/workspace", p.handleLastWorkspace)
	r.Auth("GET /api/me", p.handleMe)
	r.Auth("POST /api/password", p.handlePassword)
	r.Sensitive("POST /api/users/{id}/access-code", "users.access_code", users.ManageUsers, p.handleAccessCode)
}

// decode reads the JSON body into v; an unknown field is a bad request.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		WriteError(w, errBadRequest())
		return false
	}
	return true
}

func (p *Panel) handleStatus(w http.ResponseWriter, r *http.Request) {
	open, err := p.users.SetupOpen(r.Context())
	if err != nil {
		p.log.Error("panel: reading the state of the setup", "err", err)
		WriteError(w, errInternal())
		return
	}
	WriteJSON(w, http.StatusOK, Status{SetupOpen: open})
}

// setCookie opens the session in the browser.
func (p *Panel) setCookie(w http.ResponseWriter, token string, ss Session) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/", Expires: ss.CreatedAt.Add(MaxAge),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

func (p *Panel) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

// me is the answer that opens a session.
func (p *Panel) me(ctx context.Context, u users.User, ss Session) Me {
	var caps []string
	for _, c := range users.Capabilities() {
		if users.Can(u.Level, c) {
			caps = append(caps, string(c))
		}
	}
	slices.Sort(caps)
	ws := u.Workspaces
	if ws == nil {
		ws = []string{}
	}
	return Me{
		User:         UserInfo{ID: u.ID, Name: u.Name, Login: u.Login, Level: string(u.Level), LevelName: u.Level.Name(), Workspaces: ws, HasPIN: u.HasPIN, Language: u.Language, LastWorkspace: u.LastWorkspace},
		Session:      SessionInfo{ID: ss.ID, UserID: ss.UserID, Device: ss.Device, IP: ss.IP, CreatedAt: ss.CreatedAt, LastUsedAt: ss.LastUsedAt, Current: true},
		Capabilities: caps,
		Timezone:     p.timezone(ctx),
	}
}

// timezone is the IANA name of the Guild time zone (UTC when it has none).
func (p *Panel) timezone(ctx context.Context) string {
	var name string
	if err := p.db.Reader().QueryRowContext(ctx, `SELECT timezone FROM guild WHERE id = 1`).Scan(&name); err != nil || name == "" || name == "Local" {
		return "UTC"
	}
	if _, err := time.LoadLocation(name); err != nil {
		return "UTC"
	}
	return name
}

// open starts the session of a user and answers with who he is.
func (p *Panel) open(w http.ResponseWriter, r *http.Request, u users.User, action string) {
	token, ss, err := p.sessions.Create(r.Context(), u.ID, r.UserAgent(), clientIP(r))
	if err != nil {
		p.log.Error("panel: opening a session", "err", err)
		WriteError(w, errInternal())
		return
	}
	p.audit(r.Context(), action, map[string]any{"user_id": u.ID, "session": ss.ID, "ip": ss.IP, "device": ss.Device})
	p.setCookie(w, token, ss)
	WriteJSON(w, http.StatusOK, p.me(r.Context(), u, ss))
}

func (p *Panel) handleSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	open, err := p.users.SetupOpen(ctx)
	if err != nil {
		WriteError(w, errInternal())
		return
	}
	if !open { // once the Guild has a user the route is not there
		WriteError(w, Err(CodeNotFound, "não encontrado", nil))
		return
	}
	var in SetupRequest
	if !decode(w, r, &in) {
		return
	}
	ip := clientIP(r)
	if until, err := p.setupClosedFor(ctx, ip); err != nil {
		WriteError(w, errInternal())
		return
	} else if !until.IsZero() {
		p.audit(ctx, "panel.setup.refused", map[string]any{"ip": ip})
		WriteError(w, Err(CodeRateLimited, "muitas tentativas; tente mais tarde", map[string]any{"retry_after_seconds": retryAfter(p.clk.Now(), until)}))
		return
	}
	var u users.User
	if !p.hashing(w, func() {
		u, err = p.users.SetupOwner(ctx, in.Code, users.SetupInput{Name: in.Name, Login: in.Login, Password: in.Password, PIN: in.PIN})
	}) {
		return
	}
	switch {
	case errors.Is(err, users.ErrBadCode):
		_ = p.failSetup(ctx, ip)
		p.audit(ctx, "panel.setup.failed", map[string]any{"ip": ip})
		WriteError(w, errInvalidCode())
		return
	case errors.Is(err, users.ErrSetupDone):
		WriteError(w, Err(CodeNotFound, "não encontrado", nil))
		return
	case p.inputError(w, err):
		return
	case err != nil:
		p.log.Error("panel: the setup of the Owner", "err", err)
		WriteError(w, errInternal())
		return
	}
	p.open(w, r, u, "panel.setup")
}

// inputError answers the errors of what the user typed; it says whether it did.
func (p *Panel) inputError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, users.ErrBadPassword):
		WriteError(w, Err(CodePasswordTooShort, fmt.Sprintf("a senha tem no mínimo %d caracteres", users.PasswordMin), map[string]any{"min": users.PasswordMin}))
	case errors.Is(err, users.ErrBadLogin):
		WriteError(w, Err(CodeInvalidLogin, "o nome de login tem de 3 a 32 caracteres: letras minúsculas, números, ponto, hífen e sublinhado", nil))
	case errors.Is(err, users.ErrLoginTaken):
		WriteError(w, Err(CodeLoginTaken, "este nome de login já existe", nil))
	case errors.Is(err, users.ErrWrongPassword):
		WriteError(w, Err(CodePasswordWrong, "a senha atual não confere", nil))
	case errors.Is(err, users.ErrBadPINFormat):
		WriteError(w, Err(CodeInvalidPIN, "o PIN tem de 6 a 12 dígitos", nil))
	default:
		return false
	}
	return true
}

// hashing runs f, which does an argon2 hash, if one of the slots is free; else
// it answers rate_limited and says so.
func (p *Panel) hashing(w http.ResponseWriter, f func()) bool {
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
		f()
		return true
	default:
		WriteError(w, Err(CodeRateLimited, "muitas tentativas ao mesmo tempo; tente de novo", map[string]any{"retry_after_seconds": 1}))
		return false
	}
}

func retryAfter(now, until time.Time) int {
	s := int(until.Sub(now).Round(time.Second) / time.Second)
	return max(s, 1)
}

func (p *Panel) handleLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in LoginRequest
	if !decode(w, r, &in) {
		return
	}
	login := users.NormalizeLogin(in.Login)
	ip := clientIP(r)
	// a text that is no login name is nobody's: it is not written anywhere
	counted := users.ValidLogin(login)
	if counted {
		ok, until, err := p.reserveLogin(ctx, login)
		if err != nil {
			WriteError(w, errInternal())
			return
		}
		if !ok { // refused without looking at the password
			p.audit(ctx, "panel.login.blocked", p.loginDetail(ctx, login, ip, r))
			WriteError(w, Err(CodeLoginBlocked, "login bloqueado por excesso de erros; tente mais tarde", map[string]any{"retry_after_seconds": retryAfter(p.clk.Now(), until)}))
			return
		}
	}
	var (
		u   users.User
		res users.Credential
		err error
	)
	if !p.hashing(w, func() { u, res, err = p.users.CheckPassword(ctx, login, in.Password) }) {
		return
	}
	if err != nil {
		p.log.Error("panel: checking the password", "err", err)
		WriteError(w, errInternal())
		return
	}
	if res != users.CredOK {
		action := "panel.login.failed"
		if res == users.CredSuspended {
			action = "panel.login.suspended"
		}
		p.audit(ctx, action, p.loginDetail(ctx, login, ip, r))
		if counted {
			until, blocked, err := p.failLogin(ctx, login)
			if err != nil {
				p.log.Error("panel: recording the wrong password", "err", err)
			}
			if blocked {
				p.audit(ctx, "panel.login.blocked", p.loginDetail(ctx, login, ip, r))
				p.warnBlocked(ctx, login, until)
			}
		}
		WriteError(w, errInvalidCredentials()) // the same words for every reason
		return
	}
	_ = p.clearLogin(ctx, login)
	p.open(w, r, u, "panel.login")
}

// loginDetail is the detail of the audit of an attempt: the login is written only
// when it is one of a user (people type passwords where the login goes).
func (p *Panel) loginDetail(ctx context.Context, login, ip string, r *http.Request) map[string]any {
	d := map[string]any{"ip": ip, "device": clip(r.UserAgent(), 300)}
	if u, err := p.users.ByLogin(ctx, login); err == nil {
		d["user_id"], d["login"] = u.ID, login
	}
	return d
}

// warnBlocked tells the Owner that the login of a user was blocked.
func (p *Panel) warnBlocked(ctx context.Context, login string, until time.Time) {
	if p.alerts == nil {
		return
	}
	u, err := p.users.ByLogin(ctx, login)
	if err != nil {
		return // a login nobody has: nothing to warn about
	}
	p.alerts.OwnerAlert(ctx, fmt.Sprintf("O login de %s foi bloqueado por %d minutos: %d senhas erradas em %d minutos.",
		u.Name, int(LoginBlockFor/time.Minute), LoginErrors, int(AttemptWindow/time.Minute)))
}

func (p *Panel) handleFirstAccess(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in FirstAccessRequest
	if !decode(w, r, &in) {
		return
	}
	ip := clientIP(r)
	// the code is a secret of 31^12: the wrong ones are limited by IP all the same
	if until, err := p.setupClosedFor(ctx, "access:"+ip); err != nil {
		WriteError(w, errInternal())
		return
	} else if !until.IsZero() {
		WriteError(w, Err(CodeRateLimited, "muitas tentativas; tente mais tarde", map[string]any{"retry_after_seconds": retryAfter(p.clk.Now(), until)}))
		return
	}
	var (
		u   users.User
		err error
	)
	if !p.hashing(w, func() {
		u, err = p.users.RedeemAccessCode(ctx, in.Code, users.SetupInput{Login: in.Login, Password: in.Password, PIN: in.PIN})
	}) {
		return
	}
	switch {
	case errors.Is(err, users.ErrBadCode):
		_ = p.failSetup(ctx, "access:"+ip)
		p.audit(ctx, "panel.first_access.failed", map[string]any{"ip": ip})
		WriteError(w, errInvalidCode())
		return
	case p.inputError(w, err):
		return
	case err != nil:
		p.log.Error("panel: the first access", "err", err)
		WriteError(w, errInternal())
		return
	}
	p.open(w, r, u, "panel.first_access")
}

func (p *Panel) handleLogout(w http.ResponseWriter, r *http.Request) {
	u, ss := UserOf(r), SessionOf(r)
	if err := p.sessions.End(r.Context(), ss.ID); err != nil {
		WriteError(w, errInternal())
		return
	}
	p.audit(r.Context(), "panel.logout", map[string]any{"user_id": u.ID, "session": ss.ID, "ip": clientIP(r)})
	p.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (p *Panel) handleMe(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, p.me(r.Context(), UserOf(r), SessionOf(r)))
}

func (p *Panel) handlePassword(w http.ResponseWriter, r *http.Request) {
	u, ss := UserOf(r), SessionOf(r)
	var in PasswordRequest
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	// the current password is guessed at the same limit as a login
	if ok, until, err := p.reserveLogin(ctx, u.Login); err != nil {
		WriteError(w, errInternal())
		return
	} else if !ok {
		WriteError(w, Err(CodeLoginBlocked, "muitos erros de senha; tente mais tarde", map[string]any{"retry_after_seconds": retryAfter(p.clk.Now(), until)}))
		return
	}
	var err error
	if !p.hashing(w, func() { err = p.users.ChangePassword(ctx, u.ID, in.Current, in.New, ss.ID) }) {
		return
	}
	if errors.Is(err, users.ErrWrongPassword) {
		if until, blocked, ferr := p.failLogin(ctx, u.Login); ferr == nil && blocked {
			p.warnBlocked(ctx, u.Login, until)
		}
	} else if err == nil {
		_ = p.clearLogin(ctx, u.Login)
	}
	if p.inputError(w, err) {
		p.audit(r.Context(), "panel.password.refused", map[string]any{"user_id": u.ID, "session": ss.ID, "ip": clientIP(r)})
		return
	}
	if err != nil {
		p.log.Error("panel: changing the password", "err", err)
		WriteError(w, errInternal())
		return
	}
	p.audit(r.Context(), "panel.password.changed", map[string]any{"user_id": u.ID, "session": ss.ID, "ip": clientIP(r)})
	w.WriteHeader(http.StatusNoContent)
}

// handleAccessCode gives the Owner the code of first access of a user, or the
// code that resets his password. Both ask for the PIN of the Owner ("gerar códigos").
func (p *Panel) handleAccessCode(w http.ResponseWriter, r *http.Request) {
	{
		ctx := r.Context()
		var in AccessCodeRequest
		if !decode(w, r, &in) {
			return
		}
		target := r.PathValue("id")
		c, err := p.users.NewAccessCode(ctx, UserOf(r), target, in.Kind)
		switch {
		case errors.Is(err, users.ErrNoUser):
			WriteError(w, Err(CodeNotFound, "usuário não encontrado", nil))
			return
		case errors.Is(err, users.ErrLevel):
			WriteError(w, Err(CodeForbiddenLevel, err.Error(), nil))
			return
		case err != nil:
			WriteError(w, Err(CodeBadRequest, err.Error(), nil))
			return
		}
		p.audit(ctx, "panel.access_code.created", map[string]any{"user_id": UserOf(r).ID, "target": target, "kind": c.Kind, "expires_at": c.ExpiresAt, "ip": clientIP(r)})
		WriteJSON(w, http.StatusOK, AccessCodeResponse{Code: c.Code, Kind: c.Kind, ExpiresAt: c.ExpiresAt})
	}
}

func (p *Panel) handleLanguage(w http.ResponseWriter, r *http.Request) {
	u := UserOf(r)
	var in LanguageRequest
	if !decode(w, r, &in) {
		return
	}
	switch err := p.users.SetLanguage(r.Context(), u.ID, in.Language); {
	case errors.Is(err, users.ErrBadLanguage):
		WriteError(w, Err(CodeBadRequest, "idioma inválido", nil))
		return
	case err != nil:
		WriteError(w, errInternal())
		return
	}
	u.Language = in.Language
	WriteJSON(w, http.StatusOK, p.me(r.Context(), u, SessionOf(r)))
}

// handleLastWorkspace remembers the workspace the user opened last. Only one the
// user sees is kept: the server decides, not the browser.
func (p *Panel) handleLastWorkspace(w http.ResponseWriter, r *http.Request) {
	u := UserOf(r)
	var in LastWorkspaceRequest
	if !decode(w, r, &in) {
		return
	}
	if in.Workspace == "" || !visible(u, in.Workspace) {
		WriteError(w, Err(CodeNotFound, "campanha não encontrada", nil))
		return
	}
	if err := p.users.SetLastWorkspace(r.Context(), u.ID, in.Workspace); err != nil {
		WriteError(w, errInternal())
		return
	}
	u.LastWorkspace = in.Workspace
	WriteJSON(w, http.StatusOK, p.me(r.Context(), u, SessionOf(r)))
}
