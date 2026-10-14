package conformance

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// The cleanup of a run removes only what it builds itself (the folders of
// the project, the session files of a session id with the shape of a
// UUID), reached through no link: a transcript path of a payload, the
// folder of one, an id that walks out of the profile or a planted link
// removes nothing elsewhere (M3 audit).
func TestCleanupRemovesOnlyWhatTheRunBuilds(t *testing.T) {
	t.Parallel()
	outside := t.TempDir()
	keep := filepath.Join(outside, "keep.jsonl")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	const id = "019a0000-0000-7000-8000-000000000001"
	for _, tt := range []struct {
		provider providers.ID
		files    []string // under the profile
		want     []string // what sessionFiles gives, under the profile
	}{
		{providers.Codex, []string{"sessions/2026/10/14/rollout-2026-10-14T10-00-00-" + id + ".jsonl", "sessions/2026/10/14/rollout-x-other.jsonl", "auth.json"},
			[]string{"sessions/2026/10/14/rollout-2026-10-14T10-00-00-" + id + ".jsonl"}},
		{providers.Antigravity, []string{".gemini/antigravity-cli/brain/" + id + "/x.pb"}, []string{".gemini/antigravity-cli/brain/" + id}},
		{providers.ClaudeCode, []string{"projects/" + claudeProject("/w/wt") + "/" + id + ".jsonl", "projects/other/x.jsonl"},
			[]string{"projects/" + claudeProject("/w/wt")}},
	} {
		profile := t.TempDir()
		for _, f := range tt.files {
			p := filepath.Join(profile, filepath.FromSlash(f))
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		ru := &run{req: Request{Provider: tt.provider, Profile: Profile{Dir: profile}}, ws: &Workspace{Worktree: "/w/wt", Mirror: "/w/mirror.git"}}
		ru.events = []providers.Event{
			{SessionID: id, TranscriptPath: keep},                                   // a payload path outside
			{SessionID: "../../../" + filepath.Base(outside), TranscriptPath: keep}, // an id that walks out
			{SessionID: "x", TranscriptPath: filepath.Join(profile, "auth.json")},   // a payload path inside
		}
		got := ru.sessionFiles()
		var want []string
		for _, w := range tt.want {
			want = append(want, filepath.Join(profile, filepath.FromSlash(w)))
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: sessionFiles = %q, want %q", tt.provider, got, want)
		}
	}
	if runtime.GOOS == "windows" {
		return // a symbolic link takes a privilege there; noLinkPath has its own test of junctions
	}
	// A link planted where a folder of the run would be leads nowhere.
	profile := t.TempDir()
	if err := os.MkdirAll(filepath.Join(profile, ".gemini", "antigravity-cli"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(profile, ".gemini", "antigravity-cli", "brain")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(outside, id), 0o700); err != nil {
		t.Fatal(err)
	}
	ru := &run{req: Request{Provider: providers.Antigravity, Profile: Profile{Dir: profile}}, events: []providers.Event{{SessionID: id}}}
	if got := ru.sessionFiles(); len(got) != 0 {
		t.Errorf("through a planted link: %q", got)
	}
}
