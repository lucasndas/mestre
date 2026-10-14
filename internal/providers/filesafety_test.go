package providers_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/safefile"
)

// The account profiles and the worktrees are written by the account of the
// agents (C2/P5 of the M3 audit): an agent can put a link to an engine file
// in place of a file the engine reads or rewrites there. The engine refuses
// it instead of copying the engine file into a file the agents read.

const engineSecret = "-----BEGIN PRIVATE KEY-----\nAAAA\n"

func plantLink(t *testing.T, link string) (secret string) {
	t.Helper()
	secret = filepath.Join(t.TempDir(), "engine.key")
	if err := os.WriteFile(secret, []byte(engineSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symbolic links need a privilege here (internal/safefile tests the junctions): %v", err)
		}
		t.Fatal(err)
	}
	return secret
}

func untouched(t *testing.T, secret, link string) {
	t.Helper()
	b, err := os.ReadFile(secret)
	if err != nil || string(b) != engineSecret {
		t.Fatalf("the engine file changed: %q, %v", b, err)
	}
	if st, err := os.Lstat(link); err == nil && st.Mode().Type() != os.ModeSymlink {
		if c, _ := os.ReadFile(link); strings.Contains(string(c), "PRIVATE KEY") {
			t.Fatalf("the engine file was copied into %s", link)
		}
	}
}

// CA-NATIVO-10
func TestPreTrustRefusesALinkInTheProfile(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		adapter providers.Adapter
		file    func(profile string) string
	}{
		"claude-code": {providers.NewClaudeCode(providers.Options{}), func(p string) string { return filepath.Join(p, ".claude.json") }},
		"codex":       {providers.NewCodex(providers.Options{}), func(p string) string { return filepath.Join(p, "config.toml") }},
		"antigravity": {providers.NewAntigravity(providers.Options{}), providers.AntigravitySettingsPath},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			profile := t.TempDir()
			link := tc.file(profile)
			secret := plantLink(t, link)
			_, err := tc.adapter.PreTrust(providers.PreTrustRequest{OS: runtime.GOOS, Profile: profile, Worktree: filepath.Join(profile, "wt")})
			if !errors.Is(err, safefile.ErrLink) {
				t.Fatalf("PreTrust over a link = %v, want ErrLink", err)
			}
			untouched(t, secret, link)
		})
	}
}

// CA-NATIVO-10
//
// The Mestre layer reads MESTRE.md from the worktree: a link in its place
// would put the engine file in the agent file of Antigravity or on the
// command line of Codex.
func TestTheMestreLayerRefusesALinkInPlaceOfMestreMD(t *testing.T) {
	t.Parallel()
	for name, a := range map[string]providers.Adapter{
		"codex":       providers.NewCodex(providers.Options{}),
		"antigravity": providers.NewAntigravity(providers.Options{}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			wt := t.TempDir()
			md := filepath.Join(wt, ".mestre", "agents", "a1", "MESTRE.md")
			secret := plantLink(t, md)
			l, err := a.DeliverMestreLayer(providers.LayerRequest{Path: md, OS: runtime.GOOS, AgentID: "a1", Worktree: wt})
			if !errors.Is(err, safefile.ErrLink) {
				t.Fatalf("layer from a link = %+v, %v; want ErrLink", l, err)
			}
			untouched(t, secret, md)
		})
	}
	wt := t.TempDir()
	agent := providers.AntigravityAgentPath(wt, "a1")
	plantLink(t, agent)
	if err := providers.CheckAntigravityAgentFile(agent, "mestre-a1"); !errors.Is(err, providers.ErrAgentFile) || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatalf("the check of a linked agent file = %v", err)
	}
}

// CA-NATIVO-12
//
// C3: the writable roots of Codex come from the engine's record of the
// worktree, never from the .git file or commondir the agent writes.
func TestCodexGitRootsComeFromTheRecord(t *testing.T) {
	t.Parallel()
	mirror := filepath.Join(t.TempDir(), "espelho.git")
	gitdir := filepath.Join(mirror, "worktrees", "mesa")
	for _, d := range []string{gitdir, filepath.Join(mirror, "objects"), filepath.Join(mirror, "refs")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	wt := t.TempDir()
	// The agent points the .git file of its worktree somewhere else.
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+elsewhere+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	codex := providers.NewCodex(providers.Options{}).(providers.NativeTranslator)
	roots := func(p providers.NativePolicy) (string, error) {
		nc, err := codex.Native(p)
		for i, a := range nc.Args {
			if i > 0 && strings.HasPrefix(a, "sandbox_workspace_write.writable_roots=") {
				return a, err
			}
		}
		return "", err
	}
	p := providers.NativePolicy{AgentID: "a1", OS: runtime.GOOS, Worktree: wt, Write: []string{"**"}}
	if got, err := roots(p); err != nil || got != "" {
		t.Fatalf("without a record = %q, %v; want no git root (the .git file is not read)", got, err)
	}
	p.GitDir, p.GitCommon = gitdir, mirror
	got, err := roots(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{gitdir, filepath.Join(mirror, "objects"), filepath.Join(mirror, "refs")} {
		if !strings.Contains(got, strings.ReplaceAll(want, `\`, `\\`)) {
			t.Errorf("roots %s lack %s", got, want)
		}
	}
	if strings.Contains(got, elsewhere) || strings.Contains(got, "logs") {
		t.Errorf("roots %s have a folder that is not in the record or does not exist", got)
	}
	p.NoWrite = true
	if got, _ := roots(p); got != "" {
		t.Errorf("a class without write opens %s", got)
	}
	p.NoWrite = false
	for name, bad := range map[string]providers.NativePolicy{
		"git folder outside the mirror": {GitDir: elsewhere, GitCommon: mirror},
		"git folder deeper":             {GitDir: filepath.Join(gitdir, "x"), GitCommon: mirror},
		"relative":                      {GitDir: "worktrees/mesa", GitCommon: "espelho.git"},
	} {
		bad.AgentID, bad.OS, bad.Worktree = "a1", runtime.GOOS, wt
		if _, err := codex.Native(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if runtime.GOOS != "windows" {
		linked := filepath.Join(mirror, "worktrees", "linked")
		if err := os.Symlink(elsewhere, linked); err != nil {
			t.Fatal(err)
		}
		p.GitDir = linked
		if _, err := codex.Native(p); err == nil {
			t.Error("a git folder that is a link was accepted")
		}
	}
}
