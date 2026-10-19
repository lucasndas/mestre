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
}

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
	op := NewOp()
	body := StripMarkers(in.Body)
	if recurrence != 0 {
		body = fmt.Sprintf("Recorrência de #%d\n\n%s", recurrence, body)
	}
	if hash != "" {
		body = strings.TrimRight(body, "\n") + "\n\n" + ImpressionMarker(hash)
	}
	made, err := s.API.CreateIssue(ctx, repo, in.Title, WithMarker(body, op), labels)
	if err != nil {
		return IssueCreated{}, err
	}
	if err := s.Objects.Register(ctx, Object{Repo: repo, Kind: KindIssue, Number: made.Number, GitHubID: made.ID, Table: table, ParentNumber: parent, Op: op, Fingerprint: hash}); err != nil {
		return IssueCreated{}, err
	}
	if parent != 0 {
		if err := s.API.AddSubIssue(ctx, repo, parent, made.ID); err != nil {
			return IssueCreated{}, fmt.Errorf("the issue #%d was created but not linked under #%d: %w", made.Number, parent, err)
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
	if err := CheckLabels(a, in.AddLabels, in.RemoveLabel); err != nil {
		return IssueData{}, err
	}
	if err := g.EnsureLabels(ctx, repo, in.AddLabels); err != nil {
		return IssueData{}, err
	}
	edit := IssueEdit{Title: in.Title, StateReason: in.Reason}
	if in.Body != nil {
		b := StripMarkers(*in.Body)
		edit.Body = &b
	}
	if in.State != "" {
		st := in.State
		edit.State = &st
	}
	if len(in.AddLabels)+len(in.RemoveLabel) > 0 {
		cur, err := s.API.IssueData(ctx, repo, n)
		if err != nil {
			return IssueData{}, err
		}
		var next []string
		drop := func(l string) bool {
			for _, r := range in.RemoveLabel {
				if strings.EqualFold(strings.TrimSpace(r), l) {
					return true
				}
			}
			return false
		}
		for _, l := range append(cur.Labels, in.AddLabels...) {
			dup := false
			for _, x := range next {
				dup = dup || strings.EqualFold(x, l)
			}
			if !dup && !drop(l) {
				next = append(next, l)
			}
		}
		edit.Labels = &next
	}
	if err := s.API.UpdateIssue(ctx, repo, n, edit); err != nil {
		return IssueData{}, err
	}
	return s.API.IssueData(ctx, repo, n)
}

// Comment posts a comment on an issue of the table, once for the operation.
func (s *IssueService) Comment(ctx context.Context, a Actor, repo string, n int, body string) (CommentInfo, error) {
	return s.guard().PostOnce(ctx, a, repo, n, body, NewOp())
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
