package fakegithub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The issues API the guard of the actions uses: the parent of a sub-issue, the
// comments, the labels of a repository. The state is set from the test with
// AddIssue, SetParent and LabelIssue.

type issueExtra struct {
	parent   int
	comments []map[string]any
	// children are the sub-issues, in the order they were added.
	children []int
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
	now := s.clk.Now().UTC().Format(time.RFC3339)
	rp.issues = append(rp.issues, map[string]any{"id": id, "number": n, "title": title, "state": "open", "labels": ls, "user": map[string]any{"login": "lucas"},
		"created_at": now, "updated_at": now, "html_url": fmt.Sprintf("https://github.com/%s/issues/%d", full, n)})
	return n
}

// AddPR creates a PR (it is an issue too, as on GitHub) from head in headRepo
// and returns its number.
func (s *Server) AddPR(full, title, head, headRepo string) int {
	n := s.AddIssue(full, title)
	s.mu.Lock()
	defer s.mu.Unlock()
	rp := s.repos[full]
	if rp.pulls == nil {
		rp.pulls = map[int][2]string{}
	}
	rp.pulls[n] = [2]string{head, headRepo}
	rp.issues[n-1]["pull_request"] = map[string]any{"merged_at": nil}
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
	if rp.extra[parent] == nil {
		rp.extra[parent] = &issueExtra{}
	}
	rp.extra[parent].children = append(rp.extra[parent].children, child)
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
	if s.serveEvents(w, r, parts) {
		return true
	}
	full := parts[1] + "/" + parts[2]
	isIssueSub := len(parts) == 6 && parts[3] == "issues" && (parts[5] == "parent" || parts[5] == "comments" || parts[5] == "sub_issues" || parts[5] == "labels" && r.Method == http.MethodPost)
	if len(parts) == 5 && parts[3] == "issues" && r.Method == http.MethodPatch {
		return s.patchIssue(w, r, full, parts[4])
	}
	if len(parts) == 7 && parts[3] == "issues" && parts[5] == "labels" && r.Method == http.MethodDelete {
		return s.removeIssueLabel(w, r, full, parts[4], parts[6])
	}
	if parts[3] == "pulls" || parts[3] == "commits" {
		return s.servePulls(w, r, full, parts)
	}
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
		s.mu.Lock()
		fail, after := s.failWrite, s.failWriteAfter
		s.failWrite = 0
		s.mu.Unlock()
		if fail != 0 {
			w.Header().Set("Retry-After", after)
			s.write(w, r, inst, fail, map[string]any{"message": "You have exceeded a secondary rate limit"})
			return true
		}
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
		c := s.commentView(id, full, n, in.Body, inst.app+"[bot]")
		x.comments = append(x.comments, c)
		s.touch(rp, n)
		s.actions = append(s.actions, Action{App: inst.app, Method: r.Method, Path: r.URL.Path})
		s.mu.Unlock()
		s.write(w, r, inst, http.StatusCreated, c)
	case parts[5] == "labels" && r.Method == http.MethodPost:
		var in struct {
			Labels []string `json:"labels"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Labels) == 0 {
			s.write(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
			return true
		}
		s.mu.Lock()
		issue := rp.issues[n-1]
		cur := labelNames(issue)
		for _, l := range in.Labels {
			if !containsFold(cur, l) {
				cur = append(cur, l)
			}
		}
		issue["labels"] = labelMaps(cur)
		s.actions = append(s.actions, Action{App: inst.app, Method: r.Method, Path: r.URL.Path})
		out := issue["labels"]
		s.mu.Unlock()
		s.write(w, r, inst, http.StatusOK, out)
	case parts[5] == "sub_issues" && r.Method == http.MethodGet:
		s.mu.Lock()
		list := make([]map[string]any, 0, len(x.children))
		for _, c := range x.children {
			list = append(list, rp.issues[c-1])
		}
		s.mu.Unlock()
		// GitHub pages the list: page (from 1) of per_page (at most 100).
		per, page := 30, 1
		if v, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil && v > 0 {
			per = min(v, 100)
		}
		if v, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && v > 0 {
			page = v
		}
		lo := min((page-1)*per, len(list))
		s.write(w, r, inst, http.StatusOK, list[lo:min(lo+per, len(list))])
	case parts[5] == "sub_issues" && r.Method == http.MethodPost:
		s.mu.Lock()
		fail := s.subIssueFail
		s.subIssueFail = 0
		s.mu.Unlock()
		if fail != 0 {
			s.write(w, r, inst, fail, map[string]any{"message": "failed"})
			return true
		}
		var in struct {
			ID int64 `json:"sub_issue_id"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.ID == 0 {
			s.write(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
			return true
		}
		s.mu.Lock()
		child := 0
		for i, is := range rp.issues {
			if id, _ := is["id"].(int64); id == in.ID {
				child = i + 1
			}
		}
		if child == 0 {
			s.mu.Unlock()
			s.write(w, r, inst, http.StatusNotFound, map[string]any{"message": "Not Found"})
			return true
		}
		// GitHub: an issue has one parent, is not its own, and a parent has at
		// most 100 sub-issues.
		if child == n || (rp.extra[child] != nil && rp.extra[child].parent != 0) || len(x.children) >= 100 {
			s.mu.Unlock()
			s.write(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
			return true
		}
		if rp.extra[child] == nil {
			rp.extra[child] = &issueExtra{}
		}
		rp.extra[child].parent = n
		x.children = append(x.children, child)
		s.actions = append(s.actions, Action{App: inst.app, Method: r.Method, Path: r.URL.Path})
		parent := rp.issues[n-1]
		s.mu.Unlock()
		s.write(w, r, inst, http.StatusCreated, parent)
	default:
		s.write(w, r, inst, http.StatusNotFound, map[string]any{"message": "Not Found"})
	}
	return true
}

// patchIssue updates the title, the body, the labels and the state of an issue.
func (s *Server) patchIssue(w http.ResponseWriter, r *http.Request, full, num string) bool {
	inst, ok := s.auth(w, r)
	if !ok {
		return true
	}
	n, err := strconv.Atoi(num)
	s.mu.Lock()
	rp := s.repos[full]
	var issue map[string]any
	if rp != nil && err == nil && n >= 1 && n <= len(rp.issues) {
		issue = rp.issues[n-1]
	}
	s.mu.Unlock()
	if issue == nil {
		s.write(w, r, inst, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return true
	}
	var in struct {
		Title       *string   `json:"title"`
		Body        *string   `json:"body"`
		Labels      *[]string `json:"labels"`
		State       *string   `json:"state"`
		StateReason *string   `json:"state_reason"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		s.write(w, r, inst, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
		return true
	}
	s.mu.Lock()
	if in.Title != nil {
		issue["title"] = *in.Title
	}
	if in.Body != nil {
		issue["body"] = *in.Body
	}
	if in.Labels != nil {
		ls := make([]map[string]any, 0, len(*in.Labels))
		for _, l := range *in.Labels {
			ls = append(ls, map[string]any{"name": l})
		}
		issue["labels"] = ls
	}
	if in.State != nil {
		issue["state"] = *in.State
	}
	if in.StateReason != nil {
		issue["state_reason"] = *in.StateReason
	}
	s.actions = append(s.actions, Action{App: inst.app, Method: r.Method, Path: r.URL.Path})
	s.mu.Unlock()
	s.write(w, r, inst, http.StatusOK, issue)
	return true
}

func labelNames(issue map[string]any) []string {
	var out []string
	switch ls := issue["labels"].(type) {
	case []map[string]any:
		for _, l := range ls {
			out = append(out, l["name"].(string))
		}
	case []any:
		for _, l := range ls {
			if m, ok := l.(map[string]any); ok {
				out = append(out, m["name"].(string))
			}
		}
	}
	return out
}

func labelMaps(names []string) []map[string]any {
	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]any{"name": n})
	}
	return out
}

func containsFold(l []string, v string) bool {
	for _, x := range l {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// removeIssueLabel is DELETE /repos/{r}/issues/{n}/labels/{name}.
func (s *Server) removeIssueLabel(w http.ResponseWriter, r *http.Request, full, num, name string) bool {
	inst, ok := s.auth(w, r)
	if !ok {
		return true
	}
	n, err := strconv.Atoi(num)
	s.mu.Lock()
	rp := s.repos[full]
	var issue map[string]any
	if rp != nil && err == nil && n >= 1 && n <= len(rp.issues) {
		issue = rp.issues[n-1]
	}
	if issue == nil {
		s.mu.Unlock()
		s.write(w, r, inst, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return true
	}
	var left []string
	found := false
	for _, l := range labelNames(issue) {
		if strings.EqualFold(l, name) {
			found = true
			continue
		}
		left = append(left, l)
	}
	if !found {
		s.mu.Unlock()
		s.write(w, r, inst, http.StatusNotFound, map[string]any{"message": "Label does not exist"})
		return true
	}
	issue["labels"] = labelMaps(left)
	s.actions = append(s.actions, Action{App: inst.app, Method: r.Method, Path: r.URL.Path})
	s.mu.Unlock()
	s.write(w, r, inst, http.StatusOK, issue["labels"])
	return true
}

// FailNextSubIssue makes the next link of a sub-issue answer with the status.
func (s *Server) FailNextSubIssue(status int) {
	s.mu.Lock()
	s.subIssueFail = status
	s.mu.Unlock()
}

// Issue is the issue as the double keeps it (title, body, state, labels).
func (s *Server) Issue(full string, n int) (title, body, state string, labels []string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rp := s.repos[full]
	if rp == nil || n < 1 || n > len(rp.issues) {
		return
	}
	is := rp.issues[n-1]
	title, _ = is["title"].(string)
	body, _ = is["body"].(string)
	state, _ = is["state"].(string)
	switch ls := is["labels"].(type) {
	case []map[string]any:
		for _, l := range ls {
			labels = append(labels, l["name"].(string))
		}
	case []any:
		for _, l := range ls {
			if m, ok := l.(map[string]any); ok {
				labels = append(labels, m["name"].(string))
			}
		}
	}
	return title, body, state, labels, true
}

// SetIssueState opens or closes an issue as a person would.
func (s *Server) SetIssueState(full string, n int, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repos[full].issues[n-1]["state"] = state
	s.touch(s.repos[full], n)
}

// CloseIssueAs closes an issue with the reason GitHub gives ("completed" or
// "not_planned") and the instant of the change.
func (s *Server) CloseIssueAs(full string, n int, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	issue := s.repos[full].issues[n-1]
	issue["state"], issue["state_reason"] = "closed", reason
	issue["closed_at"] = s.clk.Now().UTC().Format(time.RFC3339)
	s.touch(s.repos[full], n)
}

// Parent is the parent of a sub-issue (0: none).
func (s *Server) Parent(full string, n int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if x := s.repos[full].extra[n]; x != nil {
		return x.parent
	}
	return 0
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

// AddIssueLabel puts a label on an issue as a person would.
func (s *Server) AddIssueLabel(full string, n int, label string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	is := s.repos[full].issues[n-1]
	is["labels"] = labelMaps(append(labelNames(is), label))
}

// RemoveIssueLabel takes a label off an issue or PR as a person would, with no
// App and no recorded action.
func (s *Server) RemoveIssueLabel(full string, n int, label string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	is := s.repos[full].issues[n-1]
	var left []string
	for _, l := range labelNames(is) {
		if !strings.EqualFold(l, label) {
			left = append(left, l)
		}
	}
	is["labels"] = labelMaps(left)
}
