package providers_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/internal/testkit/fakeagent"
	"github.com/lucasndas/mestre/pkg/clock"
)

// agyHooks answers like the dispatcher with an engine that allows everything
// and records the commands the fake agent ran.
type agyHooks struct {
	mu    sync.Mutex
	calls map[string]string // event -> command
}

func (h *agyHooks) RunHook(_ context.Context, c fakeagent.HookCall) (fakeagent.HookResult, error) {
	h.mu.Lock()
	h.calls[c.Event] = c.Command
	h.mu.Unlock()
	if c.Event == "PreToolUse" {
		return fakeagent.HookResult{Stdout: []byte(`{"decision":"allow"}`)}, nil
	}
	return fakeagent.HookResult{Stdout: []byte("{}")}, nil
}

// What the adapter generates is what the fake agent, which reads the same
// files as agy, takes: the pre-trusted worktree opens without the trust
// screen, the launch finds the agent file, and the hooks.json is read with
// its five events and the generated commands.
func TestTheFilesOfTheAdapterAreTheOnesTheFakeAgentReads(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		wt, home := filepath.Join(root, "work"), filepath.Join(root, "home")
		testkit.WriteFiles(t, root, map[string]string{
			"work/.git/HEAD":   "ref: refs/heads/main\n",
			"work/script.yaml": "prompts:\n  - match: write\n    steps:\n      - edit: {path: out.txt, content: hi}\n",
			"home/.gemini/antigravity-cli/antigravity-oauth-token": "{}", // the fake's login marker, not a token
		})
		mestreMD := writeMestreMD(t, root, "Mestre layer.\n")
		a := agy(t, providers.Options{})
		prov := a
		binary := filepath.Join(root, "bin", "mestre")
		hooksFiles, err := prov.HookFiles(providers.HookFilesRequest{Root: wt, OS: runtime.GOOS, Binary: binary})
		if err != nil {
			t.Fatal(err)
		}
		_, err = prov.PreTrust(providers.PreTrustRequest{Profile: home, Worktree: wt, RepoRoot: wt, OS: runtime.GOOS})
		if err != nil {
			t.Fatal(err)
		}
		layer, err := prov.DeliverMestreLayer(providers.LayerRequest{AgentID: "a1", Worktree: wt, Path: mestreMD})
		if err != nil {
			t.Fatal(err)
		}
		profileFiles(t, hooksFiles, layer.Files)

		l, err := a.BuildLaunch(providers.LaunchRequest{
			Base:    providers.Base{Command: "agy", Env: []string{"FAKEAGENT_SCRIPT=script.yaml"}, Dir: wt},
			OS:      runtime.GOOS,
			AgentID: "a1", TableID: "3-login", Worktree: wt,
			Env: []string{"HOME=" + home},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := l.Verify(); err != nil {
			t.Fatalf("the agent file is not where the check looks: %v", err)
		}

		h := &agyHooks{calls: map[string]string{}}
		launcher := &fakeagent.MemLauncher{Clock: clock.Real{}, Hooks: h, Reporter: t}
		proc, err := launcher.Start(t.Context(), terminal.Spec{Path: l.Path, Args: l.Args, Env: l.Env, Dir: l.Dir})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = proc.Kill() }()
		mp := proc.(*fakeagent.MemProcess)
		synctest.Wait()
		if out := mp.Output(); strings.Contains(out, "trust") || !strings.HasSuffix(out, fakeagent.PromptMarker) {
			t.Fatalf("the pre-trusted worktree did not open straight to the prompt:\n%s", out)
		}
		if _, err := mp.PTY().Write([]byte("\x1b[200~write it\x1b[201~\r")); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()

		want := hookCommands(t, wt, binary)
		for _, event := range []string{"PreInvocation", "PreToolUse", "PostToolUse", "PostInvocation", "Stop"} {
			if h.calls[event] == "" || h.calls[event] != want[event] {
				t.Errorf("%s ran %q, want the generated %q", event, h.calls[event], want[event])
			}
		}
		if out := mp.Output(); strings.Contains(out, "blocked") {
			t.Errorf("a tool was blocked:\n%s", out)
		}
	})
}

// hookCommands are the commands HookFiles generated, by event.
func hookCommands(t *testing.T, worktree, binary string) map[string]string {
	t.Helper()
	prov := agy(t, providers.Options{})
	files, err := prov.HookFiles(providers.HookFilesRequest{Root: worktree, OS: runtime.GOOS, Binary: binary})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]map[string]json.RawMessage
	if err := json.Unmarshal([]byte(mergedM(t, files[0], "")), &doc); err != nil {
		t.Fatal(err)
	}
	type handler struct {
		Command string `json:"command"`
		Hooks   []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	out := map[string]string{}
	for event, raw := range doc["mestre"] {
		if event == "enabled" {
			continue
		}
		var hs []handler
		if err := json.Unmarshal(raw, &hs); err != nil || len(hs) != 1 {
			t.Fatalf("%s: %s, %v", event, raw, err)
		}
		out[event] = hs[0].Command
		if len(hs[0].Hooks) == 1 {
			out[event] = hs[0].Hooks[0].Command
		}
	}
	return out
}
