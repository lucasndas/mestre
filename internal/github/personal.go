package github

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The personal credential of a user: a user access token of the App
// "Grão-Mestre" obtained by the device flow
// (docs/github/aprovacao-no-github.md#credencial-pessoal-user-access-token-pelo-device-flow).
// The tokens live only in the vault; the database keeps what the engine knows
// besides them (github_user_credentials). Nothing here puts a token in a log,
// an error, an audit row or an argument of a process.

// The states of a credential.
const (
	CredentialConnected = "connected"
	CredentialExpired   = "expired"
)

// The refusals.
var (
	// ErrNoCredential: the user has no credential in use (never connected, expired,
	// suspended or removed, or the login of the registration changed): nothing is
	// done in her name.
	ErrNoCredential = errors.New("github: the user has no GitHub credential in use")
	// ErrLoginMismatch: the account that authorized is not the github_login of the
	// registration; the token was discarded.
	ErrLoginMismatch = errors.New("github: the GitHub account is not the login registered for the user")
	// ErrNoGitHubLogin: the registration has no github_login to check the account against.
	ErrNoGitHubLogin = errors.New("github: the user has no github_login registered")
	// ErrDeviceDenied: the user refused the authorization on github.com.
	ErrDeviceDenied = errors.New("github: the user did not authorize")
	// ErrDeviceExpired: the code of the device expired before the authorization.
	ErrDeviceExpired = errors.New("github: the code of the device expired")
	// ErrNoDeviceFlow: a poll with no connection begun.
	ErrNoDeviceFlow = errors.New("github: no connection was begun for the user")
	// ErrNoClientSecret: the secret of the client of the App is not in the vault, so
	// a token can not be renewed or revoked.
	ErrNoClientSecret = errors.New("github: the client secret of the App is not in the vault")
)

// UserTokenSecret is the name in the vault of the tokens of a user.
func UserTokenSecret(userID string) string { return "github_user_token:" + userID }

// ClientSecretName is the name in the vault of the client secret of an App
// (kept for the App "Grão-Mestre", the one with the device flow).
func ClientSecretName(app string) string { return "github_app_client_secret:" + app }

// Directory is the users (*users.Service).
type Directory interface {
	Get(ctx context.Context, id string) (users.User, error)
	List(ctx context.Context) ([]users.User, error)
}

// PersonalOptions are the pieces of Personal.
type PersonalOptions struct {
	DB    *store.DB
	Clock clock.Clock
	Apps  *Registry
	Users Directory
	Vault Vault
	// API is the REST API and Web github.com, where the device flow lives.
	API, Web string
	HTTP     *http.Client
	Logger   *slog.Logger
}

// Personal makes and keeps the personal credentials.
type Personal struct {
	o PersonalOptions

	mu      sync.Mutex
	pending map[string]*deviceGrant
	locks   map[string]*sync.Mutex
}

// deviceGrant is a connection begun: the device code stays here, in memory,
// and never goes to the panel.
type deviceGrant struct {
	code     string
	expires  time.Time
	interval time.Duration
	next     time.Time
}

// NewPersonal returns the service.
func NewPersonal(o PersonalOptions) (*Personal, error) {
	if o.DB == nil || o.Apps == nil || o.Users == nil || o.Vault == nil || o.API == "" || o.Web == "" {
		return nil, errors.New("github: the personal credentials need the database, the Apps, the users, the vault and the addresses of GitHub")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Personal{o: o, pending: map[string]*deviceGrant{}, locks: map[string]*sync.Mutex{}}, nil
}

// Challenge is what the user does on github.com: type the code at the address.
type Challenge struct {
	UserCode        string
	VerificationURI string
	ExpiresAt       time.Time
	// Interval is how often Poll may ask.
	Interval time.Duration
}

func (p *Personal) lockOf(userID string) *sync.Mutex {
	p.mu.Lock()
	defer p.mu.Unlock()
	l := p.locks[userID]
	if l == nil {
		l = &sync.Mutex{}
		p.locks[userID] = l
	}
	return l
}

func (p *Personal) audit(ctx context.Context, action, user, detail string) {
	writeAudit(ctx, p.o.DB, p.o.Clock, p.o.Logger, RoleGrandMaster, action, user, detail)
}

// form posts a form to github.com and reads the JSON answer. The body, which
// carries codes and secrets, never reaches an error.
func (p *Personal) form(ctx context.Context, path string, vals url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.o.Web, "/")+path, strings.NewReader(vals.Encode()))
	if err != nil {
		return errors.New("github: bad request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := p.o.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: POST %s failed", ErrUnavailable, path)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%w: POST %s: status %d", ErrUnavailable, path, resp.StatusCode)
	}
	if json.Unmarshal(raw, out) != nil {
		return fmt.Errorf("github: the answer of POST %s is not readable (status %d)", path, resp.StatusCode)
	}
	return nil
}

func (p *Personal) app(ctx context.Context) (App, error) {
	app, err := p.o.Apps.Fixed(ctx, RoleGrandMaster)
	if err != nil {
		return App{}, err
	}
	if !app.Ready() || app.ClientID == "" {
		return App{}, fmt.Errorf("%w: %s (%s)", ErrAppNotReady, app.Name, app.State.Label())
	}
	return app, nil
}

// Begin starts the device flow of a user: GitHub gives the code the user types
// at github.com/login/device. The user must be active, above Observer, and have
// a github_login to be checked against.
func (p *Personal) Begin(ctx context.Context, userID string) (Challenge, error) {
	u, err := p.o.Users.Get(ctx, userID)
	if err != nil {
		return Challenge{}, err
	}
	if u.State != users.Active || u.Level == users.Observer {
		return Challenge{}, ErrNoCredential
	}
	if strings.TrimSpace(u.GitHubLogin) == "" {
		return Challenge{}, ErrNoGitHubLogin
	}
	app, err := p.app(ctx)
	if err != nil {
		return Challenge{}, err
	}
	var out struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		Error           string `json:"error"`
	}
	if err := p.form(ctx, "/login/device/code", url.Values{"client_id": {app.ClientID}}, &out); err != nil {
		return Challenge{}, err
	}
	switch {
	case out.Error == "device_flow_disabled":
		return Challenge{}, ErrDeviceFlowOff
	case out.Error != "" || out.DeviceCode == "" || out.UserCode == "":
		return Challenge{}, fmt.Errorf("github: the device flow did not start: %s", safeCode(out.Error))
	}
	now := p.o.Clock.Now()
	interval := time.Duration(max(out.Interval, 1)) * time.Second
	g := &deviceGrant{code: out.DeviceCode, expires: now.Add(time.Duration(out.ExpiresIn) * time.Second), interval: interval, next: now.Add(interval)}
	p.mu.Lock()
	p.pending[userID] = g
	p.mu.Unlock()
	p.audit(ctx, "credential_begun", userID, "")
	return Challenge{UserCode: out.UserCode, VerificationURI: out.VerificationURI, ExpiresAt: g.expires, Interval: interval}, nil
}

// safeCode keeps a code of error of GitHub short and plain before it is shown.
func safeCode(s string) string {
	if len(s) > 60 || strings.ContainsFunc(s, func(r rune) bool { return r < ' ' || r > '~' }) {
		return "unknown"
	}
	return s
}

// PollResult is where a connection is.
type PollResult string

// The results of a poll.
const (
	// PollPending: the user has not authorized yet (or it is too soon to ask).
	PollPending PollResult = "pending"
	// PollConnected: the token was checked and kept.
	PollConnected PollResult = "connected"
)

// Poll asks GitHub whether the user authorized, no more often than GitHub
// said. On the token it checks with GET /user that the account is the
// github_login of the registration (otherwise the token is discarded) and keeps
// the tokens in the vault.
func (p *Personal) Poll(ctx context.Context, userID string) (PollResult, error) {
	lk := p.lockOf(userID)
	lk.Lock()
	defer lk.Unlock()
	p.mu.Lock()
	g := p.pending[userID]
	p.mu.Unlock()
	if g == nil {
		return "", ErrNoDeviceFlow
	}
	now := p.o.Clock.Now()
	if !now.Before(g.expires) {
		p.drop(userID)
		return "", ErrDeviceExpired
	}
	if now.Before(g.next) {
		return PollPending, nil
	}
	app, err := p.app(ctx)
	if err != nil {
		return "", err
	}
	var out tokenAnswer
	if err := p.form(ctx, "/login/oauth/access_token", url.Values{"client_id": {app.ClientID}, "device_code": {g.code},
		"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}, &out); err != nil {
		return "", err
	}
	switch out.Error {
	case "":
	case "authorization_pending":
		g.next = now.Add(g.interval)
		return PollPending, nil
	case "slow_down":
		g.interval = max(time.Duration(out.Interval)*time.Second, g.interval+5*time.Second)
		g.next = now.Add(g.interval)
		return PollPending, nil
	case "expired_token":
		p.drop(userID)
		return "", ErrDeviceExpired
	case "access_denied":
		p.drop(userID)
		p.audit(ctx, "credential_denied", userID, "")
		return "", ErrDeviceDenied
	default:
		p.drop(userID)
		return "", fmt.Errorf("github: the device flow: %s", safeCode(out.Error))
	}
	p.drop(userID) // the device code is spent
	if out.AccessToken == "" {
		return "", errors.New("github: the device flow gave no token")
	}
	if err := p.keep(ctx, userID, app, out); err != nil {
		return "", err
	}
	return PollConnected, nil
}

func (p *Personal) drop(userID string) {
	p.mu.Lock()
	delete(p.pending, userID)
	p.mu.Unlock()
}

// tokenAnswer is the answer of POST /login/oauth/access_token.
type tokenAnswer struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	Interval     int    `json:"interval"`
}

// keep checks the account of a new token and keeps it. A token of another
// account, or one whose account can not be read, is revoked and thrown away.
func (p *Personal) keep(ctx context.Context, userID string, app App, t tokenAnswer) error {
	u, err := p.o.Users.Get(ctx, userID)
	if err != nil {
		p.revoke(ctx, app, t.AccessToken, false)
		return err
	}
	if u.State != users.Active || u.Level == users.Observer {
		p.revoke(ctx, app, t.AccessToken, false)
		return ErrNoCredential
	}
	rest := &REST{BaseURL: p.o.API, HTTP: p.o.HTTP, Token: func(context.Context, string) (string, error) { return t.AccessToken, nil }}
	login, err := rest.CurrentLogin(ctx)
	if err != nil {
		p.revoke(ctx, app, t.AccessToken, false)
		return fmt.Errorf("github: the account of the token could not be read: %w", err)
	}
	if want := strings.TrimSpace(u.GitHubLogin); want == "" || !strings.EqualFold(login, want) {
		p.revoke(ctx, app, t.AccessToken, false)
		p.audit(ctx, "credential_refused", userID, "the account that authorized is "+login)
		return ErrLoginMismatch
	}
	if p.loginShared(ctx, userID, login) {
		// two users with one login: the token could be taken as the other's by whoever types the code
		p.revoke(ctx, app, t.AccessToken, false)
		p.audit(ctx, "credential_refused", userID, "the login "+login+" is registered for another user too")
		return ErrLoginMismatch
	}
	if err := p.store(ctx, userID, login, t, true); err != nil {
		p.revoke(ctx, app, t.AccessToken, false)
		return err
	}
	p.audit(ctx, "credential_connected", userID, login)
	return nil
}

// loginShared says another active user has the same github_login.
func (p *Personal) loginShared(ctx context.Context, userID, login string) bool {
	all, err := p.o.Users.List(ctx)
	if err != nil {
		return true // not knowing is not "alone"
	}
	for _, o := range all {
		if o.ID != userID && o.State == users.Active && strings.EqualFold(strings.TrimSpace(o.GitHubLogin), login) {
			return true
		}
	}
	return false
}

// store puts the tokens in the vault and the state in the database.
func (p *Personal) store(ctx context.Context, userID, login string, t tokenAnswer, connected bool) error {
	val, err := json.Marshal(vault.GitHubUserTokenValue{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken})
	if err != nil {
		return err
	}
	name := UserTokenSecret(userID)
	if err := p.o.Vault.Put(ctx, name, vault.TypeGitHubUserToken, val, nil); err != nil {
		return fmt.Errorf("github: the tokens of the user did not go to the vault: %w", err)
	}
	now := p.o.Clock.Now()
	var expires int64
	if t.ExpiresIn > 0 {
		expires = now.Add(time.Duration(t.ExpiresIn) * time.Second).UnixMilli()
	}
	return p.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO github_user_credentials (user_id, login, state, secret, expires_at, connected_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (user_id) DO UPDATE SET login = excluded.login, state = excluded.state, secret = excluded.secret, expires_at = excluded.expires_at,
			connected_at = CASE WHEN ? THEN excluded.connected_at ELSE connected_at END, updated_at = excluded.updated_at`,
			userID, login, CredentialConnected, name, expires, now.UnixMilli(), now.UnixMilli(), connected)
		return err
	})
}

// credential is the row of a user.
type credential struct {
	login, state, secret string
	expires              time.Time
	connected            time.Time
}

func (p *Personal) row(ctx context.Context, userID string) (credential, bool, error) {
	var c credential
	var exp, conn int64
	err := p.o.DB.Reader().QueryRowContext(ctx, `SELECT login, state, secret, expires_at, connected_at FROM github_user_credentials WHERE user_id = ?`, userID).
		Scan(&c.login, &c.state, &c.secret, &exp, &conn)
	if errors.Is(err, sql.ErrNoRows) {
		return credential{}, false, nil
	}
	if err != nil {
		return credential{}, false, err
	}
	if exp > 0 {
		c.expires = time.UnixMilli(exp).UTC()
	}
	c.connected = time.UnixMilli(conn).UTC()
	return c, true, nil
}

// clientSecret reads the client secret of the App from the vault and gives it to fn.
func (p *Personal) clientSecret(ctx context.Context, app App, purpose string, fn func(secret string) error) error {
	err := p.o.Vault.Use(ctx, ClientSecretName(app.Name), purpose, func(v []byte) error { return fn(string(v)) })
	if err != nil && errors.Is(err, vault.ErrNotFound) {
		return ErrNoClientSecret
	}
	return err
}

// revoke takes a token back from GitHub, best effort: the grant is deleted with
// the client id and secret of the App. A failure is logged without the token.
func (p *Personal) revoke(ctx context.Context, app App, token string, wholeGrant bool) {
	// A discarded token goes by /token: the grant of an account is all its tokens,
	// which may be those of a user who did nothing wrong (the one who typed the code).
	kind := "/token"
	if wholeGrant {
		kind = "/grant"
	}
	err := p.clientSecret(ctx, app, "revoke a user token of the App "+app.Name, func(secret string) error {
		body, _ := json.Marshal(map[string]string{"access_token": token})
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimRight(p.o.API, "/")+"/applications/"+url.PathEscape(app.ClientID)+kind, strings.NewReader(string(body)))
		if err != nil {
			return err
		}
		req.SetBasicAuth(app.ClientID, secret)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("Content-Type", "application/json")
		resp, err := p.o.HTTP.Do(req)
		if err != nil {
			return errors.New("the request failed")
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
			return fmt.Errorf("status %d", resp.StatusCode)
		}
		return nil
	})
	if err != nil {
		p.o.Logger.Warn("github: a user token was not revoked at GitHub", "err", err)
	}
}

// tokens reads the tokens of a user from the vault.
func (p *Personal) tokens(ctx context.Context, userID, purpose string) (vault.GitHubUserTokenValue, error) {
	var v vault.GitHubUserTokenValue
	err := p.o.Vault.Use(ctx, UserTokenSecret(userID), purpose, func(raw []byte) error { return json.Unmarshal(raw, &v) })
	if err != nil {
		return vault.GitHubUserTokenValue{}, err
	}
	return v, nil
}

// Client is the GitHub as the person: a client whose token is her user access
// token, renewed when it is about to end. A user who is not active, who never
// connected, whose renewal was refused or whose github_login changed since she
// connected has no credential in use (ErrNoCredential).
func (p *Personal) Client(ctx context.Context, userID string) (*REST, error) {
	u, err := p.o.Users.Get(ctx, userID)
	if err != nil {
		if errors.Is(err, users.ErrNoUser) {
			return nil, ErrNoCredential
		}
		return nil, err
	}
	if u.State != users.Active || u.Level == users.Observer {
		return nil, ErrNoCredential
	}
	c, ok, err := p.row(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !ok || c.state != CredentialConnected || !strings.EqualFold(c.login, strings.TrimSpace(u.GitHubLogin)) {
		return nil, ErrNoCredential
	}
	if !c.expires.IsZero() && !p.o.Clock.Now().Add(TokenRenewBefore).Before(c.expires) {
		if err := p.renew(ctx, userID); err != nil {
			return nil, err
		}
	}
	return &REST{BaseURL: p.o.API, HTTP: p.o.HTTP, App: RoleGrandMaster, Token: func(ctx context.Context, _ string) (string, error) {
		v, err := p.tokens(ctx, userID, "act on GitHub as user "+userID)
		return v.AccessToken, err
	}}, nil
}

// renew exchanges the refresh token for new tokens. A refusal of GitHub makes
// the credential expired; a failure to reach it does not.
func (p *Personal) renew(ctx context.Context, userID string) error {
	lk := p.lockOf(userID)
	lk.Lock()
	defer lk.Unlock()
	c, ok, err := p.row(ctx, userID)
	if err != nil {
		return err
	}
	if !ok || c.state != CredentialConnected {
		return ErrNoCredential
	}
	if !c.expires.IsZero() && p.o.Clock.Now().Add(TokenRenewBefore).Before(c.expires) {
		return nil // another call renewed it meanwhile
	}
	app, err := p.app(ctx)
	if err != nil {
		return err
	}
	cur, err := p.tokens(ctx, userID, "renew the tokens of user "+userID)
	if err != nil {
		return err
	}
	if cur.RefreshToken == "" {
		return p.expire(ctx, userID, "no refresh token")
	}
	var out tokenAnswer
	err = p.clientSecret(ctx, app, "renew a user token of the App "+app.Name, func(secret string) error {
		return p.form(ctx, "/login/oauth/access_token", url.Values{"client_id": {app.ClientID}, "client_secret": {secret},
			"grant_type": {"refresh_token"}, "refresh_token": {cur.RefreshToken}}, &out)
	})
	if err != nil {
		return err
	}
	switch out.Error {
	case "":
		if out.AccessToken == "" {
			return errors.New("github: the renewal gave no token")
		}
	case "bad_refresh_token", "expired_token":
		return p.expire(ctx, userID, "the renewal was refused: "+out.Error)
	default:
		// the App or its secret is wrong, not the credential of the user
		return fmt.Errorf("github: the renewal of a user token failed: %s", safeCode(out.Error))
	}
	err = p.store(ctx, userID, c.login, out, false)
	if err != nil {
		// GitHub spent the refresh token: the new ones must not be lost
		err = p.store(ctx, userID, c.login, out, false)
	}
	if err != nil {
		p.revoke(ctx, app, out.AccessToken, false)
		_ = p.expire(ctx, userID, "the renewed tokens were not kept")
		return err
	}
	p.audit(ctx, "credential_renewed", userID, "")
	return nil
}

// expire marks the credential expired: the panel shows it and the user connects again.
func (p *Personal) expire(ctx context.Context, userID, why string) error {
	err := p.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE github_user_credentials SET state = ?, updated_at = ? WHERE user_id = ?`, CredentialExpired, p.o.Clock.Now().UnixMilli(), userID)
		return err
	})
	if err != nil {
		return err
	}
	p.audit(ctx, "credential_expired", userID, why)
	return ErrNoCredential
}

// Remove ends the credential of a user: the grant is revoked at GitHub (best
// effort), and the tokens and the row are erased.
func (p *Personal) Remove(ctx context.Context, userID string) error {
	lk := p.lockOf(userID)
	lk.Lock()
	defer lk.Unlock()
	p.drop(userID)
	_, ok, err := p.row(ctx, userID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if app, err := p.app(ctx); err == nil {
		if v, err := p.tokens(ctx, userID, "revoke the tokens of user "+userID); err == nil {
			p.revoke(ctx, app, v.AccessToken, true)
		}
	}
	if err := p.o.Vault.Delete(ctx, UserTokenSecret(userID)); err != nil && !errors.Is(err, vault.ErrNotFound) {
		return err
	}
	err = p.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM github_user_credentials WHERE user_id = ?`, userID)
		return err
	})
	if err != nil {
		return err
	}
	p.audit(ctx, "credential_removed", userID, "")
	return nil
}

// Status is what the panel shows of a credential.
type Status struct {
	// State is "", CredentialConnected or CredentialExpired.
	State     string
	Login     string
	ExpiresAt time.Time
}

// Status reports the credential of a user, never a token.
func (p *Personal) Status(ctx context.Context, userID string) (Status, error) {
	c, ok, err := p.row(ctx, userID)
	if err != nil || !ok {
		return Status{}, err
	}
	return Status{State: c.state, Login: c.login, ExpiresAt: c.expires}, nil
}

// CurrentLogin is the login of the account the token acts as (GET /user).
func (c *REST) CurrentLogin(ctx context.Context) (string, error) {
	st, hdr, raw, err := c.doAbs(ctx, http.MethodGet, "", "/user", nil)
	if err != nil {
		return "", err
	}
	if st != http.StatusOK {
		return "", failed("GET /user", st, raw, hdr)
	}
	var v struct {
		Login string `json:"login"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Login == "" {
		return "", errors.New("github: the answer of GET /user is not readable")
	}
	return v.Login, nil
}
