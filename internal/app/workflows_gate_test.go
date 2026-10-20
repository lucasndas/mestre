package app_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// ciPerms is a class that may ask the CI to be changed (or never), and may use `pr update`.
func ciPerms(policy string) string {
	return fmt.Sprintf("version: 1\nfiles:\n  read: [\"**\"]\n  write: [\"**\"]\ncommands:\n  allow: [\"npm test\"]\n  default: ask\ngit:\n  commit: true\n  request_push: true\n  workflows: %s\ncli:\n  allow: [\"pr update\"]\n", policy)
}

// remoteTip is the commit of the branch of the table at the double of GitHub, "" before the first sending.
func (m *mm) remoteTip() string {
	repo, err := m.git.Open(m.w.ctx, m.remote)
	if err != nil {
		return ""
	}
	h, err := repo.ResolveRef(m.w.ctx, m.branch)
	if err != nil {
		return ""
	}
	return string(h)
}

// CA-GH-ID-05
func TestABranchThatChangesTheCIAsksForAnApprovalWithAskAndIsRefusedWithNever(t *testing.T) {
	t.Parallel()
	for _, policy := range []string{"ask", "never"} {
		t.Run(policy, func(t *testing.T) {
			t.Parallel()
			m := &mm{gate: true}
			w := newCommWorld(t, withCitadelClasses, m.setup)
			// the ceiling of the workspace lets the classes ask for the CI to change
			ceiling, err := classes.ParseCeiling([]byte("version: 1\npermissions:\n  git:\n    workflows: ask\n    request_push: true\n"))
			if err != nil {
				t.Fatal(err)
			}
			w.lib.Ceiling = commCeiling{ceiling}
			w.approveClass("dev", ciPerms(policy))
			dev := w.recruit("Dev-CI", "Dev Claude")
			// the branch of the table in the mirror of the workspace, with its work
			mirror, err := w.orc.Workspaces.OpenMirror(w.ctx, w.slug)
			if err != nil {
				t.Fatal(err)
			}
			m.mirror, m.branch = mirror.Dir(), w.table.Branch
			m.git.Handle(m.mirror).Data().Commit(m.branch, "feat: login", map[string]string{"src/login.ts": "export const timeout = 30\n"})
			if r := w.run(w.master, "pr", "open", "--title", "Login", "--json"); r.code != 0 {
				t.Fatalf("pr open = %+v", r)
			}
			sent := m.remoteTip()
			if sent == "" {
				t.Fatal("the first sending did not reach the double of GitHub")
			}
			// the branch of the table changes the CI
			m.git.Handle(m.mirror).Data().Commit(m.branch, "ci: muda o build", map[string]string{".github/workflows/ci.yml": "name: ci2\n"})

			r := w.run(dev, "pr", "update", "--json")
			v := decodeJSON(t, r)
			if got := m.remoteTip(); got != sent {
				t.Fatalf("the branch was sent although it changes the CI: the double of GitHub has %s, had %s", got, sent)
			}
			switch policy {
			case "ask":
				if r.code != 9 || v.Error.Code != "approval_pending" {
					t.Fatalf("pr update with ask = %d %+v, want exit 9 approval_pending", r.code, v.Error)
				}
				var id int64
				if _, err := fmt.Sscanf(v.Error.Message[strings.Index(v.Error.Message, "#")+1:], "%d", &id); err != nil || id == 0 {
					t.Fatalf("the answer names no request: %q", v.Error.Message)
				}
				req, err := w.orc.Chain.Get(w.ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if req.Type != "envio_workflows" || req.Requester != dev.ID || req.Workspace != w.slug {
					t.Fatalf("the request = %+v, want an envio_workflows of the character", req)
				}
				// asking again is the same question
				if again := decodeJSON(t, w.run(dev, "pr", "update", "--json")); !strings.Contains(again.Error.Message, fmt.Sprintf("#%d", id)) {
					t.Errorf("asking again opened another request: %q", again.Error.Message)
				}
				// the live target the approval is verified against is the request's while the head is the same
				live, err := w.orc.WorkflowsTarget(w.ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				if a, _ := chain.Impress(live); a != req.Impression {
					t.Fatalf("the live target %s is not the one of the request", live)
				}
				// approved by the Owner, the same command sends the branch
				if _, err := w.orc.Chain.DecideByPerson(w.ctx, chain.PersonDecision{Request: id, User: "u1", Level: "owner", Answer: chain.AnswerApprove}); err != nil {
					t.Fatal(err)
				}
				if r := w.run(dev, "pr", "update", "--json"); r.code != 0 {
					t.Fatalf("pr update after the approval = %+v", r)
				}
				if got := m.remoteTip(); got == sent {
					t.Fatal("the approved branch was not sent")
				}
				// a new commit after the approval is another question: the live target no longer matches
				m.git.Handle(m.mirror).Data().Commit(m.branch, "ci: outra mudança", map[string]string{".github/workflows/ci.yml": "name: ci3\n"})
				if live, err := w.orc.WorkflowsTarget(w.ctx, req); err != nil {
					t.Fatal(err)
				} else if a, _ := chain.Impress(live); a == req.Impression {
					t.Error("the live target did not change with the head")
				}
				tip := m.remoteTip()
				r = w.run(dev, "pr", "update", "--json")
				if v := decodeJSON(t, r); r.code != 9 || v.Error.Code != "approval_pending" || strings.Contains(v.Error.Message, fmt.Sprintf("#%d)", id)) {
					t.Fatalf("pr update after a new commit = %d %+v, want a new approval_pending", r.code, v.Error)
				}
				if m.remoteTip() != tip {
					t.Error("the new commit was sent with the approval of the old one")
				}
			case "never":
				if r.code != 3 || v.Error.Code != "workflow_not_allowed" {
					t.Fatalf("pr update with never = %d %+v, want exit 3 workflow_not_allowed", r.code, v.Error)
				}
				items, err := w.orc.Inbox.Store().All(w.ctx, w.gm.ID)
				if err != nil {
					t.Fatal(err)
				}
				var told bool
				for _, it := range items {
					told = told || it.Kind == inbox.KindSystem && strings.Contains(it.Text, "workflows") && strings.Contains(it.Text, "never")
				}
				if !told {
					t.Error("the Grand Master was not told that the branch was not sent")
				}
			}
		})
	}
}
