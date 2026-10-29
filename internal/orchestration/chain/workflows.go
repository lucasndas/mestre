package chain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/i18n"
)

// A branch that changes .github/workflows/ is sent by a class with
// `git.workflows: ask` only after a user approved it (docs/github/identidades-e-apps.md#workflows):
// the first try opens a request of type envio_workflows bound to the repository,
// the branch and the head that would go, and answers "pending"; the try after the
// approval, with the head unchanged, is let through. The approval is for that
// head only: a new commit invalidates it.

// WorkflowsAsk is the sending of a branch that changes the workflows.
type WorkflowsAsk struct {
	// Requester is the character that runs the command (the Master of the table).
	Requester string
	// Repo is the repository of the workspace of the Requester, never one the
	// caller names; Branch and Head are what would go.
	Repo, Branch, Head string
	// About is the line the people read.
	About string
	// AboutMsg is the line as a message of the engine, when it has words.
	AboutMsg i18n.Msg
}

// WorkflowsTarget is the target a request of envio_workflows is bound to.
type WorkflowsTarget struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	Head   string `json:"head"`
}

// JSON is the target in the form the impression is made from.
func (t WorkflowsTarget) JSON() json.RawMessage {
	b, _ := json.Marshal(t)
	return b
}

// WorkflowsFingerprint finds the requests for the same branch of the same
// repository, whatever its head.
func WorkflowsFingerprint(workspace, repo, branch string) string {
	h := sha256.Sum256([]byte(workspace + "\x00" + repo + "\x00" + branch))
	return "workflows:" + hex.EncodeToString(h[:])
}

// ErrNoWorkflows: the ask is incomplete.
var ErrNoWorkflows = errors.New("chain: the sending of workflows needs the character, the repository, the branch and the head")

// Workflows says whether the sending of the branch at the head was approved
// (granted), or leaves a request for it open and says which. An approval holds for
// that head only, within the time of the people after the decision; a request left
// open for another head is invalidated and asked again.
func (s *Service) Workflows(ctx context.Context, a WorkflowsAsk) (granted bool, request int64, err error) {
	if a.Requester == "" || a.Repo == "" || a.Branch == "" || a.Head == "" {
		return false, 0, ErrNoWorkflows
	}
	c, err := s.o.Characters.Get(ctx, a.Requester)
	if err != nil {
		return false, 0, err
	}
	fp := WorkflowsFingerprint(c.Workspace, a.Repo, a.Branch)
	target := WorkflowsTarget{Repo: a.Repo, Branch: a.Branch, Head: a.Head}.JSON()
	imp, err := Impress(target)
	if err != nil {
		return false, 0, err
	}
	st, _ := s.settings(ctx, c.Workspace)
	s.dmu.Lock()
	granted, id, err := s.workflowsApproved(ctx, c.ID, fp, imp, st)
	s.dmu.Unlock()
	if err != nil || granted {
		return granted, id, err
	}
	return s.workflowsAsk(ctx, a, c.Workspace, c.Table, c.ID, fp, imp, target)
}

// workflowsApproved looks for an approval that holds for this head, under dmu.
func (s *Service) workflowsApproved(ctx context.Context, character, fp, imp string, st Settings) (bool, int64, error) {
	approved, err := s.st.ApprovedFor(ctx, character, fp)
	if err != nil {
		return false, 0, err
	}
	for _, r := range approved {
		if r.Spent || r.Type != classes.RequestWorkflows {
			continue
		}
		if r.ClosedAt.IsZero() || !s.clk.Now().Before(r.ClosedAt.Add(st.peopleDeadline(r.Type))) {
			continue // the approval was not used in time
		}
		if r.Impression != imp {
			// another head than the approved one: the approval is over
			r.State, r.Spent = Invalidated, true
			if err := s.st.Save(ctx, r); err != nil {
				return false, 0, err
			}
			s.audit(ctx, r, AuditEntry{Level: LevelPeople, Answer: "invalidate", Outcome: "accepted", Reason: "a cabeça da branch mudou antes de o envio acontecer"})
			continue
		}
		return true, r.ID, nil
	}
	return false, 0, nil
}

// workflowsAsk leaves the question open for the head, or finds the one open.
func (s *Service) workflowsAsk(ctx context.Context, a WorkflowsAsk, workspace, table, character, fp, imp string, target json.RawMessage) (bool, int64, error) {
	if open, ok, err := s.st.FindOpen(ctx, character, fp); err != nil {
		return false, 0, err
	} else if ok {
		if open.Impression == imp {
			return false, open.ID, nil
		}
		if err := s.Invalidate(ctx, open.ID, "a cabeça da branch mudou antes de o envio ser aprovado"); err != nil && !IsNoRequest(err) {
			return false, 0, err
		}
	}
	id, err := s.Request(ctx, OpenRequest{Type: classes.RequestWorkflows, Workspace: workspace, Table: table, Requester: a.Requester,
		AboutMsg: i18n.M("approval.about_workflows", "branch", a.Branch, "head", fmt.Sprintf("%.8s", a.Head), "about", workflowsAbout(a)), Target: target, Fingerprint: fp})
	return false, id, err
}

// WorkflowsPolicy is git.workflows in the effective rules of the character (the
// class limited by the ceiling of the workspace): what decides whether a branch
// that changes the CI is sent at all. A policy the engine does not know, or rules
// it cannot read, are never.
func (s *Service) WorkflowsPolicy(ctx context.Context, characterID string) (classes.Workflows, error) {
	if s.o.Classes == nil {
		return classes.WorkflowsNever, errors.New("chain: no classes to read the rules from")
	}
	r, err := s.o.Classes.Rules(ctx, characterID)
	if err != nil || r == nil {
		return classes.WorkflowsNever, err
	}
	switch p := r.Git.Workflows; p {
	case classes.WorkflowsAsk, classes.WorkflowsAllow:
		return p, nil
	}
	return classes.WorkflowsNever, nil
}

func workflowsAbout(a WorkflowsAsk) any {
	if !a.AboutMsg.IsZero() {
		return a.AboutMsg
	}
	return oneLine(a.About)
}
