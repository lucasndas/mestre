package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/pkg/clock"
)

// IssueAPI is the GitHub the commands of issues use: what the guard reads and
// the writes. It is made for the App of who asks.
type IssueAPI interface {
	GitHub
	CreateIssue(ctx context.Context, repo, title, body string, labels []string) (IssueData, error)
	IssueData(ctx context.Context, repo string, n int) (IssueData, error)
	UpdateIssue(ctx context.Context, repo string, n int, e IssueEdit) error
	AddSubIssue(ctx context.Context, repo string, parent int, childID int64) error
	SubIssues(ctx context.Context, repo string, n int) ([]IssueData, error)
	Issues(ctx context.Context, repo, state string) ([]IssueData, error)
	AddLabels(ctx context.Context, repo string, n int, labels []string) error
	RemoveLabel(ctx context.Context, repo string, n int, name string) error
}

var _ IssueAPI = (*REST)(nil)

// IssueService is what `mestre issue` does on GitHub for a table
// (docs/cli/coordenacao-github.md). It is made per call, over the API of the
// App of the one who asks.
type IssueService struct {
	API     IssueAPI
	Objects *Objects
	Tables  Tables
	Clock   clock.Clock
}

func (s *IssueService) guard() *Guard { return &Guard{Objects: s.Objects, GH: s.API, Tables: s.Tables} }

// IssueCreate is `issue create`.
type IssueCreate struct {
	Title, Body string
	// Parent is the issue above (0: the epic of the table; the Grão-Mestre
	// with none makes a loose issue).
	Parent      int
	Labels      []string
	Fingerprint string
	// Op identifies the request: the same one again (a resending of the CLI)
	// finds the issue it made and makes no other. Empty: a new operation.
	Op string
}

// PartialError is an issue that exists on GitHub but whose setup did not end:
// it is the table's (or registered nowhere, Registered false), and the command
// must not be repeated.
type PartialError struct {
	// Kind is what was made ("issue" if empty).
	Kind       string
	Number     int
	Parent     int
	Registered bool
	Err        error
}

func (e *PartialError) Error() string {
	kind := e.Kind
	if kind == "" {
		kind = "issue"
	}
	return fmt.Sprintf("the %s #%d was created but its setup did not end: %v", kind, e.Number, e.Err)
}

func (e *PartialError) Unwrap() error { return e.Err }

// IssueCreated is what `issue create` answers.
type IssueCreated struct {
	Number int
	ID     int64
	URL    string
	Parent int
	// SeenAgain: an open issue had the same impression; nothing was created.
	SeenAgain bool
	// RecurrenceOf is the closed issue the new one comes back from.
	RecurrenceOf int
}

// ImpressionMarker is the mark of the impression in the body of an issue.
func ImpressionMarker(hash string) string { return "<!-- mestre:impressao=" + hash + " -->" }

// Impression is the hash of a problem text: lower case, blanks joined.
func Impression(text string) string {
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(strings.ToLower(text)), " ")))
	return hex.EncodeToString(sum[:])
}

// Create makes an issue as a sub-issue of the epic (or of Parent), the way the
// table is allowed to.
func (s *IssueService) Create(ctx context.Context, a Actor, repo string, in IssueCreate) (IssueCreated, error) {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return IssueCreated{}, refuse(CodeNotAllowed, "repositório inválido")
	}
	g := s.guard()
	parent := in.Parent
	table := ""
	op := in.Op
	if op == "" {
		op = NewOp()
	} else if obj, found, err := s.Objects.ByOp(ctx, op); err != nil {
		return IssueCreated{}, err
	} else if found {
		// a resending: the issue is already made
		if obj.Kind != KindIssue || obj.Repo != repo || (a.Kind == ActorTable && obj.Table != a.Table) {
			return IssueCreated{}, fmt.Errorf("github: the operation %s was made for something else", op)
		}
		return IssueCreated{Number: obj.Number, ID: obj.GitHubID, URL: fmt.Sprintf("https://github.com/%s/issues/%d", repo, obj.Number), Parent: obj.ParentNumber}, nil
	}
	if a.Kind == ActorTable {
		table = a.Table
		ref, ok, err := s.Tables.Epic(ctx, a.Table)
		if err != nil {
			return IssueCreated{}, err
		}
		if !ok {
			return IssueCreated{}, refuse(CodeNotAllowed, "a mesa %s não tem épico registrado", a.Table)
		}
		if parent == 0 {
			parent = ref.Epic
		}
	}
	if parent != 0 {
		if err := g.Authorize(ctx, a, Target{Repo: repo, Kind: KindIssue, Number: parent}); err != nil {
			return IssueCreated{}, err
		}
		if a.Kind != ActorTable {
			// the Grão-Mestre under an issue of a table makes the issue of that table
			if po, found, err := s.Objects.Get(ctx, repo, KindIssue, parent); err != nil {
				return IssueCreated{}, err
			} else if found {
				table = po.Table
			}
		}
	}
	if err := CheckLabels(a, in.Labels, nil); err != nil {
		return IssueCreated{}, err
	}
	labels := append([]string(nil), in.Labels...)
	hash, recurrence := "", 0
	if in.Fingerprint != "" {
		hash = Impression(in.Fingerprint)
		found, err := s.Objects.ByFingerprint(ctx, repo, hash)
		if err != nil {
			return IssueCreated{}, err
		}
		for _, o := range found {
			if a.Kind == ActorTable && o.Table != a.Table {
				continue // the impressions of the other tables are theirs
			}
			cur, err := s.API.IssueData(ctx, repo, o.Number)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return IssueCreated{}, err
			}
			if cur.State == "open" {
				if _, err := s.API.PostComment(ctx, repo, o.Number, WithMarker("Visto de novo em "+s.Clock.Now().UTC().Format("2006-01-02")+": "+oneLine(in.Title), NewOp())); err != nil {
					return IssueCreated{}, err
				}
				return IssueCreated{Number: o.Number, ID: cur.ID, URL: cur.URL, SeenAgain: true}, nil
			}
			if recurrence == 0 {
				recurrence = o.Number
			}
		}
		if recurrence != 0 {
			labels = append(labels, RecurrenceLabel)
		}
	}
	if err := g.EnsureLabels(ctx, repo, labels); err != nil {
		return IssueCreated{}, err
	}
	body := StripMarkers(in.Body)
	if recurrence != 0 {
		body = fmt.Sprintf("Recorrência de #%d\n\n%s", recurrence, body)
	}
	if hash != "" {
		body = strings.TrimRight(body, "\n") + "\n\n" + ImpressionMarker(hash)
	}
	made, err := s.API.CreateIssue(ctx, repo, in.Title, strings.TrimRight(body, "\n")+"\n\n"+Marker(op), labels)
	if err != nil {
		return IssueCreated{}, err
	}
	if err := s.Objects.Register(ctx, Object{Repo: repo, Kind: KindIssue, Number: made.Number, GitHubID: made.ID, Table: table, ParentNumber: parent, Op: op, Fingerprint: hash}); err != nil {
		return IssueCreated{}, &PartialError{Number: made.Number, Parent: parent, Err: err}
	}
	if parent != 0 {
		if err := s.API.AddSubIssue(ctx, repo, parent, made.ID); err != nil {
			return IssueCreated{}, &PartialError{Number: made.Number, Parent: parent, Registered: true, Err: err}
		}
	}
	return IssueCreated{Number: made.Number, ID: made.ID, URL: made.URL, Parent: parent, RecurrenceOf: recurrence}, nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// IssueUpdate is `issue update`; an empty field is left as it is.
type IssueUpdate struct {
	Title, Body            *string
	AddLabels, RemoveLabel []string
	State, Reason          string
}

// Update changes an issue of the table.
func (s *IssueService) Update(ctx context.Context, a Actor, repo string, n int, in IssueUpdate) (IssueData, error) {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return IssueData{}, refuse(CodeNotAllowed, "repositório inválido")
	}
	g := s.guard()
	if err := g.Authorize(ctx, a, Target{Repo: repo, Kind: KindIssue, Number: n}); err != nil {
		return IssueData{}, err
	}
	// An issue and a PR share the numbers, but a PR is not changed here: its
	// body has the blocks and the head that `pr update` keeps.
	if obj, found, err := s.Objects.Get(ctx, repo, KindIssue, n); err != nil {
		return IssueData{}, err
	} else if found && obj.Kind == KindPR {
		return IssueData{}, refuse(CodeNotAllowed, "#%d é um PR, não uma issue; use mestre pr", n)
	}
	if err := CheckLabels(a, in.AddLabels, in.RemoveLabel); err != nil {
		return IssueData{}, err
	}
	if err := g.EnsureLabels(ctx, repo, in.AddLabels); err != nil {
		return IssueData{}, err
	}
	edit := IssueEdit{Title: in.Title, StateReason: in.Reason}
	if in.Body != nil {
		// the marks of the engine (the operation, the impression) stay
		cur, err := s.API.IssueData(ctx, repo, n)
		if err != nil {
			return IssueData{}, err
		}
		b := strings.TrimRight(StripMarkers(*in.Body), "\n") + strings.Join(Markers(cur.Body), "")
		edit.Body = &b
	}
	if in.State != "" {
		st := in.State
		edit.State = &st
	}
	if edit.Title != nil || edit.Body != nil || edit.State != nil {
		if err := s.API.UpdateIssue(ctx, repo, n, edit); err != nil {
			return IssueData{}, err
		}
	}
	// the labels are added and taken one by one: a label a person put
	// meanwhile is not lost with a list that replaces the others
	if len(in.AddLabels) > 0 {
		if err := s.API.AddLabels(ctx, repo, n, in.AddLabels); err != nil {
			return IssueData{}, err
		}
	}
	for _, l := range in.RemoveLabel {
		if err := s.API.RemoveLabel(ctx, repo, n, l); err != nil {
			return IssueData{}, err
		}
	}
	return s.API.IssueData(ctx, repo, n)
}

// Comment posts a comment on an issue of the table, once for the operation.
func (s *IssueService) Comment(ctx context.Context, a Actor, repo string, n int, body, op string) (CommentInfo, error) {
	if op == "" {
		op = NewOp()
	}
	return s.guard().PostOnce(ctx, a, repo, n, body, op)
}

// ListTree lists the issues under the epic, the epic first (up to 500, 8 levels).
func (s *IssueService) ListTree(ctx context.Context, repo string, epic int) ([]IssueNode, error) {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return nil, refuse(CodeNotAllowed, "repositório inválido")
	}
	root, err := s.API.IssueData(ctx, repo, epic)
	if err != nil {
		return nil, err
	}
	out := []IssueNode{{IssueData: root}}
	type item struct{ n, depth int }
	queue := []item{{epic, 0}}
	for len(queue) > 0 && len(out) < 500 {
		it := queue[0]
		queue = queue[1:]
		if it.depth >= maxClimb {
			continue
		}
		kids, err := s.API.SubIssues(ctx, repo, it.n)
		if err != nil {
			return nil, err
		}
		for _, k := range kids {
			out = append(out, IssueNode{IssueData: k, Parent: it.n})
			queue = append(queue, item{k.Number, it.depth + 1})
		}
	}
	return out, nil
}

// IssueNode is an issue in the tree of an epic.
type IssueNode struct {
	IssueData
	Parent int
}
