package gitx

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/mestre-ia/mestre/internal/logging"
)

// ErrCredentialsInURL is returned for a URL with user information
// ("https://user:token@host/..."): a credential in a URL lands in the
// arguments of git, which every local account can read
// (/proc/<pid>/cmdline), and Clone would keep it in remote.origin.url. Pass
// it as an Auth instead.
var ErrCredentialsInURL = errors.New("gitx: credentials in a URL; pass them as an Auth")

// CheckURL refuses a URL that carries user information. Local paths and
// URLs without it pass.
func CheckURL(u string) error {
	if credsRe.MatchString(u) {
		return fmt.Errorf("%w: %s", ErrCredentialsInURL, RedactURL(u))
	}
	return nil
}

// CheckConfig refuses credentials in the value of a URL setting
// (remote.<name>.url, remote.<name>.pushurl).
func CheckConfig(key, value string) error {
	if k := strings.ToLower(key); strings.HasSuffix(k, ".url") || strings.HasSuffix(k, ".pushurl") {
		return CheckURL(value)
	}
	return nil
}

// Auth authenticates the HTTP(S) requests of one git call with an extra
// header, handed to git through its environment (GIT_CONFIG_COUNT,
// GIT_CONFIG_KEY_0 = http.<URL>.extraHeader, GIT_CONFIG_VALUE_0 = Header):
// never in a URL or in the arguments. The environment of a process is
// readable only by its own account; its arguments, by every local account,
// mestre-agente included. Printing an Auth shows the URL only.
type Auth struct {
	// URL is the prefix the header goes to, e.g. "https://github.com/":
	// http or https, no user information, query or fragment.
	URL string
	// Header is the whole header line, e.g. "Authorization: Basic ...".
	Header string
}

// BasicAuth is the Auth for a token over HTTPS: "Authorization: Basic"
// with user and token (for a GitHub App installation token, the user is
// x-access-token).
func BasicAuth(prefix, user, token string) Auth {
	cred := base64.StdEncoding.EncodeToString([]byte(user + ":" + token))
	return Auth{URL: prefix, Header: "Authorization: Basic " + cred}
}

// Validate checks the URL prefix and the header line.
func (a Auth) Validate() error {
	u, err := url.Parse(a.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(a.URL, "\r\n\x00") {
		return fmt.Errorf("%w: auth URL %q must be an http(s) prefix without credentials, query or fragment", ErrInvalidArgument, RedactURL(a.URL))
	}
	name, value, ok := strings.Cut(a.Header, ":")
	if !ok || strings.TrimSpace(name) == "" || strings.ContainsAny(name, " \t") ||
		strings.TrimSpace(value) == "" || strings.ContainsAny(a.Header, "\r\n\x00") {
		return fmt.Errorf("%w: auth header must be one \"Name: value\" line", ErrInvalidArgument)
	}
	return nil
}

// env is the configuration of the header, in the environment of git.
func (a Auth) env() []string {
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http." + a.URL + ".extraHeader",
		"GIT_CONFIG_VALUE_0=" + a.Header,
	}
}

// secrets are what the masker must hide besides the Authorization pattern it
// already knows: the header value and, for Basic, the token inside it.
func (a Auth) secrets() []string {
	_, value, _ := strings.Cut(a.Header, ":")
	value = strings.TrimSpace(value)
	out := []string{value}
	if scheme, cred, ok := strings.Cut(value, " "); ok && strings.EqualFold(scheme, "basic") {
		if raw, err := base64.StdEncoding.DecodeString(cred); err == nil {
			if _, token, ok := strings.Cut(string(raw), ":"); ok {
				out = append(out, token)
			}
		}
	}
	return out
}

func (a Auth) String() string             { return "auth for " + a.URL + " " + logging.Redacted }
func (a Auth) GoString() string           { return a.String() }
func (a Auth) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(a.String())) }
func (a Auth) LogValue() slog.Value       { return slog.StringValue(a.String()) }
