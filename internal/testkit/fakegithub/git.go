package fakegithub

import (
	"crypto/sha1" //nolint:gosec // the id of a git object, not a secret
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mestre-ia/mestre/internal/testkit"
)

// Request is one call the double answered, for the tests that count what a
// client sent (the lock checks every 30 s; the conditional ones are 304).
type Request struct {
	Method, Path string
	// IfNoneMatch says the request was conditional.
	IfNoneMatch bool
	Status      int
}

type gitData struct {
	commits map[string]gitCommit
	refs    map[string]string // "refs/heads/main" → sha
	order   []string          // refs in the order they were made
}

type gitCommit struct {
	Message string
	Tree    string
	Parents []string
}

// ensureGit gives the repository its first commit and `main`, as a repository
// made with auto_init has.
func (rp *repo) ensureGit() *gitData {
	if rp.git == nil {
		rp.git = &gitData{commits: map[string]gitCommit{}, refs: map[string]string{}}
		tree := objectID("tree", "empty")
		sha := objectID("commit", "initial commit")
		rp.git.commits[sha] = gitCommit{Message: "Initial commit", Tree: tree}
		rp.git.refs["refs/heads/main"] = sha
		rp.git.order = append(rp.git.order, "refs/heads/main")
	}
	return rp.git
}

func objectID(kind, content string) string {
	sum := sha1.Sum([]byte(kind + " " + content)) //nolint:gosec
	return hex.EncodeToString(sum[:])
}

// DownGit makes every call to the git API of the repositories answer with the
// status (a 503, a 403 with a limit) until UpGit: the GitHub that is out for the
// lock.
func (s *Server) DownGit(status int) {
	s.mu.Lock()
	s.gitDown = status
	s.mu.Unlock()
}

// UpGit makes the git API answer again.
func (s *Server) UpGit() { s.DownGit(0) }

// AddRef makes a ref of a repository, as another machine's claim would
// (owner/name, the full ref, a new commit), and returns the sha.
func (s *Server) AddRef(full, ref, message string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	rp, ok := s.repos[full]
	if !ok {
		rp = &repo{}
		s.repos[full] = rp
	}
	g := rp.ensureGit()
	sha := objectID("commit", message+ref)
	g.commits[sha] = gitCommit{Message: message, Tree: g.commits[g.refs["refs/heads/main"]].Tree, Parents: []string{g.refs["refs/heads/main"]}}
	g.refs[ref] = sha
	g.order = append(g.order, ref)
	return sha
}

// Requests are the calls to the git API answered so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// serveGit answers the git API the lock uses: the refs by prefix (with ETag), a
// ref, a commit, the creation of a commit and of a ref. It reports whether the
// request was one of them.
func (s *Server) serveGit(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 3 && parts[0] == "repos" && r.Method == http.MethodGet {
		return s.serveRepo(w, r, parts[1]+"/"+parts[2])
	}
	if len(parts) < 5 || parts[0] != "repos" || parts[3] != "git" {
		return false
	}
	full := parts[1] + "/" + parts[2]
	rest := strings.Join(parts[5:], "/")
	log := func(status int) {
		s.mu.Lock()
		s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, IfNoneMatch: r.Header.Get("If-None-Match") != "", Status: status})
		s.mu.Unlock()
	}
	s.mu.Lock()
	down := s.gitDown
	s.mu.Unlock()
	if down != 0 {
		log(down)
		writeJSON(w, down, map[string]any{"message": "Service Unavailable"})
		return true
	}
	inst, ok := s.auth(w, r)
	if !ok {
		log(http.StatusUnauthorized)
		return true
	}
	s.mu.Lock()
	rp, exists := s.repos[full]
	var g *gitData
	if exists {
		g = rp.ensureGit()
	}
	s.mu.Unlock()
	if !exists {
		log(http.StatusNotFound)
		s.write(w, r, inst, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return true
	}
	status, body := http.StatusNotFound, any(map[string]any{"message": "Not Found"})
	switch {
	case r.Method == http.MethodGet && parts[4] == "matching-refs":
		prefix := "refs/" + rest
		s.mu.Lock()
		names := make([]string, 0, len(g.order))
		for _, ref := range g.order {
			if strings.HasPrefix(ref, prefix) {
				names = append(names, ref)
			}
		}
		list := make([]map[string]any, 0, len(names))
		for i := len(names) - 1; i >= 0; i-- { // the order of the answer is not guaranteed: newest first here
			list = append(list, refObject(names[i], g.refs[names[i]]))
		}
		page, size := 1, 100
		if v, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && v > 0 {
			page = v
		}
		if s.pageSize > 0 {
			size = s.pageSize
		}
		more := len(list) > page*size
		list = list[min((page-1)*size, len(list)):min(page*size, len(list))]
		s.mu.Unlock()
		if more { // GitHub writes these links with /repositories/<id>
			w.Header().Set("Link", fmt.Sprintf(`<https://api.github.com/repositories/1/git/matching-refs/%s?per_page=100&page=%d>; rel="next"`, rest, page+1))
		}
		status, body = http.StatusOK, list
	case r.Method == http.MethodGet && parts[4] == "ref":
		s.mu.Lock()
		sha, found := g.refs["refs/"+rest]
		s.mu.Unlock()
		if found {
			status, body = http.StatusOK, refObject("refs/"+rest, sha)
		}
	case r.Method == http.MethodGet && parts[4] == "commits":
		s.mu.Lock()
		c, found := g.commits[rest]
		s.mu.Unlock()
		if found {
			status, body = http.StatusOK, map[string]any{"sha": rest, "message": c.Message, "tree": map[string]any{"sha": c.Tree}}
		}
	case r.Method == http.MethodPost && parts[4] == "commits" && rest == "":
		var in struct {
			Message string   `json:"message"`
			Tree    string   `json:"tree"`
			Parents []string `json:"parents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Message == "" || in.Tree == "" {
			status, body = http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"}
			break
		}
		sha := objectID("commit", in.Message+in.Tree+strings.Join(in.Parents, ","))
		s.mu.Lock()
		g.commits[sha] = gitCommit{Message: in.Message, Tree: in.Tree, Parents: in.Parents}
		s.actions = append(s.actions, Action{App: inst.app, Method: r.Method, Path: r.URL.Path})
		s.mu.Unlock()
		status, body = http.StatusCreated, map[string]any{"sha": sha, "message": in.Message}
	case r.Method == http.MethodPost && parts[4] == "refs" && rest == "":
		var in struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !strings.HasPrefix(in.Ref, "refs/") {
			status, body = http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"}
			break
		}
		s.mu.Lock()
		lose := s.loseRefAnswer
		s.loseRefAnswer = false
		_, taken := g.refs[in.Ref]
		_, known := g.commits[in.SHA]
		switch {
		case taken:
			status, body = http.StatusUnprocessableEntity, map[string]any{"message": "Reference already exists"}
		case !known:
			status, body = http.StatusUnprocessableEntity, map[string]any{"message": "Object does not exist"}
		default:
			g.refs[in.Ref] = in.SHA
			g.order = append(g.order, in.Ref)
			s.actions = append(s.actions, Action{App: inst.app, Method: r.Method, Path: r.URL.Path})
			status, body = http.StatusCreated, refObject(in.Ref, in.SHA)
			if lose { // the server did it and the answer was lost
				status, body = http.StatusBadGateway, map[string]any{"message": "Bad Gateway"}
			}
		}
		s.mu.Unlock()
	default:
		testkit.NotImplemented(s.rep, w, r.Method+" "+r.URL.Path)
		return true
	}
	log(s.write(w, r, inst, status, body))
	return true
}

func refObject(ref, sha string) map[string]any {
	return map[string]any{"ref": ref, "object": map[string]any{"sha": sha, "type": "commit"}}
}

// Used is how many requests the token has spent of its quota (a 304 spends none).
func (s *Server) Used(token string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inst, ok := s.tokens[token]; ok {
		return inst.used
	}
	return 0
}

// serveRepo answers GET /repos/{owner}/{repo} for the repositories made with
// AddRepo: the name, the default branch and that it is private.
func (s *Server) serveRepo(w http.ResponseWriter, r *http.Request, full string) bool {
	inst, ok := s.auth(w, r)
	if !ok {
		return true
	}
	s.mu.Lock()
	_, exists := s.repos[full]
	branch := s.defaultBranch[full]
	s.mu.Unlock()
	if !exists {
		s.write(w, r, inst, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return true
	}
	if branch == "" {
		branch = "main"
	}
	s.mu.Lock()
	id := s.approval().repoIDs[full]
	s.mu.Unlock()
	s.write(w, r, inst, http.StatusOK, map[string]any{"full_name": full, "id": id, "private": !s.public[full], "default_branch": branch})
	return true
}

// SetDefaultBranch names the default branch of a repository (an account that
// does not use main).
func (s *Server) SetDefaultBranch(full, branch string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rp, ok := s.repos[full]
	if !ok {
		return
	}
	g := rp.ensureGit()
	g.refs["refs/heads/"+branch] = g.refs["refs/heads/main"]
	delete(g.refs, "refs/heads/main")
	if s.defaultBranch == nil {
		s.defaultBranch = map[string]string{}
	}
	s.defaultBranch[full] = branch
}

// LoseNextRefAnswer makes the next successful creation of a ref answer 502
// after the server made it (the answer was lost on the way).
func (s *Server) LoseNextRefAnswer() {
	s.mu.Lock()
	s.loseRefAnswer = true
	s.mu.Unlock()
}

// SetPageSize makes the listings of refs have that many per page (0: 100).
func (s *Server) SetPageSize(n int) {
	s.mu.Lock()
	s.pageSize = n
	s.mu.Unlock()
}
