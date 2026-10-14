package providers_test

import (
	"errors"
	"slices"
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

// A class without internet: on Linux the network of the sandbox is on, for
// the `mestre` CLI to reach the local channel, and the gap of the native
// barrier is reported; on Windows it stays off unless the conformance
// switched its fallback on. The socket folder is never freed for writing,
// and the hosted search never runs.
func TestCodexNetworkOfAClassWithoutInternet(t *testing.T) {
	t.Parallel()
	tests := []struct {
		os       string
		fallback bool
		network  string
		gap      bool
	}{
		{"linux", false, "true", true},
		{"windows", false, "false", false},
		{"windows", true, "true", true},
	}
	for _, tt := range tests {
		p := providers.NativePolicy{AgentID: "a1", OS: tt.os, Worktree: "/srv/w", Offline: true}
		p.Fallbacks.CodexOfflineNetwork = tt.fallback
		nc, err := providers.NewCodex(providers.Options{}).(providers.NativeTranslator).Native(p)
		if err != nil {
			t.Fatal(err)
		}
		// No folder is freed for writing, and the empty list is pinned.
		want := []string{"-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=" + tt.network, "-c", "sandbox_workspace_write.writable_roots=[]"}
		if !slices.Equal(nc.Args, want) {
			t.Errorf("%s (fallback %v): args = %q, want %q", tt.os, tt.fallback, nc.Args, want)
		}
		if providers.CodexNoNativeNetwork(p) != tt.gap {
			t.Errorf("%s (fallback %v): no native network barrier = %v, want %v", tt.os, tt.fallback, !tt.gap, tt.gap)
		}
	}
	// With internet there is no gap to report.
	if providers.CodexNoNativeNetwork(providers.NativePolicy{AgentID: "a1", OS: "linux", Worktree: "/srv/w"}) {
		t.Error("a class with internet reported without the native network barrier")
	}
}
