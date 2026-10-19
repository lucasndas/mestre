package fakegithub

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// The issues API the guard of the actions uses: the parent of a sub-issue, the
// comments, the labels of a repository. The state is set from the test with
// AddIssue, SetParent and LabelIssue.

type issueExtra struct {
	parent   int
	comments []map[string]any
}

// AddIssue creates an issue (as a person would) and returns its number.
func (s *Server) AddIssue(full, title string, labels ...string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	rp := s.repos[full]
	if rp == nil {
		rp = &repo{}
		s.repos[full] = rp
	}
	n := len(rp.issues) + 1
	ls := make([]map[string]any, 0, len(labels))
	for _, l := range labels {
		ls = append(ls, map[string]any{"name": l})
	}
	id := s.nextID
	s.nextID++
	rp.issues = append(rp.issues, map[string]any{"id": id, "number": n, "title": title, "labels": ls, "user": map[string]any{"login": "lucas"}})
	return n
}

// SetParent makes child a sub-issue of parent.
func (s *Server) SetParent(full string, child, parent int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rp := s.repos[full]
	if rp.extra == nil {
		rp.extra = map[int]*issueExtra{}
	}
	if rp.extra[child] == nil {
		rp.extra[child] = &issueExtra{}
	}
	rp.extra[child].parent = parent
}

// Labels are the labels of the repository.
func (s *Server) Labels(full string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for k, v := range s.repos[full].labels {
		out[k] = v
	}
	return out
}

// Comments are the bodies of the comments of an issue.
func (s *Server) Comments(full string, n int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	if x := s.repos[full].extra[n]; x != nil {
		for _, c := range x.comments {
			out = append(out, c["body"].(string))
		}
	}
	return out
}

// serveIssues answers the parent, the comments and the labels of the
// repository; false is any other request.
func (s *Server) serveIssues(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 4 || parts[0] != "repos" {
		return false
	}
	full := parts[1] + "/" + parts[2]
	isIssueSub := len(parts) == 6 && parts[3] == "issues" && (parts[5] == "parent" || parts[5] == "comments")
	isLabel := (len(parts) == 4 && parts[3] == "labels" && r.Method == http.MethodPost) || (len(parts) == 5 && parts[3] == "labels" && r.Method == http.MethodGet)
	if !isIssueSub && !isLabel {
		return false
	}
	inst, ok := s.auth(w, r)
	if !ok {
		return true
	}
	s.mu.Lock()
	rp := s.repos[full]
	s.mu.Unlock()
	if rp == nil {
		s.write(w, r, inst, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return true
	}
	if isLabel {
		s.serveLabel(w, r, inst, rp, parts)
		return true
	}
	n, err := strconv.Atoi(parts[4])
	s.mu.Lock()
	if rp.extra == nil {
		rp.extra = map[int]*issueExtra{}
	}
	exists := err == nil && n >= 1 && n <= len(rp.issues)
	var x *issueExtra
	if exists {
		if rp.extra[n] == nil {
			rp.extra[n] = &issueExtra{}
		}
		x = rp.extra[n]
	}
	s.mu.Unlock()
	if !exists {
		s.write(w, r, inst, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return true
	}
	switch {
	case parts[5] == "parent" && r.Method == http.MethodGet:
		s.mu.Lock()
		p := x.parent
		var parent map[string]any
		if p >= 1 && p <= len(rp.issues) {
			parent = rp.issues[p-1]
		}
		s.mu.Unlock()
		if parent == nil {
			s.write(w, r, inst, http.StatusNotFound, map[string]any{"message": "Not Found"})
			return true
		}
		s.write(w, r, inst, http.StatusOK, parent)
	case parts[5] == "comments" && r.Method == http.MethodGet:
		s.mu.Lock()
		list := append(make([]map[string]any, 0, len(x.comments)), x.comments...)
		s.mu.Unlock()
		s.write(w, r, inst, http.StatusOK, list)
	case parts[5] == "comments" && r.Method == http.MethodPost:
		var in struct {
			Body string `json:"body"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Body == "" {
			s.write(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
			return true
		}
		s.mu.Lock()
		id := s.nextID
		s.nextID++
		c := map[string]any{"id": id, "body": in.Body, "user": map[string]any{"login": inst.app + "[bot]"}}
		x.comments = append(x.comments, c)
		s.actions = append(s.actions, Action{App: inst.app, Method: r.Method, Path: r.URL.Path})
		s.mu.Unlock()
		s.write(w, r, inst, http.StatusCreated, c)
	default:
		s.write(w, r, inst, http.StatusNotFound, map[string]any{"message": "Not Found"})
	}
	return true
}

func (s *Server) serveLabel(w http.ResponseWriter, r *http.Request, inst *installation, rp *repo, parts []string) {
	s.mu.Lock()
	if rp.labels == nil {
		rp.labels = map[string]string{}
	}
	s.mu.Unlock()
	if r.Method == http.MethodGet {
		s.mu.Lock()
		desc, found := rp.labels[parts[4]]
		s.mu.Unlock()
		if !found {
			s.write(w, r, inst, http.StatusNotFound, map[string]any{"message": "Not Found"})
			return
		}
		s.write(w, r, inst, http.StatusOK, map[string]any{"name": parts[4], "description": desc})
		return
	}
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Name == "" {
		s.write(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
		return
	}
	s.mu.Lock()
	_, taken := rp.labels[in.Name]
	if !taken {
		rp.labels[in.Name] = in.Description
		s.actions = append(s.actions, Action{App: inst.app, Method: r.Method, Path: r.URL.Path})
	}
	s.mu.Unlock()
	if taken {
		s.write(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed", "errors": []map[string]any{{"code": "already_exists"}}})
		return
	}
	s.write(w, r, inst, http.StatusCreated, map[string]any{"name": in.Name, "description": in.Description})
}
