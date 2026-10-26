package panel

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// CookieName is the cookie of the session: HttpOnly, Secure, SameSite=Strict,
// path "/" and no Domain, which is what the __Host- prefix demands of a browser.
const CookieName = "__Host-mestre"

// PINHeader carries the PIN of a sensitive action, in the very request of the
// action. A PIN checked for one action is spent by it and is worth no other.
const PINHeader = "X-Mestre-Pin"

// maxBody is the largest body of the API the panel reads.
const maxBody = 1 << 20

// hashSlots is how many argon2 hashes the panel does at once.
const hashSlots = 3

// sweepEvery is how often the sessions that are over are ended.
const sweepEvery = time.Minute

// Options are the pieces of the Panel.
type Options struct {
	DB    *store.DB
	Clock clock.Clock
	Users *users.Service
	// Alerts tells the Owner (nil: nobody).
	Alerts users.Alerts
	// Assets are the files of the front end (nil: Assets(), the embedded build).
	Assets fs.FS
	// Machine is the id of this machine, for the audit.
	Machine string
	Logger  *slog.Logger
	// GitHub is the device flow of the personal credentials (nil: "Conectar
	// GitHub" answers that it is not available).
	GitHub PersonalGitHub
	// Vault is the vault of the Citadel for the screen of the secrets (nil: the
	// screen answers that it is not available).
	Vault SecretVault
	// VaultChanged tells the Towers the vault changed (a new recovery envelope);
	// nil: nobody is told.
	VaultChanged func(ctx context.Context)
	// Rand is the source of the tokens (crypto/rand by default).
	Rand io.Reader
	// Approvals is the flow the central of approvals answers through (nil: the
	// central answers "unavailable") and Notices the feed of the bell (nil: no bell).
	Approvals *ApprovalSource
	Notices   *NoticeSource
	// Chat is the conversation with the Grão-Mestre (nil: the panel answers that it is
	// not available).
	Chat ChatSource
	// Creation is the creation of the GitHub Apps by link: the page of the link is
	// served at /github/apps/novo/<token> (nil: the Owner cannot make a link now).
	Creation *github.Creation
}

// Panel is the panel of the acting Citadel.
type Panel struct {
	db           *store.DB
	clk          clock.Clock
	users        *users.Service
	alerts       users.Alerts
	machine      string
	github       PersonalGitHub
	vault        SecretVault
	vaultChanged func(ctx context.Context)
	log          *slog.Logger
	ids          *protocol.IDGen
	sessions     *Sessions
	hub          *Hub
	appr         *ApprovalSource
	notes        *NoticeSource
	chat         ChatSource
	// stop ends what the areas registered with the engine (the listeners of the
	// flow and of the feed), when the panel closes.
	stop    []func()
	handler http.Handler
	// terminals are the terminals of the Guild (SetTerminals).
	terminals atomic.Pointer[terminalHolder]
	stopWatch context.CancelFunc
	// canvas is the engine of the tables for the canvas (SetCanvas).
	canvas atomic.Pointer[canvasHolder]
	// rules is the backend of the screens of the rules and of the chain (SetRules).
	rules atomic.Pointer[rulesHolder]
	// manage is the engine side of the screens of the classes, bases, profiles and usage (SetManage).
	manage atomic.Pointer[manageHolder]
	// creation is the creation of the Apps by link (nil: no link, no Apps screen action).
	creation *github.Creation
	// workspaces is the engine of the workspaces for their screens (SetWorkspaces).
	workspaces atomic.Pointer[workspaceHolder]
	// guild is the engine of the Guild for its screen (SetGuild).
	guild atomic.Pointer[guildHolder]
	// guildFollow keeps the polling of the Guild while somebody follows it.
	guildFollow guildFollow
	// slots bound the argon2 hashes at once: each takes 64 MiB, and the routes
	// that hash are open to anybody
	slots chan struct{}
}

// New builds the Panel and mounts the routes of every area registered.
func New(o Options) (*Panel, error) {
	if o.DB == nil || o.Clock == nil || o.Users == nil {
		return nil, errors.New("panel: the Guild database, the clock and the users are required")
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Assets == nil {
		o.Assets = Assets()
	}
	if o.Rand == nil {
		o.Rand = newRand()
	}
	p := &Panel{db: o.DB, clk: o.Clock, users: o.Users, alerts: o.Alerts, machine: o.Machine, github: o.GitHub, vault: o.Vault, vaultChanged: o.VaultChanged, log: o.Logger, ids: protocol.NewIDGen(o.Clock), appr: o.Approvals, notes: o.Notices, chat: o.Chat, creation: o.Creation}
	p.sessions = &Sessions{db: o.DB, clk: o.Clock, ids: p.ids, rnd: o.Rand}
	p.hub = newHub(p)
	p.slots = make(chan struct{}, hashSlots)
	p.sessions.ended = p.hub.closeSession
	p.listenApprovals()
	p.listenNotices()
	p.listenChat()
	// the sessions the users service ended in the database: close their sockets
	o.Users.OnSessionsEnded(func(ctx context.Context, userID string) { p.hub.closeUserGone(ctx, userID) })

	mux := http.NewServeMux()
	r := &Router{p: p, mux: mux}
	for _, a := range registered() {
		a.mount(r)
	}
	mux.HandleFunc("GET /ws/ui", p.serveWS)
	if o.Creation != nil {
		// the three pages of the creation of an App: the link, the return of the manifest
		// and the return of the installation; they say who is logged in through the panel
		web := &github.Web{Creation: o.Creation, Who: p.whoIs}
		mux.Handle("/github/", continueCrossSite(web.Handler()))
	}
	mux.Handle("/api/", p.require(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, Err(CodeNotFound, "não encontrado", nil))
	}, false))
	mux.Handle("/", static(o.Assets))
	p.handler = secure(mux)
	// the canvas follows the commits from the start, so none is missed
	watchCtx, stop := context.WithCancel(context.Background())
	p.stopWatch = stop
	go p.watchCanvas(watchCtx, p.db.LogWatch())
	return p, nil
}

// ServeHTTP serves the panel; mount it behind Gate.
func (p *Panel) ServeHTTP(w http.ResponseWriter, r *http.Request) { p.handler.ServeHTTP(w, r) }

// Hub is the channel of the browser, for the areas that publish to it.
func (p *Panel) Hub() *Hub { return p.hub }

// Run ends the sessions that are over, every minute, until ctx ends.
func (p *Panel) Run(ctx context.Context) {
	t := p.clk.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
			if _, err := p.sessions.Sweep(ctx); err != nil && ctx.Err() == nil {
				p.log.Warn("panel: the sessions that are over were not swept", "err", err)
			}
		}
	}
}

// Close closes every socket of the browser.
func (p *Panel) Close() {
	for _, stop := range p.stop {
		stop()
	}
	p.stopWatch()
	p.hub.closeAll()
}

// secure sets the headers every answer of the panel carries.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

type ctxKey int

const (
	keyUser ctxKey = iota
	keySession
)

// UserOf is the user a request is made by (routes behind Auth, Require and
// Sensitive only).
func UserOf(r *http.Request) users.User {
	u, _ := r.Context().Value(keyUser).(users.User)
	return u
}

// SessionOf is the session a request is made in.
func SessionOf(r *http.Request) Session {
	s, _ := r.Context().Value(keySession).(Session)
	return s
}

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// originOK says whether the Origin of the request is the address of the panel:
// the host the browser used. A mutating request, and the opening of the socket,
// without it or with another is refused.
func originOK(r *http.Request) bool {
	o := r.Header.Get("Origin")
	return o != "" && strings.EqualFold(o, "https://"+r.Host)
}

func mutating(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// authenticate finds the session and the user of the request.
func (p *Panel) authenticate(r *http.Request) (context.Context, *APIError) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return nil, errUnauthenticated()
	}
	ss, err := p.sessions.Lookup(r.Context(), c.Value)
	switch {
	case errors.Is(err, ErrNoSession):
		return nil, errUnauthenticated()
	case err != nil:
		p.log.Error("panel: reading the session", "err", err)
		return nil, errInternal()
	}
	u, err := p.users.Get(r.Context(), ss.UserID)
	if err != nil || u.State != users.Active {
		_ = p.sessions.End(r.Context(), ss.ID)
		return nil, errUnauthenticated()
	}
	ctx := context.WithValue(r.Context(), keyUser, u)
	return context.WithValue(ctx, keySession, ss), nil
}

// whoIs is the user logged in for a request outside the API (the pages of the creation
// of an App): the session of the cookie, the same as the API's.
func (p *Panel) whoIs(r *http.Request) (users.User, bool) {
	ctx, apiErr := p.authenticate(r)
	if apiErr != nil {
		return users.User{}, false
	}
	return UserOf(r.WithContext(ctx)), true
}

// require wraps h: the Origin of a mutating request, then a valid session
// (401 without one), then, when cap is not "", the level of the user in the table
// of levels, read from the database at each request.
func (p *Panel) require(h http.HandlerFunc, public bool, caps ...users.Capability) http.Handler {
	return p.requireLimit(maxBody, h, public, caps...)
}

// requireLimit is require with its own limit for the body: the routes that take a file.
func (p *Panel) requireLimit(limit int64, h http.HandlerFunc, public bool, caps ...users.Capability) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		if mutating(r.Method) && !originOK(r) {
			if _, err := r.Cookie(CookieName); err == nil { // a request with no session writes nothing
				p.audit(r.Context(), "panel.origin_refused", map[string]any{"ip": clientIP(r), "path": clip(r.URL.Path, 200)})
			}
			WriteError(w, Err(CodeBadOrigin, "origem não permitida", nil))
			return
		}
		if public {
			h(w, r)
			return
		}
		ctx, apiErr := p.authenticate(r)
		if apiErr != nil {
			WriteError(w, apiErr)
			return
		}
		r = r.WithContext(ctx)
		for _, c := range caps {
			if err := users.Require(UserOf(r).Level, c); err != nil {
				var le *users.LevelError
				params := map[string]any{"capability": string(c)}
				if errors.As(err, &le) {
					params["need"], params["have"] = string(le.Need), string(le.Have)
				}
				WriteError(w, Err(CodeForbiddenLevel, "ação não permitida: "+err.Error(), params))
				return
			}
		}
		h(w, r)
	})
}

// Router registers the routes of an area. Patterns are those of http.ServeMux
// ("GET /api/things/{id}").
type Router struct {
	p   *Panel
	mux *http.ServeMux
}

// Panel is the panel the routes belong to.
func (r *Router) Panel() *Panel { return r.p }

// Public registers a route that needs no session (the login). A mutating
// request still has its Origin checked.
func (r *Router) Public(pattern string, h http.HandlerFunc) {
	r.mux.Handle(pattern, r.p.require(h, true))
}

// Auth registers a route that needs a session.
func (r *Router) Auth(pattern string, h http.HandlerFunc) {
	r.mux.Handle(pattern, r.p.require(h, false))
}

// Require registers a route that needs a session and a level that has all of the
// capabilities of the table of levels.
func (r *Router) Require(pattern string, h http.HandlerFunc, caps ...users.Capability) {
	r.mux.Handle(pattern, r.p.require(h, false, caps...))
}

// RequireLimit is Require for a route whose body may be as large as limit bytes (the
// others take 1 MiB): the one that takes the files of a message.
func (r *Router) RequireLimit(pattern string, limit int64, h http.HandlerFunc, caps ...users.Capability) {
	r.mux.Handle(pattern, r.p.requireLimit(limit, h, false, caps...))
}

// Sensitive registers a route of an action that asks for the PIN, in the same
// request (header PINHeader): "PIN obrigatório" without it. The capability, if
// not "", is checked first. action names the action in the request of PIN and in
// the audit.
func (r *Router) Sensitive(pattern, action string, c users.Capability, h http.HandlerFunc) {
	var caps []users.Capability
	if c != "" {
		caps = append(caps, c)
	}
	r.mux.Handle(pattern, r.p.require(r.p.withPIN(action, h), false, caps...))
}

// SensitiveRequest is Sensitive for an action whose service spends the request of
// PIN itself: h gets the id of the request, answered right and still open.
func (r *Router) SensitiveRequest(pattern, action string, c users.Capability, h func(w http.ResponseWriter, r *http.Request, request string)) {
	var caps []users.Capability
	if c != "" {
		caps = append(caps, c)
	}
	r.mux.Handle(pattern, r.p.require(r.p.withPINRequest(action, h), false, caps...))
}

// area is a set of routes, in a file routes_<area>.go.
type area struct {
	name  string
	mount func(*Router)
}

var (
	areasMu sync.Mutex
	areas   []area
)

// RegisterArea adds the routes of an area to every Panel; call it from an init
// of the file routes_<area>.go of the area, so that a new area touches no file
// that is shared.
func RegisterArea(name string, mount func(*Router)) {
	areasMu.Lock()
	defer areasMu.Unlock()
	areas = append(areas, area{name, mount})
}

func registered() []area {
	areasMu.Lock()
	defer areasMu.Unlock()
	return append([]area(nil), areas...)
}
