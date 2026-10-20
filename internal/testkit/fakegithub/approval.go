package fakegithub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/testkit"
)

// The API of the approval on GitHub (docs/github/aprovacao-no-github.md): the
// reviews of a PR, the rules of the main branch and its protection, the
// rulesets, the permission of a collaborator, the reviewDecision of GraphQL,
// the files of a branch and the device flow of the user access tokens. The
// state is set from the test with SetBranchRules, SetProtection, SetPro,
// SetPermission, AddUser, SetFile and AuthorizeDevice.

// Rule is a rule of a branch as GET /repos/{o}/{r}/rules/branches/{b} lists it.
type Rule struct {
	Type       string
	Parameters map[string]any
}

type deviceGrant struct {
	clientID, userCode, login string
	authorized, denied        bool
	expiresAt                 time.Time
	slowDown                  int
	spent                     bool
}

type approvalState struct {
	rules       map[string][]Rule         // full|branch
	protection  map[string]map[string]any // full|branch
	rulesets    map[string][]map[string]any
	pro         map[string]bool
	perms       map[string]string // full|login
	permStatus  int
	humans      map[string]string // login → User or Organization
	assoc       map[string]string // login → author_association
	appsDeny    map[string]bool   // full → true: the reviews of Apps do not count (the default counts)
	files       map[string]string // full|branch|path → text
	repoIDs     map[string]int64
	devices     map[string]*deviceGrant
	refresh     map[string]string // refresh token → login
	secrets     map[string]string // client id → client secret
	revoked     []string
	tokenLife   time.Duration
	refreshFail bool
}

func (s *Server) approval() *approvalState {
	if s.ap == nil {
		s.ap = &approvalState{rules: map[string][]Rule{}, protection: map[string]map[string]any{}, rulesets: map[string][]map[string]any{}, pro: map[string]bool{},
			perms: map[string]string{}, humans: map[string]string{}, assoc: map[string]string{}, appsDeny: map[string]bool{}, files: map[string]string{},
			repoIDs: map[string]int64{}, devices: map[string]*deviceGrant{}, refresh: map[string]string{}, secrets: map[string]string{}, tokenLife: 8 * time.Hour}
	}
	return s.ap
}

// SetBranchRules sets the rules that GET /rules/branches/{branch} lists.
func (s *Server) SetBranchRules(full, branch string, rules ...Rule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approval().rules[full+"|"+branch] = rules
}

// SetProtection sets the classic protection of a branch (the body of GET
// /branches/{branch}/protection); nil takes it away.
func (s *Server) SetProtection(full, branch string, body map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if body == nil {
		delete(s.approval().protection, full+"|"+branch)
		return
	}
	s.approval().protection[full+"|"+branch] = body
}

// SetPro says the account of the repository has GitHub Pro: rulesets work in a
// private repository.
func (s *Server) SetPro(full string, pro bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approval().pro[full] = pro
}

// Rulesets are the rulesets created through the API, as they were sent.
func (s *Server) Rulesets(full string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.approval().rulesets[full]...)
}

// SetPermission sets the permission of a collaborator (admin, write, read, none).
func (s *Server) SetPermission(full, login, permission string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approval().perms[full+"|"+strings.ToLower(login)] = permission
}

// FailPermission makes the read of a permission answer the status (0: as usual),
// as it does for an App without the permission to read it.
func (s *Server) FailPermission(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approval().permStatus = status
}

// AddUser registers a person (or an organization) with the type GitHub gives it.
func (s *Server) AddUser(login, typ string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approval().humans[strings.ToLower(login)] = typ
}

// SetAssociation sets the author_association of the reviews of a login.
func (s *Server) SetAssociation(login, assoc string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approval().assoc[strings.ToLower(login)] = assoc
}

// SetAppReviewsCount says whether the review of an App counts for the required
// reviews of the repository (true by default); the double does not know, as
// GitHub does not say.
func (s *Server) SetAppReviewsCount(full string, counts bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approval().appsDeny[full] = !counts
}

// SetFile puts a text file on a branch of a repository, as a push would.
func (s *Server) SetFile(full, branch, path, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approval().files[full+"|"+branch+"|"+path] = content
}

// File is the text of a file of a branch ("" and false when it does not exist).
func (s *Server) File(full, branch, path string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.approval().files[full+"|"+branch+"|"+path]
	return v, ok
}

// SetRepoID gives a repository the numeric id the installation API asks for.
func (s *Server) SetRepoID(full string, id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approval().repoIDs[full] = id
}

// SetClientSecret sets the client secret of an App (by its client id): the
// renewal of a user token needs it.
func (s *Server) SetClientSecret(clientID, secret string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approval().secrets[clientID] = secret
}

// SetUserTokenLife sets how long a user access token lives (8 hours by default).
func (s *Server) SetUserTokenLife(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approval().tokenLife = d
}

// RefuseRefresh makes the renewal of a user token fail (the refresh token is
// refused).
func (s *Server) RefuseRefresh(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approval().refreshFail = on
}

// AuthorizeDevice is the user typing the code on github.com/login/device and
// authorizing: the next poll of that device code gets a token for login.
func (s *Server) AuthorizeDevice(userCode, login string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.approval().devices {
		if g.userCode == userCode {
			g.authorized, g.login = true, login
		}
	}
}

// DenyDevice is the user refusing the authorization.
func (s *Server) DenyDevice(userCode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.approval().devices {
		if g.userCode == userCode {
			g.denied = true
		}
	}
}

// SlowDown makes the next polls of a device code answer slow_down that many times.
func (s *Server) SlowDown(userCode string, times int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.approval().devices {
		if g.userCode == userCode {
			g.slowDown = times
		}
	}
}

// RevokedTokens are the user tokens the engine revoked.
func (s *Server) RevokedTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.approval().revoked...)
}

// IssueUserToken makes a user access token for a login, as the device flow gives.
func (s *Server) IssueUserToken(token, login string) {
	now := s.clk.Now()
	s.mu.Lock()
	s.tokens[token] = &installation{user: login, expiresAt: now.Add(s.approval().tokenLife), resetAt: now.Add(time.Hour)}
	s.mu.Unlock()
}

func (s *Server) userType(login string) string {
	if strings.HasSuffix(login, "[bot]") {
		return "Bot"
	}
	if t, ok := s.approval().humans[strings.ToLower(login)]; ok {
		return t
	}
	return "User"
}

// actor is the login of who a token acts as.
func actorOf(inst *installation) string {
	if inst.user != "" {
		return inst.user
	}
	return inst.app + "[bot]"
}

// serveApproval serves the routes of this file; false is any other request.
func (s *Server) serveApproval(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/login/oauth/access_token":
		s.accessTokenUser(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/user":
		s.currentUser(w, r)
	case r.Method == http.MethodDelete && len(parts) == 3 && parts[0] == "applications" && parts[2] == "grant":
		s.revokeGrant(w, r, parts[1])
	case r.Method == http.MethodPut && len(parts) == 5 && parts[0] == "user" && parts[1] == "installations" && parts[3] == "repositories":
		s.addToInstallation(w, r, parts[2], parts[4])
	case len(parts) >= 4 && parts[0] == "repos":
		return s.serveRepoApproval(w, r, parts)
	default:
		return false
	}
	return true
}

func (s *Server) serveRepoApproval(w http.ResponseWriter, r *http.Request, parts []string) bool {
	full := parts[1] + "/" + parts[2]
	switch {
	case r.Method == http.MethodPost && len(parts) == 6 && parts[3] == "pulls" && parts[5] == "reviews":
		s.createReview(w, r, full, parts[4])
	case r.Method == http.MethodGet && len(parts) == 6 && parts[3] == "rules" && parts[4] == "branches":
		s.branchRules(w, r, full, parts[5])
	case r.Method == http.MethodGet && len(parts) == 6 && parts[3] == "branches" && parts[5] == "protection":
		s.branchProtection(w, r, full, parts[4])
	case r.Method == http.MethodPost && len(parts) == 4 && parts[3] == "rulesets":
		s.createRuleset(w, r, full)
	case r.Method == http.MethodGet && len(parts) == 6 && parts[3] == "collaborators" && parts[5] == "permission":
		s.collaboratorPermission(w, r, full, parts[4])
	case len(parts) >= 5 && parts[3] == "contents" && (r.Method == http.MethodPut || r.Method == http.MethodGet):
		s.contents(w, r, full, strings.Join(parts[4:], "/"))
	case r.Method == http.MethodDelete && len(parts) >= 6 && parts[3] == "git" && parts[4] == "refs":
		s.deleteRef(w, r, full, "refs/"+strings.Join(parts[5:], "/"))
	default:
		return false
	}
	return true
}

func (s *Server) reply(w http.ResponseWriter, r *http.Request, inst *installation, status int, v any) {
	s.write(w, r, inst, status, v)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request, inst *installation) {
	s.write(w, r, inst, http.StatusNotFound, map[string]any{"message": "Not Found"})
}

func (s *Server) createReview(w http.ResponseWriter, r *http.Request, full, num string) {
	inst, ok := s.auth(w, r)
	if !ok {
		return
	}
	var in struct {
		CommitID string `json:"commit_id"`
		Body     string `json:"body"`
		Event    string `json:"event"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	n, nerr := strconv.Atoi(num)
	if err := dec.Decode(&in); err != nil || nerr != nil {
		s.reply(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
		return
	}
	state := map[string]string{"APPROVE": "APPROVED", "REQUEST_CHANGES": "CHANGES_REQUESTED", "COMMENT": "COMMENTED"}[in.Event]
	s.mu.Lock()
	rp := s.repos[full]
	if rp == nil || rp.pulls == nil || rp.pulls[n] == [2]string{} {
		s.mu.Unlock()
		s.notFound(w, r, inst)
		return
	}
	px := rp.pull(n)
	author, _ := rp.issues[n-1]["user"].(map[string]any)["login"].(string)
	login := actorOf(inst)
	switch {
	case state == "":
		s.mu.Unlock()
		s.reply(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed", "errors": []string{"Review event is invalid"}})
		return
	case (state == "CHANGES_REQUESTED" || state == "COMMENTED") && strings.TrimSpace(in.Body) == "":
		s.mu.Unlock()
		s.reply(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed", "errors": []string{"Body is required"}})
		return
	case state == "APPROVED" && strings.EqualFold(author, login):
		s.mu.Unlock()
		s.reply(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Unprocessable Entity", "errors": []string{"Can not approve your own pull request"}})
		return
	case state != "COMMENTED" && px.state != "open":
		s.mu.Unlock()
		s.reply(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Unprocessable Entity", "errors": []string{"Pull request is not open"}})
		return
	}
	sha := in.CommitID
	if sha == "" {
		sha = px.headSHA
	}
	rv := s.reviewView(full, n, login, state, sha)
	rv["body"] = in.Body
	px.reviews = append(px.reviews, rv)
	s.nextID++
	s.touch(rp, n)
	s.actions = append(s.actions, Action{App: actorOf(inst), Method: r.Method, Path: r.URL.Path})
	s.mu.Unlock()
	s.reply(w, r, inst, http.StatusOK, rv)
}

// DismissReview dismisses the reviews of a login on a PR, as a person with
// write access (or a push, with "dismiss stale reviews") would.
func (s *Server) DismissReview(full string, n int, login string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	px := s.repos[full].pull(n)
	for _, rv := range px.reviews {
		if u, _ := rv["user"].(map[string]any); u != nil && strings.EqualFold(u["login"].(string), login) && rv["state"] == "APPROVED" {
			rv["state"] = "DISMISSED"
		}
	}
	s.touch(s.repos[full], n)
}

// SentReview is a review of a PR as the double has it.
type SentReview struct {
	By, State, Commit, Body string
}

// ReviewsOf are the reviews of a PR in the order they were made.
func (s *Server) ReviewsOf(full string, n int) []SentReview {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []SentReview
	for _, rv := range s.repos[full].pull(n).reviews {
		u, _ := rv["user"].(map[string]any)
		by, _ := u["login"].(string)
		st, _ := rv["state"].(string)
		sha, _ := rv["commit_id"].(string)
		body, _ := rv["body"].(string)
		out = append(out, SentReview{By: by, State: st, Commit: sha, Body: body})
	}
	return out
}

func (s *Server) branchRules(w http.ResponseWriter, r *http.Request, full, branch string) {
	inst, ok := s.auth(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	_, exists := s.repos[full]
	rules := s.approval().rules[full+"|"+branch]
	s.mu.Unlock()
	if !exists {
		s.notFound(w, r, inst)
		return
	}
	out := make([]map[string]any, 0, len(rules))
	for _, ru := range rules {
		item := map[string]any{"type": ru.Type, "ruleset_source_type": "Repository", "ruleset_id": 1}
		if ru.Parameters != nil {
			item["parameters"] = ru.Parameters
		}
		out = append(out, item)
	}
	s.reply(w, r, inst, http.StatusOK, out)
}

func (s *Server) branchProtection(w http.ResponseWriter, r *http.Request, full, branch string) {
	inst, ok := s.auth(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	_, exists := s.repos[full]
	body, has := s.approval().protection[full+"|"+branch]
	s.mu.Unlock()
	if !exists || !has {
		s.reply(w, r, inst, http.StatusNotFound, map[string]any{"message": "Branch not protected"})
		return
	}
	s.reply(w, r, inst, http.StatusOK, body)
}

func (s *Server) createRuleset(w http.ResponseWriter, r *http.Request, full string) {
	inst, ok := s.auth(w, r)
	if !ok {
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil || in["name"] == nil || in["target"] == nil || in["enforcement"] == nil {
		s.reply(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
		return
	}
	s.mu.Lock()
	_, exists := s.repos[full]
	private := !s.public[full]
	pro := s.approval().pro[full]
	if exists && (!private || pro) {
		in["id"] = s.nextID
		s.nextID++
		s.approval().rulesets[full] = append(s.approval().rulesets[full], in)
		s.actions = append(s.actions, Action{App: actorOf(inst), Method: r.Method, Path: r.URL.Path})
	}
	s.mu.Unlock()
	switch {
	case !exists:
		s.notFound(w, r, inst)
	case private && !pro:
		s.reply(w, r, inst, http.StatusForbidden, map[string]any{"message": "Upgrade to GitHub Pro or make this repository public to enable this feature."})
	default:
		s.reply(w, r, inst, http.StatusCreated, in)
	}
}

func (s *Server) collaboratorPermission(w http.ResponseWriter, r *http.Request, full, login string) {
	inst, ok := s.auth(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	st := s.approval().permStatus
	_, exists := s.repos[full]
	perm, has := s.approval().perms[full+"|"+strings.ToLower(login)]
	s.mu.Unlock()
	switch {
	case st != 0:
		s.reply(w, r, inst, st, map[string]any{"message": "Resource not accessible by integration"})
	case !exists:
		s.notFound(w, r, inst)
	case !has:
		s.reply(w, r, inst, http.StatusOK, map[string]any{"permission": "none", "user": map[string]any{"login": login}})
	default:
		s.reply(w, r, inst, http.StatusOK, map[string]any{"permission": perm, "user": map[string]any{"login": login}})
	}
}

// contents is PUT (create or change a file of a branch, in a new commit) and GET
// of /contents/{path}?ref=.
func (s *Server) contents(w http.ResponseWriter, r *http.Request, full, path string) {
	inst, ok := s.auth(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	rp := s.repos[full]
	s.mu.Unlock()
	if rp == nil {
		s.notFound(w, r, inst)
		return
	}
	if r.Method == http.MethodGet {
		branch := r.URL.Query().Get("ref")
		if branch == "" {
			branch = "main"
		}
		s.mu.Lock()
		text, has := s.approval().files[full+"|"+branch+"|"+path]
		s.mu.Unlock()
		if !has {
			s.notFound(w, r, inst)
			return
		}
		s.reply(w, r, inst, http.StatusOK, map[string]any{"type": "file", "path": path, "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(text))})
		return
	}
	var in struct {
		Message string `json:"message"`
		Content string `json:"content"`
		Branch  string `json:"branch"`
		SHA     string `json:"sha"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Message == "" || in.Branch == "" {
		s.reply(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
		return
	}
	text, err := base64.StdEncoding.DecodeString(in.Content)
	if err != nil {
		s.reply(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "content is not valid Base64"})
		return
	}
	s.mu.Lock()
	g := rp.ensureGit()
	ref := "refs/heads/" + in.Branch
	parent, exists := g.refs[ref]
	if !exists {
		s.mu.Unlock()
		s.reply(w, r, inst, http.StatusNotFound, map[string]any{"message": "Branch " + in.Branch + " not found"})
		return
	}
	sha := objectID("commit", in.Message+ref+path+string(text)+parent)
	g.commits[sha] = gitCommit{Message: in.Message, Tree: objectID("tree", path+string(text)), Parents: []string{parent}}
	g.refs[ref] = sha
	s.approval().files[full+"|"+in.Branch+"|"+path] = string(text)
	for n, px := range rp.pullx {
		if rp.pulls[n][0] == in.Branch && px.state == "open" {
			px.headSHA = sha // the PR of the branch follows its tip
			s.touch(rp, n)
		}
	}
	s.actions = append(s.actions, Action{App: actorOf(inst), Method: r.Method, Path: r.URL.Path})
	s.mu.Unlock()
	s.reply(w, r, inst, http.StatusCreated, map[string]any{"content": map[string]any{"path": path}, "commit": map[string]any{"sha": sha}})
}

func (s *Server) deleteRef(w http.ResponseWriter, r *http.Request, full, ref string) {
	inst, ok := s.auth(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	rp := s.repos[full]
	var found bool
	if rp != nil {
		g := rp.ensureGit()
		if _, found = g.refs[ref]; found {
			delete(g.refs, ref)
			for i, o := range g.order {
				if o == ref {
					g.order = append(g.order[:i], g.order[i+1:]...)
					break
				}
			}
			// GitHub closes the open PRs of a branch that is deleted
			for n, px := range rp.pullx {
				if rp.pulls[n][0] == strings.TrimPrefix(ref, "refs/heads/") && px.state == "open" {
					px.state = "closed"
					rp.issues[n-1]["state"] = "closed"
					rp.issues[n-1]["closed_at"] = s.clk.Now().UTC().Format(time.RFC3339)
					s.touch(rp, n)
				}
			}
			s.actions = append(s.actions, Action{App: actorOf(inst), Method: r.Method, Path: r.URL.Path})
		}
	}
	s.mu.Unlock()
	if !found {
		s.reply(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Reference does not exist"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reviewDecision answers the GraphQL query of the reviewDecision of a PR: null
// when the branch asks for no review, APPROVED when the required number of
// approvals stands, REVIEW_REQUIRED otherwise.
func (s *Server) reviewDecision(w http.ResponseWriter, r *http.Request, inst *installation, vars map[string]any) {
	owner, _ := vars["owner"].(string)
	name, _ := vars["name"].(string)
	num, _ := vars["number"].(float64)
	full, n := owner+"/"+name, int(num)
	s.mu.Lock()
	rp := s.repos[full]
	if rp == nil || rp.pulls == nil || rp.pulls[n] == [2]string{} {
		s.mu.Unlock()
		s.write(w, r, inst, http.StatusOK, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": nil}}, "errors": []map[string]any{{"message": "Could not resolve to a PullRequest"}}})
		return
	}
	px := rp.pull(n)
	author, _ := rp.issues[n-1]["user"].(map[string]any)["login"].(string)
	required, staleDismissed := 0, false
	for _, ru := range s.approval().rules[full+"|"+px.baseRef] {
		if ru.Type == "pull_request" {
			if v, ok := ru.Parameters["required_approving_review_count"].(int); ok {
				required = max(required, v)
			}
			staleDismissed = staleDismissed || ru.Parameters["dismiss_stale_reviews_on_push"] == true
		}
	}
	if p := s.approval().protection[full+"|"+px.baseRef]; p != nil {
		if rv, ok := p["required_pull_request_reviews"].(map[string]any); ok {
			if v, ok := rv["required_approving_review_count"].(int); ok {
				required = max(required, v)
			}
			staleDismissed = staleDismissed || rv["dismiss_stale_reviews"] == true
		}
	}
	latest := map[string]map[string]any{}
	for _, rv := range px.reviews {
		login := strings.ToLower(rv["user"].(map[string]any)["login"].(string))
		if rv["state"] == "COMMENTED" {
			continue
		}
		latest[login] = rv
	}
	approved, changes := 0, false
	for login, rv := range latest {
		switch rv["state"] {
		case "CHANGES_REQUESTED":
			changes = true
		case "APPROVED":
			if strings.EqualFold(login, author) || (staleDismissed && rv["commit_id"] != px.headSHA) {
				continue
			}
			if strings.HasSuffix(login, "[bot]") && s.approval().appsDeny[full] {
				continue
			}
			approved++
		}
	}
	s.mu.Unlock()
	var decision any
	switch {
	case changes:
		decision = "CHANGES_REQUESTED"
	case required == 0:
		decision = nil
	case approved >= required:
		decision = "APPROVED"
	default:
		decision = "REVIEW_REQUIRED"
	}
	s.write(w, r, inst, http.StatusOK, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{"reviewDecision": decision}}}})
}

func (s *Server) currentUser(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.auth(w, r)
	if !ok {
		return
	}
	if inst.user == "" {
		s.reply(w, r, inst, http.StatusForbidden, map[string]any{"message": "Resource not accessible by integration"})
		return
	}
	s.mu.Lock()
	typ := s.userType(inst.user)
	s.mu.Unlock()
	s.reply(w, r, inst, http.StatusOK, map[string]any{"login": inst.user, "id": 1, "type": typ})
}

func (s *Server) addToInstallation(w http.ResponseWriter, r *http.Request, installation, repoID string) {
	inst, ok := s.auth(w, r)
	if !ok {
		return
	}
	if inst.user == "" {
		s.reply(w, r, inst, http.StatusForbidden, map[string]any{"message": "Resource not accessible by integration"})
		return
	}
	iid, _ := strconv.ParseInt(installation, 10, 64)
	rid, _ := strconv.ParseInt(repoID, 10, 64)
	s.mu.Lock()
	app := s.installs[iid]
	var full string
	for f, id := range s.approval().repoIDs {
		if id == rid {
			full = f
		}
	}
	if app != nil && full != "" {
		if !contains(app.repos, full) {
			app.repos = append(app.repos, full)
		}
		s.actions = append(s.actions, Action{App: actorOf(inst), Method: r.Method, Path: r.URL.Path})
	}
	s.mu.Unlock()
	if app == nil || full == "" {
		s.notFound(w, r, inst)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// accessTokenUser is POST /login/oauth/access_token: the poll of a device code
// and the renewal with a refresh token. GitHub answers 200 with an error field.
func (s *Server) accessTokenUser(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad_request"})
		return
	}
	clientID := r.PostForm.Get("client_id")
	now := s.clk.Now()
	s.mu.Lock()
	ap := s.approval()
	var login string
	switch r.PostForm.Get("grant_type") {
	case "urn:ietf:params:oauth:grant-type:device_code":
		g := ap.devices[r.PostForm.Get("device_code")]
		switch {
		case g == nil || g.clientID != clientID || g.spent:
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"error": "bad_verification_code"})
			return
		case !now.Before(g.expiresAt):
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"error": "expired_token"})
			return
		case g.slowDown > 0:
			g.slowDown--
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"error": "slow_down", "interval": 10})
			return
		case g.denied:
			g.spent = true
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"error": "access_denied"})
			return
		case !g.authorized:
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"error": "authorization_pending"})
			return
		}
		g.spent, login = true, g.login
	case "refresh_token":
		rt := r.PostForm.Get("refresh_token")
		login = ap.refresh[rt]
		secret, hasSecret := ap.secrets[clientID]
		if login == "" || ap.refreshFail || r.PostForm.Get("client_secret") == "" || (hasSecret && r.PostForm.Get("client_secret") != secret) {
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"error": "bad_refresh_token"})
			return
		}
		delete(ap.refresh, rt) // the refresh token is used once
	default:
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"error": "unsupported_grant_type"})
		return
	}
	s.nextID++
	access := fmt.Sprintf("ghu_fake%08d", s.nextID)
	refresh := fmt.Sprintf("ghr_fake%08d", s.nextID)
	life := ap.tokenLife
	s.tokens[access] = &installation{user: login, expiresAt: now.Add(life), resetAt: now.Add(time.Hour)}
	ap.refresh[refresh] = login
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"access_token": access, "token_type": "bearer", "scope": "", "expires_in": int(life.Seconds()),
		"refresh_token": refresh, "refresh_token_expires_in": 15811200})
}

// revokeGrant is DELETE /applications/{client_id}/grant: the user token is
// revoked. The App authenticates with its client id and secret.
func (s *Server) revokeGrant(w http.ResponseWriter, r *http.Request, clientID string) {
	id, secret, ok := r.BasicAuth()
	s.mu.Lock()
	want, has := s.approval().secrets[clientID]
	s.mu.Unlock()
	if !ok || id != clientID || secret == "" || (has && secret != want) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "Bad credentials"})
		return
	}
	var in struct {
		AccessToken string `json:"access_token"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.AccessToken == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
		return
	}
	s.mu.Lock()
	if _, known := s.tokens[in.AccessToken]; known {
		delete(s.tokens, in.AccessToken)
		s.approval().revoked = append(s.approval().revoked, in.AccessToken)
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// registerDevice is what POST /login/device/code does for an App with the flow
// enabled: it makes the grant and answers its codes.
func (s *Server) registerDevice(clientID string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	dev := fmt.Sprintf("fake-device-%08d", s.nextID)
	user := fmt.Sprintf("ABCD-%04d", s.nextID%10000)
	s.approval().devices[dev] = &deviceGrant{clientID: clientID, userCode: user, expiresAt: s.clk.Now().Add(15 * time.Minute)}
	return map[string]any{"device_code": dev, "user_code": user, "verification_uri": "https://github.com/login/device", "expires_in": 900, "interval": 5}
}

var _ = testkit.NotImplemented
