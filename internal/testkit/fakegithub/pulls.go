package fakegithub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// The pulls API the commands of PRs use: the PR with its head and its state, the
// files of the diff, the reviews, the comments of review, the checks of a commit.
// The state is set from the test with AddPR (in issues.go), SetPull, AddPullFile,
// AddReview and AddCheck.

type pullExtra struct {
	headSHA, baseRef, state string
	draft, merged           bool
	mergeableState          string
	files                   []map[string]any
	reviews                 []map[string]any
	comments                []map[string]any
}

func (rp *repo) pull(n int) *pullExtra {
	if rp.pullx == nil {
		rp.pullx = map[int]*pullExtra{}
	}
	if rp.pullx[n] == nil {
		rp.pullx[n] = &pullExtra{headSHA: fmt.Sprintf("%040d", n), baseRef: "main", state: "open", mergeableState: "clean"}
	}
	return rp.pullx[n]
}

// SetPull sets the head commit, the draft flag and the mergeable state of a PR.
func (s *Server) SetPull(full string, n int, headSHA string, draft bool, mergeableState string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.repos[full].pull(n)
	p.headSHA, p.draft, p.mergeableState = headSHA, draft, mergeableState
}

// AddPullFile adds a file with its patch to the diff of a PR.
func (s *Server) AddPullFile(full string, n int, path, patch string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.repos[full].pull(n)
	p.files = append(p.files, map[string]any{"filename": path, "patch": patch, "status": "modified"})
}

// AddReview adds a review (APPROVED, CHANGES_REQUESTED, COMMENTED).
func (s *Server) AddReview(full string, n int, by, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.repos[full].pull(n)
	p.reviews = append(p.reviews, map[string]any{"id": s.nextID, "user": map[string]any{"login": by}, "state": state})
	s.nextID++
}

// AddCheck adds a check run to a commit; conclusion "" is one still running.
func (s *Server) AddCheck(full, sha, name, conclusion string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rp := s.repos[full]
	if rp.checks == nil {
		rp.checks = map[string][]map[string]any{}
	}
	status := "completed"
	var concl any = conclusion
	if conclusion == "" {
		status, concl = "in_progress", nil
	}
	rp.checks[sha] = append(rp.checks[sha], map[string]any{"name": name, "status": status, "conclusion": concl})
}

// PullBody is the body of a PR.
func (s *Server) PullBody(full string, n int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := s.repos[full].issues[n-1]["body"].(string)
	return b
}

// ReviewComments are the comments of review of a PR: their paths, lines and bodies.
func (s *Server) ReviewComments(full string, n int) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.repos[full].pull(n).comments...)
}

func (s *Server) servePulls(w http.ResponseWriter, r *http.Request, full string, parts []string) bool {
	switch {
	case parts[3] == "pulls" && len(parts) == 5 && (r.Method == http.MethodGet || r.Method == http.MethodPatch):
	case parts[3] == "pulls" && len(parts) == 6 && (parts[5] == "reviews" || parts[5] == "files") && r.Method == http.MethodGet:
	case parts[3] == "pulls" && len(parts) == 6 && parts[5] == "comments" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
	case parts[3] == "pulls" && len(parts) == 8 && parts[5] == "comments" && parts[7] == "replies" && r.Method == http.MethodPost:
	case parts[3] == "commits" && len(parts) == 6 && parts[5] == "check-runs" && r.Method == http.MethodGet:
	default:
		return false
	}
	inst, ok := s.auth(w, r)
	if !ok {
		return true
	}
	notFound := func() bool {
		s.write(w, r, inst, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return true
	}
	s.mu.Lock()
	rp := s.repos[full]
	s.mu.Unlock()
	if rp == nil {
		return notFound()
	}
	if parts[3] == "commits" {
		s.mu.Lock()
		list := append([]map[string]any(nil), rp.checks[parts[4]]...)
		s.mu.Unlock()
		s.write(w, r, inst, http.StatusOK, map[string]any{"total_count": len(list), "check_runs": list})
		return true
	}
	n, err := strconv.Atoi(parts[4])
	s.mu.Lock()
	head, found := rp.pulls[n]
	s.mu.Unlock()
	if err != nil || !found {
		return notFound()
	}
	switch {
	case len(parts) == 5 && r.Method == http.MethodGet:
		s.mu.Lock()
		out := s.pullView(rp, full, n, head)
		s.mu.Unlock()
		s.write(w, r, inst, http.StatusOK, out)
	case len(parts) == 5:
		var in struct {
			Title *string `json:"title"`
			Body  *string `json:"body"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if dec.Decode(&in) != nil {
			s.write(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
			return true
		}
		s.mu.Lock()
		is := rp.issues[n-1]
		if in.Title != nil {
			is["title"] = *in.Title
		}
		if in.Body != nil {
			is["body"] = *in.Body
		}
		s.actions = append(s.actions, Action{App: inst.app, Method: r.Method, Path: r.URL.Path})
		out := s.pullView(rp, full, n, head)
		s.mu.Unlock()
		s.write(w, r, inst, http.StatusOK, out)
	case parts[5] == "reviews":
		s.mu.Lock()
		list := append([]map[string]any(nil), rp.pull(n).reviews...)
		s.mu.Unlock()
		s.write(w, r, inst, http.StatusOK, list)
	case parts[5] == "files":
		s.mu.Lock()
		list := append([]map[string]any(nil), rp.pull(n).files...)
		s.mu.Unlock()
		s.write(w, r, inst, http.StatusOK, list)
	case parts[5] == "comments" && len(parts) == 6 && r.Method == http.MethodGet:
		s.mu.Lock()
		list := append([]map[string]any(nil), rp.pull(n).comments...)
		s.mu.Unlock()
		s.write(w, r, inst, http.StatusOK, list)
	default: // a comment of review, or a reply to one
		var in struct {
			Body     string `json:"body"`
			CommitID string `json:"commit_id"`
			Path     string `json:"path"`
			Line     int    `json:"line"`
			Side     string `json:"side"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Body) == "" {
			s.write(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
			return true
		}
		s.mu.Lock()
		px := rp.pull(n)
		c := map[string]any{"id": s.nextID, "body": in.Body, "user": map[string]any{"login": inst.app + "[bot]"}}
		s.nextID++
		if len(parts) == 8 {
			id, _ := strconv.ParseInt(parts[6], 10, 64)
			var parent map[string]any
			for _, o := range px.comments {
				if oid, _ := o["id"].(int64); oid == id {
					parent = o
				}
			}
			if parent == nil {
				s.mu.Unlock()
				return notFound()
			}
			c["in_reply_to_id"] = id
		} else {
			if in.Path == "" || in.Line <= 0 || in.CommitID == "" {
				s.mu.Unlock()
				s.write(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
				return true
			}
			c["path"], c["line"], c["commit_id"] = in.Path, in.Line, in.CommitID
		}
		px.comments = append(px.comments, c)
		s.actions = append(s.actions, Action{App: inst.app, Method: r.Method, Path: r.URL.Path})
		s.mu.Unlock()
		s.write(w, r, inst, http.StatusCreated, c)
	}
	return true
}

// pullView is a PR as GitHub says it (s.mu held).
func (s *Server) pullView(rp *repo, full string, n int, head [2]string) map[string]any {
	px := rp.pull(n)
	is := rp.issues[n-1]
	return map[string]any{
		"number": n, "title": is["title"], "body": is["body"], "state": px.state, "draft": px.draft, "merged": px.merged,
		"mergeable_state": px.mergeableState, "html_url": fmt.Sprintf("https://github.com/%s/pull/%d", full, n),
		"head": map[string]any{"ref": head[0], "sha": px.headSHA, "repo": map[string]any{"full_name": head[1]}},
		"base": map[string]any{"ref": px.baseRef},
	}
}
