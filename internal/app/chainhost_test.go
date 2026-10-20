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
