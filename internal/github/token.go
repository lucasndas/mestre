package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The numbers of the tokens (docs/github/identidades-e-apps.md#tokens).
const (
	// TokenRenewBefore is how long before the end a token in memory is renewed.
	TokenRenewBefore = 5 * time.Minute
	// jwtTTL is the life of the JWT that asks for a token (GitHub accepts up to 10
	// minutes); the clock of GitHub may be a little ahead, so it starts a minute back.
	jwtTTL  = 9 * time.Minute
	jwtSkew = time.Minute
)

// KeySecret is the name of the private key of an App in the vault.
func KeySecret(app string) string { return "github_app_key:" + app }

// Keys gives the private key of an App to a function and forgets it
// (*vault.Vault). Only the engine reads it.
type Keys interface {
	Use(ctx context.Context, name, purpose string, fn func(value []byte) error) error
}

var _ Keys = (*vault.Vault)(nil)

// Token is an installation token: a secret that never goes to disk, log, error or
// the environment of an agent.
type Token struct {
	value     string
	ExpiresAt time.Time
}

// Secret is the token, for the Authorization header of the request; nothing else
// should read it.
func (t Token) Secret() string { return t.value }

// String and GoString do not print the token (nor does a log: LogValue).
func (t Token) String() string   { return "github.Token(redacted)" }
func (t Token) GoString() string { return "github.Token(redacted)" }

// LogValue keeps the token out of the structured logs.
func (t Token) LogValue() slog.Value { return slog.StringValue("github.Token(redacted)") }

// Request is a request for a token: the App that acts, the one repository
// (`owner/name`) and the permissions the action uses.
type Request struct {
	App         string
	Repo        string
	Permissions map[string]string
}

// Tokens makes and keeps the installation tokens.
type Tokens struct {
	Apps    *Registry
	Keys    Keys
	Clock   clock.Clock
	BaseURL string
	HTTP    *http.Client

	mu    sync.Mutex
	cache map[string]Token
}

// ErrTokenRefused is a token GitHub did not give (key revoked, App deleted): the
// App goes to `com_erro` and the Grand Master tells the Owner.
var ErrTokenRefused = errors.New("github: GitHub refused the token of the App")

func cacheKey(r Request) string {
	keys := make([]string, 0, len(r.Permissions))
	for k, v := range r.Permissions {
		keys = append(keys, k+"="+v)
	}
	sort.Strings(keys)
	return r.App + "|" + r.Repo + "|" + strings.Join(keys, ",")
}

// Installation gives a token for the request. It asks GitHub for a token of the
// installation, restricted to the repository of the action and to the permissions
// it uses; a token still good (more than 5 minutes ahead) is reused. The key of
// the App is read from the vault only to sign the JWT.
func (t *Tokens) Installation(ctx context.Context, r Request) (Token, error) {
	key := cacheKey(r)
	now := t.Clock.Now()
	t.mu.Lock()
	if tok, ok := t.cache[key]; ok && now.Add(TokenRenewBefore).Before(tok.ExpiresAt) {
		t.mu.Unlock()
		return tok, nil
	}
	t.mu.Unlock()

	app, err := t.Apps.Get(ctx, r.App)
	if err != nil {
		return Token{}, err
	}
	if app.AppID == 0 {
		return Token{}, fmt.Errorf("github: the App %q was not created yet", app.Name)
	}
	var inst int64
	if r.Repo == "" { // a token for what is not in a repository: the repository of the lock is made so
		inst, err = t.Apps.AnyInstallation(ctx, app.Name)
	} else {
		inst, err = t.Apps.Installation(ctx, app.Name, r.Repo)
	}
	if err != nil {
		return Token{}, err
	}
	var jwt string
	err = t.Keys.Use(ctx, KeySecret(app.Name), "sign the JWT of the App "+app.Name, func(pemBytes []byte) error {
		var serr error
		jwt, serr = signJWT(pemBytes, app.AppID, now)
		return serr
	})
	if err != nil {
		return Token{}, fmt.Errorf("github: the JWT of the App %q: %w", app.Name, err)
	}
	doc := map[string]any{"permissions": r.Permissions}
	if r.Repo != "" { // restricted to the repository of the action, and only to it
		name := r.Repo
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}
		doc["repositories"] = []string{name}
	}
	body, _ := json.Marshal(doc)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/app/installations/%d/access_tokens", strings.TrimRight(t.BaseURL, "/"), inst), bytes.NewReader(body))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	hc := t.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("github: asking for the token of %q: %w", app.Name, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
		_ = t.Apps.SetState(ctx, app.Name, classes.AppWithError)
		return Token{}, fmt.Errorf("%w: %s (%d)", ErrTokenRefused, app.Name, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusCreated {
		return Token{}, fmt.Errorf("github: the token of %q: status %d", app.Name, resp.StatusCode)
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Token == "" {
		return Token{}, fmt.Errorf("github: the answer of the token of %q is not readable", app.Name)
	}
	tok := Token{value: out.Token, ExpiresAt: out.ExpiresAt}
	t.mu.Lock()
	if t.cache == nil {
		t.cache = map[string]Token{}
	}
	t.cache[key] = tok
	t.mu.Unlock()
	return tok, nil
}

// signJWT signs the JWT of an App (RS256, iss = the id of the App).
func signJWT(pemBytes []byte, appID int64, now time.Time) (string, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return "", errors.New("the key is not PEM")
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = k
	} else if k8, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		var ok bool
		if key, ok = k8.(*rsa.PrivateKey); !ok {
			return "", errors.New("the key is not RSA")
		}
	} else {
		return "", errors.New("the key is not readable")
	}
	enc := base64.RawURLEncoding
	head := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iat": now.Add(-jwtSkew).Unix(), "exp": now.Add(jwtTTL).Unix(), "iss": appID})
	signing := head + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

// RolePermissions are the permissions of an App by role, the table of
// docs/github/identidades-e-apps.md#permissões-mínimas (repository permissions;
// "Workflows" only for a class that may change the CI, and "Repository creation"
// is the one of the account, kept out of the token of a repository).
func RolePermissions(role string, workflows bool) map[string]string {
	p := map[string]string{
		"metadata": "read", "contents": "write", "issues": "write", "pull_requests": "write",
		"checks": "read", "statuses": "read",
	}
	switch role {
	case RoleGrandMaster:
		p["administration"] = "write"
	case RoleClass:
		if workflows {
			p["workflows"] = "write"
		}
	}
	return p
}
