package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Part of CA-CIDADELA-18: the libraries of classes are those of the main clone
// of each workspace, a name that is not a plain folder has none, and the
// approvals of a machine without a period of the Citadel are refused.
func TestTheLibrariesAndTheApprovalsOfTheMachineFollowThePeriod(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	layout := workspaces.Layout{Root: filepath.Join(root, "workspaces")}
	books := newClassBooks(nil, clock.Real{}, filepath.Join(root, "globais"), layout)
	lib := books.For("loja")
	if lib == nil || lib.Workspace != "loja" || lib.Roots.Workspace != filepath.Join(layout.Principal("loja"), ".mestre", "classes") || lib.Roots.Global != filepath.Join(root, "globais") {
		t.Fatalf("library = %+v", lib)
	}
	if books.For("loja") != lib {
		t.Error("the library is made again at each use")
	}
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`, "../x"} {
		if books.For(bad) != nil {
			t.Errorf("a workspace named %q has a library", bad)
		}
	}
	if newClassBooks(nil, clock.Real{}, "", layout).For("loja") != nil {
		t.Error("without the global folder a library was made")
	}

	e := &Engine{}
	if _, err := (periodApprovals{e}).Open(context.Background(), hook.ApprovalRequest{AgentID: "x"}); err == nil {
		t.Error("an approval was opened with no period of the Citadel")
	}
	if ok, err := (periodTableRules{e}).Use(context.Background(), classes.TableRuleUse{}); ok || err == nil {
		t.Errorf("a table rule let a call through with no period: %v %v", ok, err)
	}
}

func TestAMachineWithoutAPeriodSaysItOpensNoApprovals(t *testing.T) {
	t.Parallel()
	_, err := (periodApprovals{&Engine{}}).Open(context.Background(), hook.ApprovalRequest{})
	if !errors.Is(err, hook.ErrNoApprovals) {
		t.Fatalf("err = %v", err)
	}
}

type fakeTeller struct{ plain, scoped []string }

func (f *fakeTeller) OwnerAlert(_ context.Context, text string) { f.plain = append(f.plain, text) }
func (f *fakeTeller) OwnerWorkspaceAlert(_ context.Context, workspace, text string) {
	f.scoped = append(f.scoped, workspace+"|"+text)
}

type plainTeller struct{ got []string }

func (p *plainTeller) OwnerAlert(_ context.Context, text string) { p.got = append(p.got, text) }

// Part of CA-ROTA-06 (the header of each message is in internal/telegram): the
// alert of the chain about a workspace, the warning of quota among them, goes
// out as a message of that workspace, not as a Sistema one with the name in the text.
func TestTheAlertOfTheChainAboutAWorkspaceKeepsTheWorkspaceForTheHeader(t *testing.T) {
	t.Parallel()
	f := &fakeTeller{}
	if err := (chainOwnerAlerts{f}).Notify(t.Context(), "loja", "O limite passou de 80%."); err != nil {
		t.Fatal(err)
	}
	if len(f.plain) != 0 || len(f.scoped) != 1 || f.scoped[0] != "loja|O limite passou de 80%." {
		t.Errorf("plain %q scoped %q", f.plain, f.scoped)
	}
	// an alert of no workspace, or a teller that does not know workspaces, names it in the text
	if err := (chainOwnerAlerts{f}).Notify(t.Context(), "", "do motor"); err != nil || len(f.plain) != 1 {
		t.Errorf("no workspace: %v, %q", err, f.plain)
	}
	p := &plainTeller{}
	if err := (chainOwnerAlerts{p}).Notify(t.Context(), "loja", "x"); err != nil || len(p.got) != 1 || p.got[0] != "[loja] x" {
		t.Errorf("a teller with no workspaces: %v, %q", err, p.got)
	}
}
