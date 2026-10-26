package providers_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/testkit"
)

// Untrust gives the file back as it was before PreTrust: the entry of the
// worktree goes, the rest stays byte for byte, and a second call changes
// nothing (the end of the conformance test and the removal of a worktree).
func TestUntrustTakesBackOnlyTheEntryOfTheWorktree(t *testing.T) {
	t.Parallel()
	wt := filepath.Join(t.TempDir(), "mesa", "1-login")
	for _, c := range []struct {
		name    string
		a       providers.Adapter
		file    func(profile string) string
		content string
	}{
		{"codex", providers.NewCodex(providers.Options{}), func(p string) string { return filepath.Join(p, "config.toml") }, codexConfigWithComments},
		{"antigravity", providers.NewAntigravity(providers.Options{}), providers.AntigravitySettingsPath,
			`{"allowNonWorkspaceAccess": true, "trustedWorkspaces": ["/other"], "toolPermission": "request-review"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			profile := t.TempDir()
			path := c.file(profile)
			rel, err := filepath.Rel(profile, path)
			if err != nil {
				t.Fatal(err)
			}
			testkit.WriteFiles(t, profile, map[string]string{filepath.ToSlash(rel): c.content})
			req := providers.PreTrustRequest{OS: runtime.GOOS, Profile: profile, Worktree: wt, RepoRoot: filepath.Dir(wt)}
			if _, err := c.a.PreTrust(req); err != nil {
				t.Fatal(err)
			}
			un, ok := c.a.(providers.TrustRemover)
			if !ok {
				t.Fatal("no Untrust")
			}
			first, err := un.Untrust(req)
			if err != nil || !first.Changed || len(first.Cleared) != 1 {
				t.Fatalf("untrust = %+v, %v", first, err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if c.name == "codex" && string(b) != c.content {
				t.Fatalf("config.toml after untrust:\n%s\nwant it as before:\n%s", b, c.content)
			}
			if c.name == "antigravity" {
				var got struct {
					Allow   bool     `json:"allowNonWorkspaceAccess"`
					Trusted []string `json:"trustedWorkspaces"`
					Perm    string   `json:"toolPermission"`
				}
				if err := json.Unmarshal(b, &got); err != nil || !got.Allow || got.Perm != "request-review" || len(got.Trusted) != 1 || got.Trusted[0] != "/other" {
					t.Fatalf("settings.json after untrust: %s (%v)", b, err)
				}
			}
			if again, err := un.Untrust(req); err != nil || again.Changed {
				t.Fatalf("second untrust = %+v, %v", again, err)
			}
		})
	}
}
