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

// CA-CODEX-WIN-01
//
// A class without internet: the network of the sandbox is on in both
// systems (on Linux the local channel needs it; on Windows the unelevated
// mode has no network barrier), so both report the gap of the native
// barrier. On Windows the sandbox mode is unelevated, given by the engine
// alone. The socket folder is never freed for writing, and the hosted search
// never runs.
func TestCodexNetworkOfAClassWithoutInternet(t *testing.T) {
	t.Parallel()
	for _, goos := range []string{"linux", "windows"} {
		p := providers.NativePolicy{AgentID: "a1", OS: goos, Worktree: "/srv/w", Offline: true}
		nc, err := providers.NewCodex(providers.Options{}).(providers.NativeTranslator).Native(p)
		if err != nil {
			t.Fatal(err)
		}
		// No folder is freed for writing, and the empty list is pinned.
		want := []string{"-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=true"}
		if goos == "windows" {
			want = append(want, "-c", `windows.sandbox="unelevated"`)
		}
		want = append(want, "-c", "sandbox_workspace_write.writable_roots=[]")
		if !slices.Equal(nc.Args, want) {
			t.Errorf("%s: args = %q, want %q", goos, nc.Args, want)
		}
		if !providers.CodexNoNativeNetwork(p) {
			t.Errorf("%s: a class without internet did not report the lack of a native network barrier", goos)
		}
		// The arguments the policy gives start the Codex of that system.
		if _, err := providers.NewCodex(providers.Options{}).BuildLaunch(providers.LaunchRequest{
			Base: providers.Base{Command: "codex"}, OS: goos, AgentID: "a1", Profile: "/p", ProviderArgs: nc.Args,
		}); err != nil {
			t.Errorf("%s: start with the native arguments: %v", goos, err)
		}
	}
	// With internet there is no gap to report.
	if providers.CodexNoNativeNetwork(providers.NativePolicy{AgentID: "a1", OS: "linux", Worktree: "/srv/w"}) {
		t.Error("a class with internet reported without the native network barrier")
	}
}

// Part of CA-CODEX-WIN-01
//
// The sandbox mode of Codex on Windows is the engine's: a Windows start
// without it is refused (the default mode shares one runner), no other mode
// is accepted (not elevated, not mxc, not a table or a bypass), and the
// base and the repository's config cannot set it.
func TestCodexWindowsSandboxModeIsReservedAndMandatory(t *testing.T) {
	t.Parallel()
	codex := providers.NewCodex(providers.Options{})
	sandbox := []string{"-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=true"}
	launch := func(os string, base, engine []string) error {
		_, err := codex.BuildLaunch(providers.LaunchRequest{Base: providers.Base{Command: "codex", Args: base}, OS: os, AgentID: "a1", Profile: "/p", ProviderArgs: engine})
		return err
	}
	if err := launch("windows", nil, sandbox); err == nil {
		t.Error("a Windows start without windows.sandbox was accepted")
	}
	if err := launch("windows", nil, append(slices.Clone(sandbox), "-c", `windows.sandbox="unelevated"`)); err != nil {
		t.Errorf("the unelevated mode was refused: %v", err)
	}
	for _, v := range []string{`"elevated"`, `"mxc"`, `elevated`, `"danger-full-access"`, `{mode="unelevated"}`, `"unelevated" # x`, `'unelevated'`, `""`} {
		if err := launch("windows", nil, append(slices.Clone(sandbox), "-c", "windows.sandbox="+v)); err == nil {
			t.Errorf("windows.sandbox=%s accepted", v)
		}
	}
	good := append(slices.Clone(sandbox), "-c", `windows.sandbox="unelevated"`)
	for _, b := range [][]string{
		{"-c", `windows.sandbox="elevated"`}, {"-c", `windows.sandbox="unelevated"`}, {"-cwindows.sandbox=\"elevated\""},
		{"--config", `windows.sandbox="elevated"`}, {"-c", `windows={sandbox="elevated"}`}, {"-c", `tui={windows={sandbox="elevated"}}`},
		{"-c", `windows.sandbox.mode="elevated"`}, {"-c", `windows="elevated"`},
	} {
		if err := launch("windows", b, good); err == nil {
			t.Errorf("base %q accepted", b)
		}
	}
}
