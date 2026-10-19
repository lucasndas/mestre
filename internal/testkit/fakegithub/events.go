package fakegithub

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// The state the poller of the events reads (docs/github/eventos.md): every issue
// and PR has created_at and updated_at, every comment has the time it was
// written or edited, and what a person does moves updated_at of the issue.

// touch moves the updated_at of an issue to now. The caller holds mu.
func (s *Server) touch(rp *repo, n int) {
	if n >= 1 && n <= len(rp.issues) {
		rp.issues[n-1]["updated_at"] = s.clk.Now().UTC().Format(time.RFC3339)
	}
}

// commentView is a comment of an issue or of the review of a PR. The caller
// holds mu.
func (s *Server) commentView(id int64, full string, n int, body, login string) map[string]any {
	now := s.clk.Now().UTC().Format(time.RFC3339)
	return map[string]any{"id": id, "body": body, "user": map[string]any{"login": login},
		"created_at": now, "updated_at": now, "number": n,
		"html_url":  fmt.Sprintf("https://github.com/%s/issues/%d#issuecomment-%d", full, n, id),
		"issue_url": fmt.Sprintf("https://api.github.com/repos/%s/issues/%d", full, n)}
}

// AddIssueComment puts a comment on an issue or a PR as a person would, and
// returns its id.
func (s *Server) AddIssueComment(full string, n int, login, body string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	rp := s.repos[full]
	if rp.extra == nil {
		rp.extra = map[int]*issueExtra{}
	}
	if rp.extra[n] == nil {
		rp.extra[n] = &issueExtra{}
	}
	id := s.nextID
	s.nextID++
	rp.extra[n].comments = append(rp.extra[n].comments, s.commentView(id, full, n, body, login))
	s.touch(rp, n)
	return id
}

// EditIssueComment changes the body of a comment of an issue or a PR as its
// author would: its updated_at, and the one of the issue, move.
func (s *Server) EditIssueComment(full string, id int64, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rp := s.repos[full]
	for n, x := range rp.extra {
		for _, c := range x.comments {
			if c["id"] == id {
				c["body"], c["updated_at"] = body, s.clk.Now().UTC().Format(time.RFC3339)
				s.touch(rp, n)
			}
		}
	}
}

// serveEvents serves the lists of the whole repository the poller reads:
// GET /repos/{o}/{r}/issues/comments and /pulls/comments. It answers true when
// the request was one of them.
func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if r.Method != http.MethodGet || len(parts) != 5 || parts[0] != "repos" || parts[4] != "comments" || (parts[3] != "issues" && parts[3] != "pulls") {
		return false
	}
	for k, v := range r.URL.Query() {
		switch {
		case k == "per_page" || k == "page":
		case k == "sort" && len(v) == 1 && v[0] == "updated", k == "direction" && len(v) == 1 && v[0] == "desc":
		default:
			s.rep.Errorf("fakegithub: GET %s with the parameter %s is not implemented", r.URL.Path, k)
			http.Error(w, "not implemented", http.StatusNotImplemented)
			return true
		}
	}
	inst, ok := s.auth(w, r)
	if !ok {
		return true
	}
	full := parts[1] + "/" + parts[2]
	s.mu.Lock()
	rp := s.repos[full]
	var all []map[string]any
	if rp != nil {
		if parts[3] == "issues" {
			for _, x := range rp.extra {
				all = append(all, x.comments...)
			}
		} else {
			for _, px := range rp.pullx {
				all = append(all, px.comments...)
			}
		}
	}
	s.mu.Unlock()
	if rp == nil {
		s.write(w, r, inst, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return true
	}
	sortByUpdated(all)
	s.write(w, r, inst, http.StatusOK, paginate(all, r))
	return true
}

// sortByUpdated orders by updated_at, newest first, and by id among the equal.
func sortByUpdated(list []map[string]any) {
	sort.SliceStable(list, func(i, j int) bool {
		a, _ := list[i]["updated_at"].(string)
		b, _ := list[j]["updated_at"].(string)
		if a != b {
			return a > b
		}
		ai, _ := list[i]["id"].(int64)
		bi, _ := list[j]["id"].(int64)
		return ai > bi
	})
}

// viewPull makes the issue of a PR show the state of the PR. The caller holds mu.
func (s *Server) viewPull(rp *repo, is map[string]any) {
	pr, ok := is["pull_request"].(map[string]any)
	if !ok {
		return
	}
	n, _ := is["number"].(int)
	px := rp.pull(n)
	if px.state != "" {
		is["state"] = px.state
	}
	_ = pr
}

// Reads are the GET requests of the REST API answered so far, with the address
// as asked (the query too), whether they were conditional and the status.
func (s *Server) Reads() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.reads...)
}

// FailNextRead makes the next GET of a repository answer with the status and the
// headers (a 429 with retry-after is the limit of GitHub). It does not count.
func (s *Server) FailNextRead(status int, headers map[string]string) {
	s.mu.Lock()
	s.readFail, s.readFailHdr = status, headers
	s.mu.Unlock()
}

func (s *Server) failRead(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/repos/") {
		return false
	}
	s.mu.Lock()
	st, hdr := s.readFail, s.readFailHdr
	s.readFail, s.readFailHdr = 0, nil
	s.mu.Unlock()
	if st == 0 {
		return false
	}
	for k, v := range hdr {
		w.Header().Set(k, v)
	}
	writeJSON(w, st, map[string]any{"message": "injected failure"})
	return true
}
