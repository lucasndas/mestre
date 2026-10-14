package providers_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// CA-CODEX-05
func TestCodexStopAndInterruptAnswerNothingWithExitZero(t *testing.T) {
	t.Parallel()
	a := codexAdapter(providers.Options{})
	for _, native := range []string{"Stop", "Interrupt"} {
		for _, r := range []providers.Reply{
			{Kind: providers.ReplyNeutral},
			{Kind: providers.ReplyAllow},
			{Kind: providers.ReplyDeny, Reason: "x"},
			{Kind: providers.ReplyContext, Context: "contexto do turno"},
			{Kind: providers.ReplyError, Reason: "engine unavailable"},
		} {
			if out := a.FormatResponse(native, r); len(out.Stdout) != 0 || out.Exit != 0 {
				t.Errorf("%s %v: stdout %q, exit %d; want nothing and 0", native, r.Kind, out.Stdout, out.Exit)
			}
		}
	}
}

func TestMultilineFallbackSendsByTheFile(t *testing.T) {
	t.Parallel()
	text := "linha 1\nlinha 2"
	if b, err := codexAdapter(providers.Options{}).TypeMessage(text); err != nil || string(b) != "\x1b[200~linha 1\nlinha 2\x1b[201~\r" {
		t.Fatalf("default = %q, %v", b, err)
	}
	fb := codexAdapter(providers.Options{CodexMultilineViaFile: true})
	if _, err := fb.TypeMessage(text); !errors.Is(err, providers.ErrTooLong) {
		t.Fatalf("fallback = %v, want ErrTooLong (the long-message file)", err)
	}
	if b, err := fb.TypeMessage("uma linha\n"); err != nil || len(b) == 0 {
		t.Fatalf("one line with the fallback = %q, %v", b, err)
	}
}

func TestCodexNetworkOfAClassWithoutInternet(t *testing.T) {
	t.Parallel()
	p := providers.NativePolicy{AgentID: "a1", Worktree: "/srv/w", Offline: true}
	nc, err := providers.NewCodex(providers.Options{}).(providers.NativeTranslator).Native(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range nc.Args {
		if a == "sandbox_workspace_write.network_access=true" || strings.HasPrefix(a, "sandbox_workspace_write.writable_roots") {
			t.Fatalf("args = %q: the default keeps the network off and never frees the socket folder for writing", nc.Args)
		}
	}
	if providers.CodexNoNativeNetwork(p) {
		t.Fatal("the default has the native network barrier")
	}
	p.Fallbacks.CodexOfflineNetwork = true
	nc, _ = providers.NewCodex(providers.Options{}).(providers.NativeTranslator).Native(p)
	want := []string{"-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=true"}
	if len(nc.Args) != len(want) || nc.Args[3] != want[3] || !providers.CodexNoNativeNetwork(p) {
		t.Fatalf("fallback args = %q; want the network on, no --search, and the gap reported", nc.Args)
	}
}
