package fakegithub

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/testkit"
)

// appInfo is an App registered in the double: its public key (the double checks
// the JWT the engine signs with the private one), its installation and what the
// token of the installation may do.
type appInfo struct {
	slug           string
	appID          int64
	installationID int64
	key            *rsa.PublicKey
	repos          []string // the repositories of the installation, `owner/name`
	repoCreation   bool     // the Repository creation permission of the Grão-Mestre
	account        string   // the account of the installation, when it has one
	clientID       string
	deviceFlow     bool
}

// TokenRequest is what the engine asked for when it asked for a token of an
// installation: the App, the repositories and the permissions.
type TokenRequest struct {
	App          string
	Repositories []string
	Permissions  map[string]string
}

// AddApp registers an App with its installation in the repositories (`owner/name`).
// The double checks the JWT of each request for a token against key.
func (s *Server) AddApp(slug string, appID, installationID int64, key *rsa.PublicKey, repoCreation bool, repos ...string) {
	a := &appInfo{slug: slug, appID: appID, installationID: installationID, key: key, repos: repos, repoCreation: repoCreation}
	s.mu.Lock()
	s.apps[appID], s.installs[installationID] = a, a
	s.mu.Unlock()
}

// AddBot registers the user of the bot of an App (`<slug>[bot]`) with its id.
func (s *Server) AddBot(slug string, id int64) {
	s.mu.Lock()
	s.bots[slug+"[bot]"] = id
	s.mu.Unlock()
}

// AddInstalledRepo adds a repository (`owner/name`) to the installation of the
// App, public or private.
func (s *Server) AddInstalledRepo(appSlug, full string, private bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.apps {
		if a.slug == appSlug {
			a.repos = append(a.repos, full)
		}
	}
	if !private {
		s.public[full] = true
	}
}

// FailTokens makes the next request for a token answer with the status and the
// headers (a 403 with Retry-After is the limit of requests of GitHub).
func (s *Server) FailTokens(status int, headers map[string]string) {
	s.mu.Lock()
	s.tokenFail, s.tokenFailHdr = status, headers
	s.mu.Unlock()
}

// FailUserRepos makes the next POST /user/repos answer with the status (403 for
// the App that may not create repositories).
func (s *Server) FailUserRepos(status int) {
	s.mu.Lock()
	s.userRepoSt = status
	s.mu.Unlock()
}

// TokenRequests returns the requests for installation tokens made so far.
func (s *Server) TokenRequests() []TokenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]TokenRequest(nil), s.tokenReqs...)
}

// serveApps serves the routes of the Apps: it answers true when the request was
// one of them.
func (s *Server) serveApps(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodPost && len(parts) == 4 && parts[0] == "app" && parts[1] == "installations" && parts[3] == "access_tokens":
		s.accessToken(w, r, parts[2])
	case s.serveManifest(w, r, parts):
	case r.Method == http.MethodGet && len(parts) == 2 && parts[0] == "users":
		s.botUser(w, r, parts[1])
	case r.Method == http.MethodPost && r.URL.Path == "/user/repos":
		s.userRepos(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/installation/repositories":
		s.installationRepos(w, r)
	default:
		return false
	}
	return true
}

func (s *Server) accessToken(w http.ResponseWriter, r *http.Request, id string) {
	n, err := strconv.ParseInt(id, 10, 64)
	s.mu.Lock()
	app := s.installs[n]
	s.mu.Unlock()
	if err != nil || app == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	s.mu.Lock()
	failSt, failHdr := s.tokenFail, s.tokenFailHdr
	s.tokenFail, s.tokenFailHdr = 0, nil
	s.mu.Unlock()
	if failSt != 0 {
		for k, v := range failHdr {
			w.Header().Set(k, v)
		}
		writeJSON(w, failSt, map[string]any{"message": "injected failure"})
		return
	}
	if !s.verifyJWT(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), app) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "A JSON web token could not be decoded"})
		return
	}
	var in struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		testkit.NotImplemented(s.rep, w, "POST /app/installations/"+id+"/access_tokens body: "+err.Error())
		return
	}
	now := s.clk.Now()
	s.mu.Lock()
	s.nextID++
	token := fmt.Sprintf("ghs_fake%08d", s.nextID)
	var full []string
	for _, name := range in.Repositories {
		for _, rp := range app.repos {
			if strings.HasSuffix(rp, "/"+name) {
				full = append(full, rp)
			}
		}
	}
	s.tokens[token] = &installation{app: app.slug, expiresAt: now.Add(TokenTTL), resetAt: now.Add(time.Hour), id: app.installationID, repos: full}
	s.tokenReqs = append(s.tokenReqs, TokenRequest{App: app.slug, Repositories: in.Repositories, Permissions: in.Permissions})
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{
		"token": token, "expires_at": now.Add(TokenTTL).UTC().Format(time.RFC3339),
		"permissions": in.Permissions, "repository_selection": "selected",
	})
}

// verifyJWT checks an RS256 token against the key of the App: the signature, the
// issuer and the time.
func (s *Server) verifyJWT(tok string, app *appInfo) bool {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return false
	}
	enc := base64.RawURLEncoding
	sig, err := enc.DecodeString(parts[2])
	if err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(app.key, crypto.SHA256, sum[:], sig) != nil {
		return false
	}
	raw, err := enc.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var c struct {
		Iss int64 `json:"iss"`
		Exp int64 `json:"exp"`
		Iat int64 `json:"iat"`
	}
	if json.Unmarshal(raw, &c) != nil {
		return false
	}
	now := s.clk.Now().Unix()
	return c.Iss == app.appID && c.Exp > now && c.Exp-c.Iat <= 600+60 && c.Iat <= now
}

func (s *Server) botUser(w http.ResponseWriter, r *http.Request, login string) {
	if i := strings.Index(login, "%5B"); i >= 0 {
		login = login[:i] + "[" + strings.TrimSuffix(login[i+3:], "%5D") + "]"
	}
	s.mu.Lock()
	id, ok := s.bots[login]
	s.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"login": login, "id": id, "type": "Bot"})
}

func (s *Server) tokenOf(w http.ResponseWriter, r *http.Request) (*installation, *appInfo, bool) {
	inst, ok := s.auth(w, r)
	if !ok {
		return nil, nil, false
	}
	s.mu.Lock()
	app := s.installs[inst.id]
	s.mu.Unlock()
	if app == nil {
		testkit.NotImplemented(s.rep, w, r.Method+" "+r.URL.Path+" with a token not made by the double of the Apps")
		return nil, nil, false
	}
	return inst, app, true
}

func (s *Server) userRepos(w http.ResponseWriter, r *http.Request) {
	inst, app, ok := s.tokenOf(w, r)
	if !ok {
		return
	}
	var in struct {
		Name     string `json:"name"`
		Private  bool   `json:"private"`
		AutoInit bool   `json:"auto_init"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || in.Name == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
		return
	}
	s.mu.Lock()
	forced := s.userRepoSt
	s.userRepoSt = 0
	owner := "owner"
	if len(app.repos) > 0 {
		owner = strings.SplitN(app.repos[0], "/", 2)[0]
	}
	full := owner + "/" + in.Name
	_, exists := s.repos[full]
	s.mu.Unlock()
	switch {
	case forced != 0:
		writeJSON(w, forced, map[string]any{"message": "Resource not accessible by integration"})
	case !app.repoCreation:
		writeJSON(w, http.StatusForbidden, map[string]any{"message": "Resource not accessible by integration"})
	case exists:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"message": "Repository creation failed.", "errors": []map[string]any{{"message": "name already exists on this account"}}})
	default:
		s.mu.Lock()
		s.repos[full] = &repo{}
		app.repos = append(app.repos, full) // the App gets access to the repository it makes
		s.actions = append(s.actions, Action{App: inst.app, Method: r.Method, Path: r.URL.Path})
		s.mu.Unlock()
		writeJSON(w, http.StatusCreated, map[string]any{"full_name": full, "name": in.Name, "private": in.Private})
	}
}

func (s *Server) installationRepos(w http.ResponseWriter, r *http.Request) {
	inst, app, ok := s.tokenOf(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	all := make([]map[string]any, 0, len(app.repos))
	for _, rp := range app.repos {
		if inst.repos == nil || contains(inst.repos, rp) {
			all = append(all, map[string]any{"full_name": rp, "private": !s.public[rp]})
		}
	}
	s.mu.Unlock()
	// the list is paged, 30 by default and 100 at most, as the real one
	per, page := 30, 1
	if v, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil && v > 0 {
		per = min(v, 100)
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && v > 0 {
		page = v
	}
	lo := min((page-1)*per, len(all))
	hi := min(lo+per, len(all))
	writeJSON(w, http.StatusOK, map[string]any{"total_count": len(all), "repositories": all[lo:hi]})
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}
