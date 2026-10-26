package attach

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// maxHops is how many redirects of the panel (a Tower, a suspended Citadel) the
// client follows to reach the Citadel of now.
const maxHops = 5

// maxBody bounds what is read of an answer of the panel.
const maxBody = 4 << 20

// loadCA reads the CA of the Guild that the engine keeps in the configuration
// folder of the instance. Only this CA is trusted: the system store is not asked.
func loadCA(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fail(codeUnavailable, "the engine of this machine writes it when it runs; check that `mestre run` has run here and that you may read the folder of the instance (or use --dir)",
			"cannot read the CA of the Guild (%s): %v", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fail(codeUnavailable, "the file is not a PEM certificate; the engine writes it again when it starts", "the CA of the Guild in %s is not valid", path)
	}
	return pool, nil
}

// panelClient talks to the API of the panel as a browser would: the session is a
// cookie that lives in this struct and nowhere else, and each request carries the
// Origin of the panel.
type panelClient struct {
	hc         *http.Client // calls with a timeout
	ws         *http.Client // the same TLS, no timeout (the socket lives long)
	base       *url.URL     // https://host:port of the Citadel of now
	token      string
	userAgent  string
	cookieName string
}

func newPanelClient(pool *x509.CertPool, userAgent string, dial func(ctx context.Context, network, addr string) (net.Conn, error)) *panelClient {
	mk := func(timeout time.Duration) *http.Client {
		return &http.Client{
			Timeout:       timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
				DialContext:     dial,
				Proxy:           nil, // the panel is on the local network: never through a proxy
			},
		}
	}
	return &panelClient{hc: mk(20 * time.Second), ws: mk(0), userAgent: userAgent, cookieName: panel.CookieName}
}

// normalizeServer reads --server: "host", "host:port" or "https://host[:port]".
func normalizeServer(s string) (*url.URL, error) {
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return nil, fail(codeUsage, "usage: "+usageLine, "--server %q is not an address of the panel", s)
	}
	if u.Scheme != "https" {
		return nil, fail(codeUsage, "the panel is served over https", "--server %q must be an https address", s)
	}
	return &url.URL{Scheme: "https", Host: u.Host}, nil
}

// unreachable is the failure for a panel that did not answer.
func unreachable(err error) failure {
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var host x509.HostnameError
	switch {
	case errors.As(err, &unknown), errors.As(err, &invalid), errors.As(err, &host):
		return fail(codeUnavailable, "the certificate of the panel is not from the CA of this Guild; check the address, or --server", "cannot verify the certificate of the panel: %v", err)
	}
	return fail(codeUnavailable, "check that the Citadel is running and that the address is right; run mestre debug on the machine of the Citadel", "the panel does not answer: %v", err)
}

// resolve finds the Citadel of now: it asks the status of the panel and follows
// the 307 of a Tower or of a suspended Citadel that knows where the Citadel is.
func (c *panelClient) resolve(ctx context.Context, start *url.URL) error {
	base := start
	for hop := 0; hop <= maxHops; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String()+"/api/status", nil)
		if err != nil {
			return fail(codeInternal, "this is a bug", "%v", err)
		}
		req.Header.Set("User-Agent", c.userAgent)
		res, err := c.hc.Do(req)
		if err != nil {
			return unreachable(err)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxBody))
		_ = res.Body.Close()
		switch {
		case res.StatusCode == http.StatusOK:
			c.base = base
			return nil
		case res.StatusCode >= 300 && res.StatusCode < 400:
			next, err := url.Parse(res.Header.Get("Location"))
			if err != nil || next.Hostname() == "" {
				return fail(codeUnavailable, "try again in a moment", "the panel redirected to an address that makes no sense")
			}
			next = base.ResolveReference(next)
			if next.Scheme != "https" {
				return fail(codeUnavailable, "the panel is served over https", "the panel redirected to a plain address")
			}
			base = &url.URL{Scheme: "https", Host: next.Host}
		default:
			return fail(codeUnavailable, "the Citadel is suspended or being changed; try again in a moment", "the panel answered %d and is not serving now", res.StatusCode)
		}
	}
	return fail(codeUnavailable, "the Guild is changing its Citadel; try again in a moment", "too many redirects looking for the Citadel")
}

// apiError is the error body of the panel.
type apiError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Params  map[string]any `json:"params"`
}

// call does a request of the API; a non-2xx answer comes back as *apiError.
func (c *panelClient) call(ctx context.Context, method, path string, in, out any) (status int, header http.Header, aerr *apiError, err error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, nil, nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, body)
	if err != nil {
		return 0, nil, nil, err
	}
	c.sign(req.Header)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, nil, unreachable(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if res.StatusCode/100 != 2 {
		var eb struct {
			Error apiError `json:"error"`
		}
		_ = json.Unmarshal(data, &eb)
		if res.StatusCode/100 == 3 {
			return res.StatusCode, res.Header, &apiError{Code: "redirected"}, nil
		}
		return res.StatusCode, res.Header, &eb.Error, nil
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return res.StatusCode, res.Header, nil, fmt.Errorf("the panel answered something that is not JSON: %w", err)
		}
	}
	return res.StatusCode, res.Header, nil, nil
}

// sign puts on a request what a browser would: the Origin of the panel, which
// the server checks against its own address, and the cookie of the session.
func (c *panelClient) sign(h http.Header) {
	h.Set("Origin", "https://"+c.base.Host)
	h.Set("User-Agent", c.userAgent)
	if c.token != "" {
		h.Set("Cookie", c.cookieName+"="+c.token)
	}
}

// login opens the session. Whatever the reason of a refusal, the answer is the
// same words (the panel does the same).
func (c *panelClient) login(ctx context.Context, login, password string) (panel.Me, error) {
	var me panel.Me
	status, hdr, aerr, err := c.call(ctx, http.MethodPost, "/api/login", panel.LoginRequest{Login: login, Password: password}, &me)
	switch {
	case err != nil:
		return me, err
	case aerr != nil && (status == http.StatusUnauthorized || status == http.StatusTooManyRequests):
		return me, fail(codeAuthFailed, "check the user and the password; after too many mistakes the login is blocked for a while", "invalid user or password, or the login is blocked")
	case aerr != nil:
		return me, fail(codeUnavailable, "try again in a moment", "the panel refused the login (%d)", status)
	}
	for _, ck := range (&http.Response{Header: hdr}).Cookies() {
		if ck.Name == c.cookieName && ck.Value != "" {
			c.token = ck.Value
			return me, nil
		}
	}
	return me, fail(codeUnavailable, "this is a bug of the panel", "the panel opened no session")
}

// logout ends the session on the Citadel; the session lives only in this process.
func (c *panelClient) logout() {
	if c.token == "" {
		return
	}
	ctx, cancel := clock.WithTimeout(context.Background(), clock.Real{}, 3*time.Second)
	defer cancel()
	_, _, _, _ = c.call(ctx, http.MethodPost, "/api/logout", nil, nil)
	c.token = ""
}

// find lists the visible terminals of the characters with the name.
func (c *panelClient) find(ctx context.Context, name, workspace string) ([]panel.TerminalInfo, error) {
	q := url.Values{"name": {name}}
	if workspace != "" {
		q.Set("workspace", workspace)
	}
	var list panel.TerminalList
	status, _, aerr, err := c.call(ctx, http.MethodGet, "/api/terminals?"+q.Encode(), nil, &list)
	switch {
	case err != nil:
		return nil, err
	case aerr != nil && status == http.StatusUnauthorized:
		return nil, errSessionEnded
	case aerr != nil:
		return nil, fail(codeUnavailable, "try again in a moment", "the panel cannot list the terminals (%d)", status)
	}
	return list.Terminals, nil
}

// info reads the terminal of a character again; found is false when it is gone.
func (c *panelClient) info(ctx context.Context, character string) (info panel.TerminalInfo, found bool, err error) {
	status, _, aerr, err := c.call(ctx, http.MethodGet, "/api/characters/"+url.PathEscape(character)+"/terminal", nil, &info)
	switch {
	case err != nil:
		return info, false, err
	case aerr != nil && status == http.StatusNotFound:
		return info, false, nil
	case aerr != nil && status == http.StatusUnauthorized:
		return info, false, errSessionEnded
	case aerr != nil:
		return info, false, fail(codeUnavailable, "try again in a moment", "the panel cannot read the terminal (%d)", status)
	}
	return info, true, nil
}

var errSessionEnded = fail(codeSessionEnded, "log in again with mestre attach; the session ends when the user is suspended, changes the password or loses the workspace", "the session was ended by the Citadel")
