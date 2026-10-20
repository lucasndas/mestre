// Package fakegithub is the in-memory GitHub REST API of every suite
// (docs/qualidade/dubles.md#github-falso). M0 serves issues (create, get,
// list) with what matters for the poller: ETag and If-None-Match answering
// 304 without spending quota, rate-limit headers per installation,
// installation tokens that expire after one hour by the clock, and the App
// that did each action. The rest of the API (sub-issues, PRs, reviews,
// rulesets, manifests, device flow, releases, the App pages) is served as
// the tests need it; what is not implemented fails the test.
package fakegithub

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/pkg/clock"
)

const (
	origin = "github"
	// RateLimit is the hourly quota of an installation.
	RateLimit = 5000
	// TokenTTL is the life of an installation token.
	TokenTTL = time.Hour
)

// Action is one write done through the API, with the App that did it.
type Action struct {
	App    string
	Method string
	Path   string
}

type installation struct {
	app       string
	expiresAt time.Time
	used      int
	resetAt   time.Time
	// id is the installation the token belongs to (0: a token made with IssueToken).
	id int64
	// repos are the repositories the token reaches (nil: any of the installation).
	repos []string
	// user is the login of a user access token (the device flow): it acts as a person.
	user string
}

type repo struct {
	extra    map[int]*issueExtra
	pulls    map[int][2]string
	pullx    map[int]*pullExtra
	checks   map[string][]map[string]any // check runs by commit sha
	statuses map[string][]map[string]any // legacy statuses by commit sha
	labels   map[string]string
	issues   []map[string]any
	git      *gitData
	merges   []MergeCall
	// merge behaviors the test asks for
	mergeFail   *mergeFailure
	loseMerge   bool
	noMergeKind map[string]bool
}

// Server is the fake API. Use it as an http.Handler.
type Server struct {
	clk  clock.Clock
	rep  testkit.Reporter
	recs fs.FS
	ver  string

	mu      sync.Mutex
	tokens  map[string]*installation
	repos   map[string]*repo
	actions []Action
	nextID  int64

	apps           map[int64]*appInfo // by id of the App
	installs       map[int64]*appInfo // by id of the installation
	bots           map[string]int64
	manifests      map[string]Conversion
	conversions    int
	lookups        int
	convFail       int
	convFailHdr    map[string]string
	tokenReqs      []TokenRequest
	public         map[string]bool     // repositories that are not private
	made           map[string]MadeRepo // what POST /user/repos was asked, by repository
	tokenFail      int
	tokenFailHdr   map[string]string
	userRepoSt     int // status the next POST /user/repos answers (0: as the permissions say)
	defaultBranch  map[string]string
	pageSize       int
	loseRefAnswer  bool
	gitDown        int // status every call to the git API answers (0: as usual)
	failWrite      int // status the next write answers (0: as usual), with Retry-After failWriteAfter
	failWriteAfter string
	subIssueFail   int // status the next POST of a sub-issue answers (0: as usual)
	requests       []Request
	reads          []Request
	readFail       int
	readFailHdr    map[string]string
	ap             *approvalState
}

// New returns an empty server using the newest recordings.
func New(clk clock.Clock, rep testkit.Reporter) (*Server, error) {
	recs := testkit.Recordings()
	ver, err := testkit.LatestVersion(recs, origin)
	if err != nil {
		return nil, err
	}
	return &Server{clk: clk, rep: rep, recs: recs, ver: ver,
		tokens: map[string]*installation{}, repos: map[string]*repo{}, nextID: 1,
		public: map[string]bool{}, apps: map[int64]*appInfo{}, installs: map[int64]*appInfo{}, bots: map[string]int64{}}, nil
}

// AddRepo creates owner/name.
func (s *Server) AddRepo(owner, name string) {
	s.mu.Lock()
	s.repos[owner+"/"+name] = &repo{}
	s.mu.Unlock()
}

// IssueToken creates an installation token for the App slug, valid for
// TokenTTL from now.
func (s *Server) IssueToken(token, app string) {
	now := s.clk.Now()
	s.mu.Lock()
	s.tokens[token] = &installation{app: app, expiresAt: now.Add(TokenTTL), resetAt: now.Add(time.Hour)}
	s.mu.Unlock()
}

// SetUsed sets how much of the hourly quota the installation of the token has used.
func (s *Server) SetUsed(token string, used int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token].used = used
}

// Actions returns the writes done so far.
func (s *Server) Actions() []Action {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Action(nil), s.actions...)
}

// ServeHTTP serves the REST API.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route := r.Method + " " + r.URL.Path
	if r.Method == http.MethodGet && s.failRead(w, r) {
		return
	}
	if r.URL.Path == "/graphql" && r.Method == http.MethodPost {
		s.serveGraphQL(w, r)
		return
	}
	if s.serveApproval(w, r) || s.serveApps(w, r) || s.serveGit(w, r) || s.serveIssues(w, r) {
		return
	}
	// The route comes before the credentials: a route the double does not
	// know fails the test instead of looking like a 401.
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	known := len(parts) >= 4 && len(parts) <= 5 && parts[0] == "repos" && parts[3] == "issues" &&
		(r.Method == http.MethodGet || (len(parts) == 4 && r.Method == http.MethodPost))
	if !known {
		testkit.NotImplemented(s.rep, w, route)
		return
	}
	// A query parameter that is not implemented fails the test; ignoring
	// one would answer a different question than the caller asked.
	// The list of issues takes state, per_page and page (the first page is
	// the whole list here).
	state := "all"
	ordered := false
	for k, v := range r.URL.Query() {
		switch {
		case len(parts) == 4 && r.Method == http.MethodGet && (k == "per_page" || k == "page"):
		case len(parts) == 4 && r.Method == http.MethodGet && (k == "sort" && len(v) == 1 && v[0] == "updated" || k == "direction" && len(v) == 1 && v[0] == "desc"):
			ordered = true
		case len(parts) == 4 && r.Method == http.MethodGet && k == "state" && len(v) == 1 && (v[0] == "open" || v[0] == "closed" || v[0] == "all"):
			state = v[0]
		default:
			testkit.NotImplemented(s.rep, w, route+"?"+firstParam(r))
			return
		}
	}
	inst, ok := s.auth(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	rp, ok := s.repos[parts[1]+"/"+parts[2]]
	s.mu.Unlock()
	if !ok {
		s.write(w, r, inst, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	switch {
	case len(parts) == 4 && r.Method == http.MethodGet:
		s.mu.Lock()
		list := make([]map[string]any, 0, len(rp.issues))
		for _, is := range rp.issues {
			is := maps.Clone(is) // a copy: it is written after mu is released
			s.viewPull(rp, is)
			if st, _ := is["state"].(string); state == "all" || st == state || st == "" && state == "open" {
				list = append(list, is)
			}
		}
		s.mu.Unlock()
		if ordered {
			sortByUpdated(list)
		}
		if r.URL.Query().Get("per_page") != "" || r.URL.Query().Get("page") != "" {
			list = paginate(list, r)
		}
		s.write(w, r, inst, http.StatusOK, list)
	case len(parts) == 4 && r.Method == http.MethodPost:
		s.createIssue(w, r, inst, rp, parts[1]+"/"+parts[2])
	case len(parts) == 5 && r.Method == http.MethodGet:
		n, err := strconv.Atoi(parts[4])
		s.mu.Lock()
		var found map[string]any
		if err == nil && n >= 1 && n <= len(rp.issues) {
			found = rp.issues[n-1]
		}
		s.mu.Unlock()
		if found == nil {
			s.write(w, r, inst, http.StatusNotFound, map[string]any{"message": "Not Found"})
			return
		}
		s.write(w, r, inst, http.StatusOK, found)
	default:
		testkit.NotImplemented(s.rep, w, route)
	}
}

// firstParam names the first query parameter in sorted order, for the failure
// message.
func firstParam(r *http.Request) string {
	q := r.URL.Query()
	names := make([]string, 0, len(q))
	for k := range q {
		names = append(names, k)
	}
	sort.Strings(names)
	return names[0]
}

func (s *Server) auth(w http.ResponseWriter, r *http.Request) (*installation, bool) {
	h := r.Header.Get("Authorization")
	tok := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(h, "Bearer "), "token "))
	s.mu.Lock()
	inst, ok := s.tokens[tok]
	s.mu.Unlock()
	if !ok || !s.clk.Now().Before(inst.expiresAt) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "Bad credentials"})
		return nil, false
	}
	return inst, true
}

func (s *Server) createIssue(w http.ResponseWriter, r *http.Request, inst *installation, rp *repo, full string) {
	var in struct {
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Labels []string `json:"labels"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(&in)
	if err != nil && strings.HasPrefix(err.Error(), "json: unknown field ") {
		testkit.NotImplemented(s.rep, w, "POST /repos/"+full+"/issues field "+strings.TrimPrefix(err.Error(), "json: unknown field "))
		return
	}
	if err != nil || in.Title == "" {
		s.write(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
		return
	}
	rec, err := testkit.LoadRecording(s.recs, origin, s.ver, "issue")
	var issue map[string]any
	if err == nil {
		err = json.Unmarshal(rec.Body, &issue)
	}
	if err != nil {
		s.rep.Errorf("fakegithub: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := s.clk.Now().UTC().Format(time.RFC3339)
	s.mu.Lock()
	n := len(rp.issues) + 1
	id := s.nextID
	s.nextID++
	labels := make([]map[string]any, 0, len(in.Labels))
	for _, l := range in.Labels {
		labels = append(labels, map[string]any{"name": l})
	}
	issue["id"], issue["number"], issue["title"], issue["body"] = id, n, in.Title, in.Body
	issue["labels"], issue["created_at"], issue["updated_at"] = labels, now, now
	issue["url"] = fmt.Sprintf("https://api.github.com/repos/%s/issues/%d", full, n)
	issue["html_url"] = fmt.Sprintf("https://github.com/%s/issues/%d", full, n)
	if u, ok := issue["user"].(map[string]any); ok {
		u["login"] = inst.app + "[bot]"
	}
	rp.issues = append(rp.issues, issue)
	s.actions = append(s.actions, Action{App: inst.app, Method: r.Method, Path: r.URL.Path})
	s.mu.Unlock()
	s.write(w, r, inst, http.StatusCreated, issue)
}

// write answers with ETag and rate-limit headers. A GET whose ETag matches
// If-None-Match gets 304 and does not count against the quota.
func (s *Server) write(w http.ResponseWriter, r *http.Request, inst *installation, status int, v any) int {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return http.StatusInternalServerError
	}
	sum := sha256.Sum256(body)
	etag := `W/"` + hex.EncodeToString(sum[:16]) + `"`
	notModified := r.Method == http.MethodGet && status == http.StatusOK && r.Header.Get("If-None-Match") == etag

	now := s.clk.Now()
	s.mu.Lock()
	if !now.Before(inst.resetAt) {
		inst.used, inst.resetAt = 0, now.Add(time.Hour)
	}
	if !notModified {
		inst.used++
	}
	used, reset := inst.used, inst.resetAt
	s.mu.Unlock()

	h := w.Header()
	h.Set("x-ratelimit-limit", strconv.Itoa(RateLimit))
	h.Set("x-ratelimit-remaining", strconv.Itoa(max(RateLimit-used, 0)))
	h.Set("x-ratelimit-used", strconv.Itoa(used))
	h.Set("x-ratelimit-reset", strconv.FormatInt(reset.Unix(), 10))
	h.Set("x-ratelimit-resource", "core")
	if r.Method == http.MethodGet {
		h.Set("ETag", etag)
	}
	if r.Method == http.MethodGet {
		s.mu.Lock()
		s.reads = append(s.reads, Request{Method: r.Method, Path: r.URL.RequestURI(), IfNoneMatch: r.Header.Get("If-None-Match") != "", Status: map[bool]int{true: http.StatusNotModified, false: status}[notModified]})
		s.mu.Unlock()
	}
	if notModified {
		w.WriteHeader(http.StatusNotModified)
		return http.StatusNotModified
	}
	h.Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
	return status
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// FailNextWrite makes the next write answer with the status and a Retry-After
// of that many seconds (the secondary limit of GitHub).
func (s *Server) FailNextWrite(status int, retryAfter string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failWrite, s.failWriteAfter = status, retryAfter
}
