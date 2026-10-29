package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/pkg/clock"
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

func (f *fakeTeller) OwnerAlert(_ context.Context, m i18n.Msg) {
	f.plain = append(f.plain, i18n.Render(i18n.PtBR, m))
}
func (f *fakeTeller) OwnerWorkspaceAlert(_ context.Context, workspace string, m i18n.Msg) {
	f.scoped = append(f.scoped, workspace+"|"+i18n.Render(i18n.PtBR, m))
}

type plainTeller struct{ got []string }

func (p *plainTeller) OwnerAlert(_ context.Context, m i18n.Msg) {
	p.got = append(p.got, i18n.Render(i18n.PtBR, m))
}

// Part of CA-ROTA-06 (the header of each message is in internal/telegram): the
// alert of the chain about a workspace, the warning of quota among them, goes
// out as a message of that workspace, not as a Sistema one with the name in the text.
func TestTheAlertOfTheChainAboutAWorkspaceKeepsTheWorkspaceForTheHeader(t *testing.T) {
	t.Parallel()
	f := &fakeTeller{}
	if err := (chainOwnerAlerts{f}).Notify(t.Context(), "loja", i18n.Text("O limite passou de 80%.")); err != nil {
		t.Fatal(err)
	}
	if len(f.plain) != 0 || len(f.scoped) != 1 || f.scoped[0] != "loja|O limite passou de 80%." {
		t.Errorf("plain %q scoped %q", f.plain, f.scoped)
	}
	// an alert of no workspace, or a teller that does not know workspaces, names it in the text
	if err := (chainOwnerAlerts{f}).Notify(t.Context(), "", i18n.Text("do motor")); err != nil || len(f.plain) != 1 {
		t.Errorf("no workspace: %v, %q", err, f.plain)
	}
	p := &plainTeller{}
	if err := (chainOwnerAlerts{p}).Notify(t.Context(), "loja", i18n.Text("x")); err != nil || len(p.got) != 1 || p.got[0] != "[loja] x" {
		t.Errorf("a teller with no workspaces: %v, %q", err, p.got)
	}
}

// chanTeller hands each alert to the test over a channel: the production
// callback sends the alert in its own goroutine.
type chanTeller struct{ got chan string }

func (c chanTeller) OwnerAlert(_ context.Context, m i18n.Msg) { c.got <- i18n.Render(i18n.PtBR, m) }

// ownerHearsOfThePolicyFile writes the content as the policy file of the
// workspace "loja", reads it through the production policyFileFor and returns
// the one message the Owner gets.
func ownerHearsOfThePolicyFile(t *testing.T, content string) string {
	t.Helper()
	layout := workspaces.Layout{Root: filepath.Join(t.TempDir(), "workspaces")}
	dir := filepath.Join(layout.Principal("loja"), ".mestre", "regras")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "politica-de-aprovacao.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	tell := chanTeller{got: make(chan string, 4)}
	pf := policyFileFor(layout, tell, nil, nil)
	if _, v, err := pf.Chain(t.Context(), "loja"); err != nil || v != "default" {
		t.Fatalf("Chain = %q, %v; want the default content to hold", v, err)
	}
	if _, _, err := pf.Chain(t.Context(), "loja"); err != nil { // the same content is told once
		t.Fatal(err)
	}
	msg := <-tell.got
	if len(tell.got) != 0 {
		t.Fatalf("the Owner got more than one message: %q and %d more", msg, len(tell.got))
	}
	if !strings.Contains(msg, "loja") || !strings.Contains(msg, "última versão válida") {
		t.Fatalf("message = %q, want it to name the workspace and say the last valid version holds", msg)
	}
	return msg
}

// CA-APROV-POL-04 (the Owner is told, through the production path, of a file with a syntax error)
func TestTheOwnerIsToldOfAPolicyFileWithASyntaxError(t *testing.T) {
	t.Parallel()
	ownerHearsOfThePolicyFile(t, "versao: 1\ncondicoes: [oops\n")
}

// CA-APROV-POL-16 (the Owner is told, through the production path, why the file is invalid)
func TestTheOwnerIsToldWhyAPolicyFileThatLetsADestructiveActionBeDecidedAloneIsInvalid(t *testing.T) {
	t.Parallel()
	msg := ownerHearsOfThePolicyFile(t, "versao: 1\ntipos:\n  acao_destrutiva: { sozinho: se_condicoes }\n")
	if !strings.Contains(msg, "acao_destrutiva") || !strings.Contains(msg, "risco alto não se decide sozinho") {
		t.Fatalf("message = %q, want the reason: acao_destrutiva does not take sozinho", msg)
	}
}
