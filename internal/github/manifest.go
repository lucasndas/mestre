package github

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/internal/vault"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The times of the creation of an App by link
// (docs/github/criacao-de-apps.md#o-link-e-o-fluxo-de-manifesto).
const (
	// LinkLife is how long the link is good (the default).
	LinkLife = 24 * time.Hour
	// CodeLife is how long the Owner has, from opening the link, to hand the
	// code of the manifest back: GitHub's own limit.
	CodeLife = time.Hour
	// InstallPoll is how often the installation is looked for when the return
	// of the setup URL does not come, for up to an hour.
	InstallPoll = 30 * time.Second
	// maxBody caps what is read from GitHub.
	maxBody = 1 << 20
)

// The refusals of the creation flow.
var (
	// ErrBadState: the callback does not carry the state of an open request; the
	// code is not exchanged.
	ErrBadState = errors.New("github: the return of GitHub does not belong to a request of the Mestre")
	// ErrLinkGone: the link is expired, used or replaced by a newer one.
	ErrLinkGone = errors.New("github: this link is no longer valid")
	// ErrOwnerOnly: only the Owner creates Apps (they are born in his account).
	ErrOwnerOnly = errors.New("github: only the Owner creates the Apps")
	// ErrCodeLate: the code of the manifest was not handed back within an hour.
	ErrCodeLate = errors.New("github: the code of the manifest was not exchanged within one hour")
	// ErrWrongOwner: the App was made in an account that is not the Owner's.
	ErrWrongOwner = errors.New("github: the App was made in an account that is not the Owner's")
	// ErrWrongInstallation: the installation is not of this App, or not of the
	// Owner's account.
	ErrWrongInstallation = errors.New("github: the installation is not of the App")
	// ErrDeviceFlowOff: the App "Grão-Mestre" has no "Enable Device Flow".
	ErrDeviceFlowOff = errors.New("github: the App has the device flow disabled")
	// ErrAppState: the App is not in a state that asks for a link.
	ErrAppState = errors.New("github: the App does not wait for a link now")
)

// LimitedError is the conversion refused for the limit of use of GitHub: the
// code stays good for the rest of the hour and the callback may be tried again.
type LimitedError struct{ RetryAfter time.Duration }

func (e *LimitedError) Error() string {
	return fmt.Sprintf("github: the conversion of the manifest was limited; try again in %s", e.RetryAfter)
}

// Vault is what the creation needs of the vault: the key goes in and, when the
// App is not used, out.
type Vault interface {
	Keys
	Put(ctx context.Context, name string, typ vault.Type, value []byte, site *vault.SiteMeta) error
	Delete(ctx context.Context, name string) error
}

var _ Vault = (*vault.Vault)(nil)

// CreationOptions are the pieces of Creation.
type CreationOptions struct {
	Apps   *Registry
	Tokens *Tokens
	Vault  Vault
	// API is the REST API (https://api.github.com); Web is github.com, where the
	// Owner clicks and the device flow starts.
	API, Web string
	HTTP     *http.Client
	// Panel is the address of the panel the Owner is sent back to, without the
	// path (`https://mestre.local:<port>`): always the name, never an IP.
	Panel func() string
	// OwnerLogin is the login on GitHub of the Owner of the Mestre.
	OwnerLogin func(ctx context.Context) (string, error)
	// Tell says something to the Owner (the bot and the panel); nil: nothing.
	Tell func(ctx context.Context, m i18n.Msg)
	// OnReady is told an App that became ready (the Grão-Mestre one makes the
	// repository of the lock then); nil: nobody.
	OnReady func(ctx context.Context, app App)
	Logger  *slog.Logger
}

// Creation makes the Apps by the manifest flow: a link for the Owner, the
// exchange of the code, the key to the vault and the check of the installation.
type Creation struct {
	o   CreationOptions
	db  *store.DB
	clk clock.Clock
	// install serializes the confirmation of an installation.
	install sync.Mutex
}

// NewCreation returns the service.
func NewCreation(o CreationOptions) (*Creation, error) {
	if o.Apps == nil || o.Tokens == nil || o.Vault == nil || o.Panel == nil || o.OwnerLogin == nil {
		return nil, errors.New("github: the creation of Apps needs the registry, the tokens, the vault, the panel address and the Owner's login")
	}
	if o.HTTP == nil {
		o.HTTP = http.DefaultClient
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Creation{o: o, db: o.Apps.DB, clk: o.Apps.Clock}, nil
}

// Link is the link of a request.
type Link struct {
	App       string
	URL       string
	ExpiresAt time.Time
}

func hashOf(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// stateOf is the state of the request of a token: the Owner's browser carries it
// to GitHub and back, and only a callback with it is accepted.
func stateOf(token string) string { return hex.EncodeToString(hashOf("state:" + token)[:24]) }

func (c *Creation) audit(ctx context.Context, app, action, user, detail string) {
	writeAudit(ctx, c.db, c.clk, c.o.Logger, app, action, user, detail)
}

// writeAudit appends a row to the audit of the Apps and credentials.
func writeAudit(ctx context.Context, db *store.DB, clk clock.Clock, log *slog.Logger, app, action, user, detail string) {
	err := db.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO github_app_audit (at, app, action, user_id, detail) VALUES (?, ?, ?, ?, ?)`,
			clk.Now().UnixMilli(), app, action, user, detail)
		return err
	})
	if err != nil {
		log.Error("github: the audit of an App was not written", "app", app, "action", action, "err", err)
	}
}

func (c *Creation) tell(ctx context.Context, m i18n.Msg) {
	if c.o.Tell != nil {
		c.o.Tell(ctx, m)
	}
}

// Begin makes the link of an App that waits for its creation (or whose link
// expired): a token of single use, good for 24 hours, tied to the request. A
// newer link replaces the older one of the same App.
func (c *Creation) Begin(ctx context.Context, appName, requestedBy string) (Link, error) {
	app, err := c.o.Apps.Get(ctx, appName)
	if err != nil {
		return Link{}, err
	}
	switch app.State {
	case classes.AppAwaitingCreation:
	case classes.AppExpired:
		if err := c.o.Apps.SetState(ctx, app.Name, classes.AppAwaitingCreation); err != nil {
			return Link{}, err
		}
	default:
		return Link{}, fmt.Errorf("%w: %s is %s", ErrAppState, app.Name, app.State.Label())
	}
	now := c.clk.Now()
	var busy int
	if err := c.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM github_app_requests WHERE app = ? AND status = 'exchanging' AND code_deadline > ?`,
		app.Name, now.UnixMilli()).Scan(&busy); err != nil {
		return Link{}, err
	}
	if busy > 0 { // the code is being exchanged: a second request would race it for the key
		return Link{}, fmt.Errorf("%w: %s is being created", ErrAppState, app.Name)
	}
	token, err := randomToken(32)
	if err != nil {
		return Link{}, err
	}
	expires := now.Add(LinkLife)
	err = c.db.WriteDurable(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE github_app_requests SET status = 'replaced', updated_at = ? WHERE app = ? AND status IN ('link', 'opened')`,
			now.UnixMilli(), app.Name); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO github_app_requests (app, link_hash, state_hash, status, requested_by, link_expires_at, created_at, updated_at)
			VALUES (?, ?, ?, 'link', ?, ?, ?, ?)`,
			app.Name, hashOf(token), hashOf(stateOf(token)), requestedBy, expires.UnixMilli(), now.UnixMilli(), now.UnixMilli())
		return err
	})
	if err != nil {
		return Link{}, err
	}
	c.audit(ctx, app.Name, "link", requestedBy, "")
	return Link{App: app.Name, URL: strings.TrimRight(c.o.Panel(), "/") + "/github/apps/novo/" + token, ExpiresAt: expires}, nil
}

// request is a row of github_app_requests.
type request struct {
	id                    int64
	app, status, openedBy string
	linkExpires, deadline time.Time
	polledAt, updatedAt   time.Time
	flowTold              bool
}

const requestColumns = `id, app, status, opened_by, link_expires_at, code_deadline, polled_at, updated_at, flow_told`

func scanRequest(sc interface{ Scan(...any) error }) (request, error) {
	var r request
	var exp, dl, pol, upd, told int64
	if err := sc.Scan(&r.id, &r.app, &r.status, &r.openedBy, &exp, &dl, &pol, &upd, &told); err != nil {
		return request{}, err
	}
	r.linkExpires, r.deadline, r.polledAt, r.updatedAt = time.UnixMilli(exp), time.UnixMilli(dl), time.UnixMilli(pol), time.UnixMilli(upd)
	r.flowTold = told == 1
	return r, nil
}

func (c *Creation) byColumn(ctx context.Context, col string, hash []byte) (request, error) {
	r, err := scanRequest(c.db.Reader().QueryRowContext(ctx, `SELECT `+requestColumns+` FROM github_app_requests WHERE `+col+` = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return request{}, ErrBadState
	}
	return r, err
}

func (c *Creation) setStatus(ctx context.Context, id int64, from, to string) (bool, error) {
	var moved bool
	err := c.db.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE github_app_requests SET status = ?, updated_at = ? WHERE id = ? AND status = ?`,
			to, c.clk.Now().UnixMilli(), id, from)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		moved = n == 1
		return nil
	})
	return moved, err
}

// Manifest is the manifest sent to GitHub (the fields of the table of
// docs/github/criacao-de-apps.md#conteúdo-do-manifesto).
type Manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	Description        string            `json:"description"`
	Public             bool              `json:"public"`
	HookAttributes     map[string]any    `json:"hook_attributes"`
	RedirectURL        string            `json:"redirect_url"`
	SetupURL           string            `json:"setup_url"`
	DefaultPermissions map[string]string `json:"default_permissions"`
	DefaultEvents      []string          `json:"default_events"`
	RequestOAuth       bool              `json:"request_oauth_on_install"`
	SetupOnUpdate      bool              `json:"setup_on_update"`
}

// Page is what the Owner sees before the click: the App, its permissions and
// the form that goes to GitHub.
type Page struct {
	App         App
	Permissions map[string]string
	Manifest    Manifest
	// ManifestJSON is the manifest as the form field carries it.
	ManifestJSON string
	// FormAction is where the form is posted: the page of the new App on GitHub,
	// with the state.
	FormAction string
	// DeviceFlow says the App needs "Enable Device Flow" after the creation.
	DeviceFlow bool
}

func (c *Creation) manifestOf(app App, ownerLogin string) Manifest {
	panel := strings.TrimRight(c.o.Panel(), "/")
	desc := "App do Mestre"
	switch app.Role {
	case RoleClass:
		desc += " para a classe " + app.Class
	case RoleGrandMaster:
		desc += ": Grão-Mestre"
	default:
		desc += ": Mestre"
	}
	return Manifest{
		Name: app.Name, URL: "https://github.com/" + ownerLogin, Description: desc, Public: false,
		HookAttributes: map[string]any{"url": "https://example.invalid", "active": false},
		RedirectURL:    panel + "/github/manifest/callback", SetupURL: panel + "/github/apps/instalado",
		DefaultPermissions: RolePermissions(app.Role, app.Workflows), DefaultEvents: []string{},
		SetupOnUpdate: true,
	}
}

// Open is the Owner opening the link: it checks who opens and the link, and
// gives the page with the manifest. The hour to hand the code back starts at the
// first opening.
func (c *Creation) Open(ctx context.Context, token string, who users.User) (Page, error) {
	if token == "" {
		return Page{}, ErrLinkGone
	}
	if err := users.Require(who.Level, users.GitHubApps); err != nil || who.State != users.Active {
		return Page{}, ErrOwnerOnly
	}
	r, err := c.byColumn(ctx, "link_hash", hashOf(token))
	if err != nil {
		return Page{}, ErrLinkGone
	}
	now := c.clk.Now()
	switch r.status {
	case "link":
		if !now.Before(r.linkExpires) {
			c.expire(ctx, r)
			return Page{}, ErrLinkGone
		}
		if _, err := c.openRequest(ctx, r, who.ID, now); err != nil {
			return Page{}, err
		}
		c.audit(ctx, r.app, "opened", who.ID, "")
	case "opened":
		if !now.Before(r.deadline) {
			c.fail(ctx, r, "opened", "o código do manifesto não foi entregue em 1 hora")
			return Page{}, ErrCodeLate
		}
		if r.openedBy != who.ID { // authorized by what is recorded: the one who opened it
			return Page{}, ErrOwnerOnly
		}
	default:
		return Page{}, ErrLinkGone
	}
	app, err := c.o.Apps.Get(ctx, r.app)
	if err != nil {
		return Page{}, err
	}
	login, err := c.o.OwnerLogin(ctx)
	if err != nil || login == "" {
		return Page{}, fmt.Errorf("github: the login of the Owner on GitHub: %v", err)
	}
	m := c.manifestOf(app, login)
	raw, err := json.Marshal(m)
	if err != nil {
		return Page{}, err
	}
	return Page{App: app, Permissions: m.DefaultPermissions, Manifest: m, ManifestJSON: string(raw),
		FormAction: strings.TrimRight(c.web(), "/") + "/settings/apps/new?state=" + url.QueryEscape(stateOf(token)),
		DeviceFlow: app.Role == RoleGrandMaster}, nil
}

func (c *Creation) web() string {
	if c.o.Web == "" {
		return "https://github.com"
	}
	return c.o.Web
}

func (c *Creation) api() string {
	if c.o.API == "" {
		return "https://api.github.com"
	}
	return strings.TrimRight(c.o.API, "/")
}

func (c *Creation) openRequest(ctx context.Context, r request, by string, now time.Time) (bool, error) {
	var moved bool
	err := c.db.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE github_app_requests SET status = 'opened', opened_by = ?, code_deadline = ?, updated_at = ? WHERE id = ? AND status = 'link'`,
			by, now.Add(CodeLife).UnixMilli(), now.UnixMilli(), r.id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		moved = n == 1
		return nil
	})
	if err == nil && !moved {
		err = ErrLinkGone
	}
	return moved, err
}

// expire ends a link nobody opened in time: the App waits for a new one and the
// Owner is told.
func (c *Creation) expire(ctx context.Context, r request) {
	moved, err := c.setStatus(ctx, r.id, "link", "expired")
	if err != nil || !moved {
		return
	}
	if app, err := c.o.Apps.Get(ctx, r.app); err == nil && app.State == classes.AppAwaitingCreation {
		_ = c.o.Apps.SetState(ctx, r.app, classes.AppExpired)
	}
	c.audit(ctx, r.app, "expired", "", "")
	c.tell(ctx, i18n.M("github.app_link_expired", "app", r.app))
}

// fail ends a request whose code was not handed back: the App may exist at
// GitHub without its key here, so the Owner is told to delete it, and the next
// request takes the next name.
func (c *Creation) fail(ctx context.Context, r request, from, why string) {
	moved, err := c.setStatus(ctx, r.id, from, "failed")
	if err != nil || !moved {
		return
	}
	_ = c.o.Apps.SetState(ctx, r.app, classes.AppFailed)
	c.audit(ctx, r.app, "failed", "", why)
	c.tell(ctx, i18n.M("github.app_creation_failed", "app", r.app, "why", why))
}

// conversion is what GitHub answers to the code of a manifest.
type conversion struct {
	ID       int64  `json:"id"`
	Slug     string `json:"slug"`
	ClientID string `json:"client_id"`
	PEM      string `json:"pem"`
	// ClientSecret renews and revokes the user tokens of the App (kept only for
	// the App "Grão-Mestre", the one with the device flow).
	ClientSecret string `json:"client_secret"`
	Owner        struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// Callback is the return of GitHub after the click on "Create GitHub App": it
// exchanges the code, checks the owner of the App and puts the key in the vault.
// It answers the address where the Owner installs the App. A state that is not
// an open request is refused before anything goes to GitHub.
func (c *Creation) Callback(ctx context.Context, code, state string, who users.User) (string, error) {
	if err := users.Require(who.Level, users.GitHubApps); err != nil || who.State != users.Active {
		return "", ErrOwnerOnly
	}
	if code == "" || state == "" {
		return "", ErrBadState
	}
	r, err := c.byColumn(ctx, "state_hash", hashOf(state))
	if err != nil {
		return "", err
	}
	if r.status != "opened" {
		return "", ErrBadState
	}
	if r.openedBy != who.ID {
		return "", ErrOwnerOnly
	}
	if !c.clk.Now().Before(r.deadline) {
		c.fail(ctx, r, "opened", "o código do manifesto não foi entregue em 1 hora")
		return "", ErrCodeLate
	}
	// who the Owner is must be known before the code is spent
	owner, oerr := c.o.OwnerLogin(ctx)
	if oerr != nil || owner == "" {
		return "", errors.New("github: the login of the Owner on GitHub is not known")
	}
	// one callback at a time uses the code
	if moved, err := c.setStatus(ctx, r.id, "opened", "exchanging"); err != nil || !moved {
		if err == nil {
			err = ErrBadState
		}
		return "", err
	}
	// from here the Owner's browser may leave: what is started is finished, and
	// the state of the request never stays in 'exchanging' for that
	bg := context.WithoutCancel(ctx)
	work, cancel := clock.WithTimeout(bg, c.clk, time.Minute)
	defer cancel()
	conv, err := c.convert(work, code)
	if err != nil {
		// the code may still be good (a limit, a lost answer): the request stays open
		_, _ = c.setStatus(bg, r.id, "exchanging", "opened")
		c.audit(bg, r.app, "conversion_failed", who.ID, "")
		return "", err
	}
	if conv.ID == 0 || conv.Slug == "" || conv.PEM == "" {
		c.abort(bg, r, who.ID, "a resposta do GitHub veio incompleta")
		return "", errors.New("github: the answer of the conversion is incomplete")
	}
	if !strings.EqualFold(owner, conv.Owner.Login) {
		// the key was never put in the vault: nothing to erase, and the App is not used
		c.abort(bg, r, who.ID, fmt.Sprintf("o App foi criado na conta %q, que não é a do Senhor da Guilda; exclua-o naquela conta e peça outro link, abrindo-o com a conta certa do GitHub", conv.Owner.Login))
		return "", ErrWrongOwner
	}
	if err := c.o.Vault.Put(bg, KeySecret(r.app), vault.TypeGitHubAppKey, []byte(conv.PEM), nil); err != nil {
		c.abort(bg, r, who.ID, "a chave não foi guardada no cofre")
		return "", fmt.Errorf("github: the key of the App %q did not go to the vault: %w", r.app, err)
	}
	app, err := c.o.Apps.Get(bg, r.app)
	if err != nil {
		_ = c.o.Vault.Delete(bg, KeySecret(r.app))
		c.abort(bg, r, who.ID, "o App não foi lido")
		return "", err
	}
	if app.Role == RoleGrandMaster {
		if conv.ClientSecret == "" {
			_ = c.o.Vault.Delete(bg, KeySecret(r.app))
			c.abort(bg, r, who.ID, "a resposta do GitHub veio sem o segredo do cliente")
			return "", errors.New("github: the answer of the conversion has no client secret")
		}
		if err := c.o.Vault.Put(bg, ClientSecretName(r.app), vault.TypeGitHubAppKey, []byte(conv.ClientSecret), nil); err != nil {
			_ = c.o.Vault.Delete(bg, KeySecret(r.app))
			c.abort(bg, r, who.ID, "o segredo do cliente não foi guardado no cofre")
			return "", fmt.Errorf("github: the client secret of the App %q did not go to the vault: %w", r.app, err)
		}
	}
	if err := c.o.Apps.Created(bg, r.app, conv.Slug, conv.ID, conv.ClientID); err != nil {
		_ = c.o.Vault.Delete(bg, KeySecret(r.app))
		_ = c.o.Vault.Delete(bg, ClientSecretName(r.app))
		c.abort(bg, r, who.ID, "o App não foi registrado")
		return "", err
	}
	if _, err := c.setStatus(bg, r.id, "exchanging", "created"); err != nil {
		c.o.Logger.Error("github: the request of an App was not marked created", "app", r.app, "err", err)
	}
	c.audit(bg, r.app, "created", who.ID, "slug "+conv.Slug)
	return strings.TrimRight(c.web(), "/") + "/apps/" + url.PathEscape(conv.Slug) + "/installations/new", nil
}

// abort ends a request whose code was spent and whose App can not be used: the
// App is falhou, the audit says why and the Owner is told to delete the App at
// GitHub.
func (c *Creation) abort(ctx context.Context, r request, user, why string) {
	_, _ = c.setStatus(ctx, r.id, "exchanging", "failed")
	_ = c.o.Apps.SetState(ctx, r.app, classes.AppFailed)
	c.audit(ctx, r.app, "aborted", user, why)
	c.tell(ctx, i18n.M("github.app_not_used", "app", r.app, "why", why))
}

// convert is POST /app-manifests/{code}/conversions.
func (c *Creation) convert(ctx context.Context, code string) (conversion, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.api()+"/app-manifests/"+url.PathEscape(code)+"/conversions", nil)
	if err != nil {
		return conversion{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.o.HTTP.Do(req)
	if err != nil {
		// the address carries the code, a credential: it stays out of the error
		if ctx.Err() != nil {
			return conversion{}, fmt.Errorf("github: the conversion of the manifest: %w", ctx.Err())
		}
		return conversion{}, errors.New("github: the conversion of the manifest: the request failed")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		wait := time.Minute
		if v, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && v > 0 {
			wait = time.Duration(v) * time.Second
		}
		return conversion{}, &LimitedError{RetryAfter: wait}
	case resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK:
		return conversion{}, fmt.Errorf("github: the conversion of the manifest: status %d", resp.StatusCode)
	}
	var out conversion
	if err := json.Unmarshal(raw, &out); err != nil {
		return conversion{}, errors.New("github: the answer of the conversion is not readable")
	}
	return out, nil
}

// appJSON is GET path with the JWT of the App.
func (c *Creation) appGet(ctx context.Context, app App, path string, out any) (int, error) {
	var jwt string
	err := c.o.Vault.Use(ctx, KeySecret(app.Name), "sign the JWT of the App "+app.Name, func(pemBytes []byte) error {
		var serr error
		jwt, serr = signJWT(pemBytes, app.AppID, c.clk.Now())
		return serr
	})
	if err != nil {
		return 0, fmt.Errorf("github: the JWT of the App %q: %w", app.Name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.api()+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.o.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode == http.StatusOK && out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, errors.New("github: an answer of GitHub is not readable")
		}
	}
	return resp.StatusCode, nil
}

type installationDoc struct {
	ID      int64 `json:"id"`
	AppID   int64 `json:"app_id"`
	Account struct {
		Login string `json:"login"`
	} `json:"account"`
}

// Installed is the return of the installation (the setup URL): the id comes from
// the browser, so it is not taken on faith. It is confirmed with the JWT of an
// App that waits for its installation: GitHub finds it only if it is an
// installation of that App, in the Owner's account. Then the repositories of the
// installation are read and the App becomes ready. An id that is of none of the
// Apps that wait is refused and they all keep waiting.
func (c *Creation) Installed(ctx context.Context, installationID int64, who users.User) (App, error) {
	if err := users.Require(who.Level, users.GitHubApps); err != nil || who.State != users.Active {
		return App{}, ErrOwnerOnly
	}
	if installationID <= 0 {
		return App{}, ErrWrongInstallation
	}
	owner, err := c.o.OwnerLogin(ctx)
	if err != nil || owner == "" {
		return App{}, fmt.Errorf("github: the login of the Owner on GitHub: %v", err)
	}
	apps, err := c.awaiting(ctx)
	if err != nil {
		return App{}, err
	}
	for _, app := range apps {
		var doc installationDoc
		st, err := c.appGet(ctx, app, "/app/installations/"+strconv.FormatInt(installationID, 10), &doc)
		if err != nil || st != http.StatusOK {
			continue
		}
		if doc.ID != installationID || doc.AppID != app.AppID || !strings.EqualFold(doc.Account.Login, owner) {
			continue
		}
		return c.finishInstall(ctx, app, installationID, who.ID)
	}
	c.audit(ctx, "", "installation_refused", who.ID, "installation "+strconv.FormatInt(installationID, 10))
	return App{}, ErrWrongInstallation
}

// awaiting are the Apps that were created and wait for their installation, and
// the ready ones, whose installation GitHub sends back when the Owner adds
// repositories (setup_on_update).
func (c *Creation) awaiting(ctx context.Context) ([]App, error) {
	all, err := c.o.Apps.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []App
	for _, a := range all {
		if (a.State == classes.AppAwaitingInstall || a.State == classes.AppReady) && a.AppID != 0 {
			out = append(out, a)
		}
	}
	return out, nil
}

// finishInstall reads the repositories of the installation, checks the device
// flow of the Grão-Mestre and makes the App ready.
func (c *Creation) finishInstall(ctx context.Context, app App, installationID int64, by string) (App, error) {
	// the return of the browser and the poll may meet: one finishes, the other
	// finds the App ready and has nothing more to tell
	c.install.Lock()
	defer c.install.Unlock()
	if fresh, err := c.o.Apps.Get(ctx, app.Name); err == nil {
		if fresh.Ready() && !app.Ready() {
			return fresh, nil
		}
		app = fresh
	}
	tok, err := c.o.Tokens.mint(ctx, app, installationID, "", map[string]string{"metadata": "read"}, c.clk.Now())
	if err != nil {
		return App{}, fmt.Errorf("github: reading the installation of %q: %w", app.Name, err)
	}
	rest := &REST{BaseURL: c.api(), HTTP: c.o.HTTP, Token: func(context.Context, string) (string, error) { return tok.Secret(), nil }}
	repos, err := rest.InstallationRepos(ctx)
	if err != nil {
		return App{}, fmt.Errorf("github: reading the repositories of %q: %w", app.Name, err)
	}
	if app.State == classes.AppReady { // the Owner added repositories: the list is refreshed, nothing else
		if err := c.o.Apps.Installed(ctx, app.Name, installationID, repos); err != nil {
			return App{}, err
		}
		c.audit(ctx, app.Name, "repositories", by, fmt.Sprintf("installation %d, %d repositories", installationID, len(repos)))
		return c.o.Apps.Get(ctx, app.Name)
	}
	if app.Role == RoleGrandMaster {
		if err := c.deviceFlowOn(ctx, app); err != nil {
			if errors.Is(err, ErrDeviceFlowOff) && c.once(ctx, app.Name, "flow_told") {
				c.audit(ctx, app.Name, "device_flow_off", by, "")
				c.tell(ctx, i18n.M("github.app_device_flow", "app", app.Name, "slug", app.Slug))
			}
			return App{}, err
		}
	}
	// the one that wins the request is the one that makes the App ready, tells
	// the Owner and calls OnReady: the return of the browser and the poll may meet
	if _, err := c.claimInstall(ctx, app.Name); err != nil {
		return App{}, err
	}
	if err := c.o.Apps.Installed(ctx, app.Name, installationID, repos); err != nil {
		return App{}, err
	}
	if app.BotUserID == 0 {
		c.readBot(ctx, app)
	}
	c.audit(ctx, app.Name, "installed", by, fmt.Sprintf("installation %d, %d repositories", installationID, len(repos)))
	ready, err := c.o.Apps.Get(ctx, app.Name)
	if err != nil {
		return App{}, err
	}
	c.tell(ctx, i18n.M("github.app_ready", "app", ready.Name))
	if c.o.OnReady != nil {
		c.o.OnReady(ctx, ready)
	}
	return ready, nil
}

// claimInstall moves the request of the App that waits for its installation to
// done; only one caller gets true.
func (c *Creation) claimInstall(ctx context.Context, app string) (bool, error) {
	var won bool
	err := c.db.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE github_app_requests SET status = 'done', updated_at = ? WHERE app = ? AND status IN ('created', 'unconfirmed')`,
			c.clk.Now().UnixMilli(), app)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		won = n > 0
		return nil
	})
	return won, err
}

// once says true the first time it is called for the App and the flag.
func (c *Creation) once(ctx context.Context, app, flag string) bool {
	var first bool
	err := c.db.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE github_app_requests SET `+flag+` = 1 WHERE app = ? AND status IN ('created', 'unconfirmed') AND `+flag+` = 0`, app)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		first = n > 0
		return nil
	})
	return err == nil && first
}

// readBot records the id of the bot of the App, read once.
func (c *Creation) readBot(ctx context.Context, app App) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.api()+"/users/"+url.PathEscape(app.BotLogin()), nil)
	if err != nil {
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.o.HTTP.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var u struct {
		ID int64 `json:"id"`
	}
	if resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&u) == nil && u.ID != 0 {
		_ = c.o.Apps.BotID(ctx, app.Name, u.ID)
	}
}

// deviceFlowOn starts a test device flow with the client id of the App: GitHub
// refuses it while "Enable Device Flow" is off.
func (c *Creation) deviceFlowOn(ctx context.Context, app App) error {
	form := url.Values{"client_id": {app.ClientID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.web(), "/")+"/login/device/code", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.o.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("github: the test of the device flow: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		DeviceCode string `json:"device_code"`
		Error      string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&out); err != nil {
		return errors.New("github: the answer of the test of the device flow is not readable")
	}
	if out.Error == "device_flow_disabled" {
		return ErrDeviceFlowOff
	}
	if out.Error != "" || out.DeviceCode == "" {
		return fmt.Errorf("github: the test of the device flow: %s", out.Error)
	}
	return nil
}

// Sweep does what time asks: it ends the links nobody opened in 24 hours, fails
// the requests whose code was not handed back in an hour, and looks for the
// installation of the Apps that wait for it when the return of the browser did
// not come (every 30 s, for up to an hour from the creation).
func (c *Creation) Sweep(ctx context.Context) error {
	now := c.clk.Now()
	rows, err := c.db.Reader().QueryContext(ctx, `SELECT `+requestColumns+` FROM github_app_requests WHERE status IN ('link', 'opened', 'exchanging', 'created')`)
	if err != nil {
		return err
	}
	var reqs []request
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			_ = rows.Close()
			return err
		}
		reqs = append(reqs, r)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	owner, _ := c.o.OwnerLogin(ctx)
	for _, r := range reqs {
		switch {
		case r.status == "link" && !now.Before(r.linkExpires):
			c.expire(ctx, r)
		case r.status == "opened" && !now.Before(r.deadline):
			c.fail(ctx, r, "opened", "o código do manifesto não foi entregue em 1 hora")
		case r.status == "exchanging" && !now.Before(r.deadline): // the process left in the middle of the exchange
			c.fail(ctx, r, "exchanging", "a troca do código não terminou")
		case r.status == "created" && owner != "":
			c.poll(ctx, r, owner)
		}
	}
	return nil
}

// poll looks for the installation of an App that was created: GET
// /users/<login>/installation with its JWT.
func (c *Creation) poll(ctx context.Context, r request, owner string) {
	now := c.clk.Now()
	// an hour from the creation; longer only while the Grão-Mestre waits for
	// the Owner to enable the device flow
	if now.Sub(r.updatedAt) >= CodeLife && !r.flowTold {
		if moved, err := c.setStatus(ctx, r.id, "created", "unconfirmed"); err == nil && moved {
			c.audit(ctx, r.app, "installation_unconfirmed", "", "")
			c.tell(ctx, i18n.M("github.app_install_unseen", "app", r.app, "slug", strings.ToLower(Slug(r.app))))
		}
		return
	}
	last := r.polledAt
	if r.updatedAt.After(last) { // the first look is 30 s after the creation
		last = r.updatedAt
	}
	if now.Sub(last) < InstallPoll {
		return
	}
	_ = c.db.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE github_app_requests SET polled_at = ? WHERE id = ?`, now.UnixMilli(), r.id)
		return err
	})
	app, err := c.o.Apps.Get(ctx, r.app)
	if err != nil || app.State != classes.AppAwaitingInstall {
		return
	}
	var doc installationDoc
	st, err := c.appGet(ctx, app, "/users/"+url.PathEscape(owner)+"/installation", &doc)
	if err != nil || st != http.StatusOK || doc.AppID != app.AppID || doc.ID == 0 {
		return
	}
	if _, err := c.finishInstall(ctx, app, doc.ID, ""); err != nil {
		c.o.Logger.Warn("github: the installation found was not confirmed", "app", app.Name, "err", err)
	}
}

// Run does Sweep every 30 seconds until ctx ends.
func (c *Creation) Run(ctx context.Context) {
	t := c.clk.NewTicker(InstallPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
			if err := c.Sweep(ctx); err != nil && ctx.Err() == nil {
				c.o.Logger.Warn("github: the sweep of the Apps failed", "err", err)
			}
		}
	}
}

// InstallationRepos lists the repositories (`owner/name`) the installation of
// the token covers, page by page.
func (c *REST) InstallationRepos(ctx context.Context) ([]string, error) {
	var out []string
	for page := 1; page <= 50; page++ {
		var list struct {
			Repositories []struct {
				FullName string `json:"full_name"`
			} `json:"repositories"`
		}
		path := fmt.Sprintf("/installation/repositories?per_page=100&page=%d", page)
		st, hdr, raw, err := c.doAbs(ctx, http.MethodGet, "", path, nil)
		switch {
		case err != nil:
			return nil, err
		case st != http.StatusOK:
			return nil, failed("GET "+path, st, raw, hdr)
		case json.Unmarshal(raw, &list) != nil:
			return nil, errors.New("github: the list of repositories of the installation is not readable")
		}
		for _, r := range list.Repositories {
			out = append(out, r.FullName)
		}
		if len(list.Repositories) < 100 {
			return out, nil
		}
	}
	return out, errTooMany
}
