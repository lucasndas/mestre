package providers

import (
	"os"
	"path/filepath"
	"testing"
)

// Claude Code rewrites .claude.json: a write that was undone by it meanwhile
// is done again, up to three times, and the result is checked
// (docs/provedores/claude-code.md#regras-e-erros).
func TestPreTrustRepeatsWhenClaudeRewritesTheFile(t *testing.T) {
	t.Parallel()
	req := func(profile string) PreTrustRequest {
		return PreTrustRequest{OS: "linux", Profile: profile, Worktree: "/srv/mestre/mesas/3-login", RepoRoot: "/srv/mestre/espelhos/projeto.git"}
	}
	original := `{"projects": {"/srv/mestre/espelhos/projeto.git": {"hasTrustDialogAccepted": true}}}`
	for _, tt := range []struct {
		name     string
		rewrites int // how many times Claude Code rewrites the file after our write
		wantErr  bool
	}{
		{"not rewritten", 0, false},
		{"rewritten once", 1, false},
		{"rewritten twice", 2, false},
		{"rewritten every time", 3, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			profile := t.TempDir()
			path := filepath.Join(profile, ".claude.json")
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			rewrites := 0
			c := &claudeCode{afterTrustWrite: func(p string) {
				if rewrites < tt.rewrites { // Claude writes its own copy, without our change
					rewrites++
					if err := os.WriteFile(p, []byte(original), 0o600); err != nil {
						t.Error(err)
					}
				}
			}}
			res, err := c.PreTrust(req(profile))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("PreTrust = %+v, want an error after %d attempts", res, trustAttempts)
				}
				return
			}
			if err != nil || !res.Changed {
				t.Fatalf("PreTrust = %+v, %v", res, err)
			}
			b, _ := os.ReadFile(path)
			obj, err := parseObject(b, path)
			if err != nil {
				t.Fatal(err)
			}
			projects := obj["projects"].(map[string]any)
			wt, _ := projects["/srv/mestre/mesas/3-login"].(map[string]any)
			mirror, _ := projects["/srv/mestre/espelhos/projeto.git"].(map[string]any)
			if _, has := mirror["hasTrustDialogAccepted"]; wt["hasTrustDialogAccepted"] != true || has {
				t.Errorf("after %d rewrites the file is %s", tt.rewrites, b)
			}
		})
	}
}
