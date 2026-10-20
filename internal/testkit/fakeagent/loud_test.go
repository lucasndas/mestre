package fakeagent_test

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/internal/testkit/fakeagent"
	"github.com/lucasndas/mestre/pkg/clock"
)

// claudeEnv is a logged-in, pre-trusted Claude profile for e.
func claudeEnv(t *testing.T) env {
	t.Helper()
	cfgDir := t.TempDir()
	e := newEnv(t, "CLAUDE_CONFIG_DIR="+cfgDir)
	testkit.WriteFiles(t, cfgDir, map[string]string{
		".credentials.json": "{}",
		".claude.json":      fmt.Sprintf(`{"projects": {%q: {"hasTrustDialogAccepted": true}}}`, filepath.ToSlash(e.dir)),
	})
	return e
}

// What the fake does not model fails the test instead of being skipped
// (docs/qualidade/dubles.md#regras-e-erros).
func TestUnknownHookEventOrTypeFailsTheTest(t *testing.T) {
	t.Parallel()
	const (
		claudeFile = ".claude/settings.local.json"
		agyFile    = ".agents/hooks.json"
	)
	cases := []struct {
		name, program, file, content, want string
	}{
		{"claude event the fake does not fire", "claude", claudeFile,
			`{"hooks": {"SubagentStop": [{"hooks": [{"type": "command", "command": "x"}]}]}}`,
			"claude hook event SubagentStop"},
		{"claude misspelled event", "claude", claudeFile,
			`{"hooks": {"SessionStrat": [{"hooks": [{"type": "command", "command": "x"}]}]}}`,
			"claude hook event SessionStrat"},
		{"claude hook type", "claude", claudeFile,
			`{"hooks": {"Stop": [{"hooks": [{"type": "prompt", "command": "x"}]}]}}`,
			`claude hook type "prompt"`},
		{"antigravity unknown key", "agy", agyFile,
			`{"mestre": {"enabled": true, "PreInvokation": []}}`,
			"antigravity hook event PreInvokation"},
		{"antigravity group that is not a group", "agy", agyFile,
			`{"mestre": []}`,
			`antigravity hooks.json: "mestre" is not a hook group`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rep := &reporter{}
			l := &fakeagent.MemLauncher{Clock: clock.Real{}, Hooks: &hooks{}, Reporter: rep}
			e := claudeEnv(t)
			if tc.program == "agy" {
				e = newEnv(t)
				testkit.WriteFiles(t, e.home, map[string]string{
					".gemini/antigravity-cli/settings.json":           fmt.Sprintf(`{"trustedWorkspaces": [%q]}`, e.dir),
					".gemini/antigravity-cli/antigravity-oauth-token": "{}",
				})
			}
			testkit.WriteFiles(t, e.dir, map[string]string{tc.file: tc.content})

			p := start(t, l, tc.program, e)
			err := p.Wait()

			var ni *fakeagent.NotImplementedError
			if !errors.As(err, &ni) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Wait = %v, want not implemented: %s", err, tc.want)
			}
			if want := testkit.NotImplementedPrefix; len(rep.msgs) != 1 || !strings.HasPrefix(rep.msgs[0], want) || !strings.Contains(rep.msgs[0], tc.want) {
				t.Fatalf("test failures = %q", rep.msgs)
			}
		})
	}
}

func TestMissingRecordingOfAConfiguredEventFailsTheTest(t *testing.T) {
	t.Parallel()
	recs := fstestCopy(t, func(p string) bool { return path.Base(p) == "SessionStart.json" })
	e := claudeEnv(t)
	testkit.WriteFiles(t, e.dir, map[string]string{".claude/settings.local.json": claudeHooks})

	err := fakeagent.Run(t.Context(), fakeagent.Config{
		Provider: fakeagent.Claude, Env: e.vars, Dir: e.dir,
		In: strings.NewReader(""), Out: io.Discard,
		Clock: clock.Real{}, Hooks: &hooks{}, Recordings: recs,
	})

	var ni *fakeagent.NotImplementedError
	if !errors.As(err, &ni) || !strings.Contains(err.Error(), "claude hook event SessionStart has no recording") {
		t.Fatalf("Run = %v", err)
	}
}

// fstestCopy copies the recordings except the files skip selects.
func fstestCopy(t *testing.T, skip func(string) bool) fs.FS {
	t.Helper()
	m := fstest.MapFS{}
	src := testkit.Recordings()
	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || skip(p) {
			return err
		}
		b, err := fs.ReadFile(src, p)
		m[p] = &fstest.MapFile{Data: b}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPinnedRecordingVersion(t *testing.T) {
	t.Parallel()
	t.Run("a recorded version is used", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			h := &hooks{}
			l := &fakeagent.MemLauncher{Clock: clock.Real{}, Hooks: h, Reporter: t, RecordingVersion: "2.1.299"}
			e := claudeEnv(t)
			testkit.WriteFiles(t, e.dir, map[string]string{".claude/settings.local.json": claudeHooks})

			p := start(t, l, "claude", e)
			synctest.Wait()

			if got := h.events(); len(got) != 1 {
				t.Fatalf("hooks = %v", got)
			}
			sameShape(t, h.calls[0].Stdin, "claude-code", "SessionStart")
			_ = stop(t, p)
		})
	})
	for _, tc := range []struct {
		name string
		l    fakeagent.MemLauncher
		args []string
		vars []string
	}{
		{"launcher", fakeagent.MemLauncher{RecordingVersion: "9.9.9"}, nil, nil},
		{"flag", fakeagent.MemLauncher{}, []string{"--fake-recording-version", "9.9.9"}, nil},
		{"environment", fakeagent.MemLauncher{}, nil, []string{"FAKEAGENT_RECORDING_VERSION=9.9.9"}},
	} {
		t.Run("an unknown version fails through the "+tc.name, func(t *testing.T) {
			t.Parallel()
			rep := &reporter{}
			l := tc.l
			l.Clock, l.Hooks, l.Reporter = clock.Real{}, &hooks{}, rep
			e := claudeEnv(t)
			e.vars = append(e.vars, tc.vars...)

			p := start(t, &l, "claude", e, tc.args...)

			var ni *fakeagent.NotImplementedError
			if err := p.Wait(); !errors.As(err, &ni) || !strings.Contains(err.Error(), `recording version "9.9.9"`) {
				t.Fatalf("Wait = %v", err)
			}
			if len(rep.msgs) != 1 {
				t.Fatalf("test failures = %q", rep.msgs)
			}
		})
	}
}

// The layers add up (docs/provedores/codex.md#hooks): the profile's hooks.json
// and the project's own .codex/hooks.json both run once trusted.
func TestCodexRunsTheProjectLayerBesideTheProfileLayer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := &hooks{}
		l := &fakeagent.MemLauncher{Clock: clock.Real{}, Hooks: h, Reporter: t}
		codexHome := t.TempDir()
		e := newEnv(t, "CODEX_HOME="+codexHome)
		hooksJSON := func(cmd string) string {
			return fmt.Sprintf(`{"hooks": {"SessionStart": [{"matcher": "*", "hooks": [{"type": "command", "command": %q}]}]}}`, cmd)
		}
		testkit.WriteFiles(t, codexHome, map[string]string{
			"auth.json":   "{}",
			"config.toml": fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", e.dir),
			"hooks.json":  hooksJSON("profile-hook"),
		})
		testkit.WriteFiles(t, e.dir, map[string]string{".codex/hooks.json": hooksJSON("project-hook")})

		p := start(t, l, "codex", e)
		synctest.Wait()
		if out := p.Output(); !strings.Contains(out, "2 new or modified hook(s)") {
			t.Fatalf("both layers need review:\n%s", out)
		}
		_, _ = p.PTY().Write([]byte("\x1b[B\r")) // "Trust all and continue"
		synctest.Wait()
		_ = stop(t, p)

		p = start(t, l, "codex", e)
		synctest.Wait()
		if out := p.Output(); strings.Contains(out, fakeagent.HookReviewTitle) {
			t.Fatalf("trusted layers asked for review again:\n%s", out)
		}
		_ = stop(t, p)
		got := map[string]int{}
		for _, ev := range h.events() {
			got[ev]++
		}
		if got["SessionStart profile-hook"] != 2 || got["SessionStart project-hook"] != 2 {
			t.Fatalf("hooks = %v", h.events())
		}

		testkit.WriteFiles(t, e.dir, map[string]string{".codex/hooks.json": hooksJSON("evil-project-hook")})
		p = start(t, l, "codex", e)
		synctest.Wait()
		if out := p.Output(); !strings.Contains(out, "1 new or modified hook(s)") {
			t.Fatalf("modified project hook not reviewed:\n%s", out)
		}
		_ = stop(t, p)
	})
}

func TestAntigravityShowsTheLoginScreenWithoutACredential(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := &hooks{}
		l := &fakeagent.MemLauncher{Clock: clock.Real{}, Hooks: h, Reporter: t}
		e := newEnv(t)
		testkit.WriteFiles(t, e.home, map[string]string{
			".gemini/antigravity-cli/settings.json": fmt.Sprintf(`{"trustedWorkspaces": [%q]}`, e.dir),
		})

		p := start(t, l, "agy", e)
		synctest.Wait()
		if out := p.Output(); !strings.Contains(out, "Sign in with Google to use Antigravity") || strings.Contains(out, fakeagent.PromptMarker) {
			t.Fatalf("no login screen:\n%s", out)
		}
		_, _ = p.PTY().Write([]byte("\r"))
		if err := p.Wait(); !errors.Is(err, fakeagent.ErrLoginRequired) {
			t.Fatalf("Wait = %v, want login required", err)
		}
		if got := h.events(); len(got) != 0 {
			t.Fatalf("hooks ran before login: %v", got)
		}
	})
}
