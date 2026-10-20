package providers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// decodeNumbers decodes a JSON object keeping the text of the numbers.
func decodeNumbers(t *testing.T, b []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, b)
	}
	return m
}

func trustFile(t *testing.T, content string) (profile, path string) {
	t.Helper()
	profile = t.TempDir()
	path = filepath.Join(profile, ".claude.json")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return profile, path
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const (
	trustWorktree = "/srv/mestre/mesas/3-login"
	trustMirror   = "/srv/mestre/espelhos/projeto.git"
)

// CA-CLAUDE-03
func TestPreTrustTrustsTheWorktreeAndTakesTheTrustOffTheMirror(t *testing.T) {
	t.Parallel()
	before := `{
  "numStartups": 7,
  "oauthAccount": {"emailAddress": "dev@example.com"},
  "bigNumber": 12345678901234567890,
  "projects": {
    "` + trustMirror + `": {"hasTrustDialogAccepted": true, "allowedTools": [], "history": [{"display": "hi"}], "lastCost": 0.5},
    "/srv/outro": {"hasTrustDialogAccepted": false, "exampleFiles": ["a.go"]}
  }
}`
	profile, path := trustFile(t, before)
	claude := providers.NewClaudeCode(providers.Options{})
	req := providers.PreTrustRequest{OS: "linux", Profile: profile, Worktree: trustWorktree, RepoRoot: trustMirror}

	first, err := claude.PreTrust(req)
	if err != nil {
		t.Fatal(err)
	}
	afterFirst := readFile(t, path)
	second, err := claude.PreTrust(req) // PreTrust ran twice
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed || first.Path != path || len(first.Trusted) != 1 || first.Trusted[0] != trustWorktree || len(first.Cleared) != 1 || first.Cleared[0] != trustMirror {
		t.Errorf("first = %+v", first)
	}
	if second.Changed || len(second.Trusted) != 0 || len(second.Cleared) != 0 || !bytes.Equal(readFile(t, path), afterFirst) {
		t.Errorf("the second run changed something: %+v", second)
	}

	want := decodeNumbers(t, []byte(before))
	projects := want["projects"].(map[string]any)
	projects[trustWorktree] = map[string]any{"hasTrustDialogAccepted": true}
	delete(projects[trustMirror].(map[string]any), "hasTrustDialogAccepted")
	got := decodeNumbers(t, readFile(t, path))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the file is not the original with the two changes:\ngot  %v\nwant %v", got, want)
	}
	if projects, _ := got["projects"].(map[string]any); projects[trustWorktree].(map[string]any)["hasTrustDialogAccepted"] != true {
		t.Errorf("the worktree is not trusted: %v", projects[trustWorktree])
	}
}

// P11: with separate accounts, the file the engine renames belongs to the
// engine, and Claude Code reads and rewrites it as mestre-agente: the mode is
// the rule of every native file (0664, the group of the folder), never the
// 0600 Claude Code creates it with.
func TestPreTrustCreatesTheFileWithTheNativeModeAndRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()
	claude := providers.NewClaudeCode(providers.Options{})

	profile, path := trustFile(t, "")
	if _, err := claude.PreTrust(providers.PreTrustRequest{OS: "linux", Profile: profile, Worktree: trustWorktree}); err != nil {
		t.Fatal(err)
	}
	if got := decodeNumbers(t, readFile(t, path)); !reflect.DeepEqual(got, map[string]any{"projects": map[string]any{trustWorktree: map[string]any{"hasTrustDialogAccepted": true}}}) {
		t.Errorf("created file = %v", got)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o664 {
			t.Errorf("new file mode = %v, want 0664", st.Mode().Perm())
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := claude.PreTrust(providers.PreTrustRequest{OS: "linux", Profile: profile, Worktree: "/srv/mestre/mesas/4-outra"}); err != nil {
			t.Fatal(err)
		}
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o664 {
			t.Errorf("rewritten file mode = %v, want 0664, not the 0600 it had", st.Mode().Perm())
		}
	}
	if entries, _ := filepath.Glob(filepath.Join(profile, ".*.mestre-*")); len(entries) != 0 {
		t.Errorf("temporary files left: %v", entries)
	}

	for _, bad := range []string{`{"projects": `, `[]`, `{"projects": []}`, `{"projects": {}} x`} {
		profile, path := trustFile(t, bad)
		_, err := claude.PreTrust(providers.PreTrustRequest{OS: "linux", Profile: profile, Worktree: trustWorktree})
		if !errors.Is(err, providers.ErrNativeFile) {
			t.Errorf("%q: err = %v, want ErrNativeFile", bad, err)
		}
		if string(readFile(t, path)) != bad {
			t.Errorf("%q: the file was touched", bad)
		}
	}
	for name, req := range map[string]providers.PreTrustRequest{
		"relative worktree": {OS: "linux", Profile: profile, Worktree: "mesa"},
		"no profile":        {OS: "linux", Worktree: trustWorktree},
		"profile missing":   {OS: "linux", Profile: filepath.Join(profile, "nao-existe"), Worktree: trustWorktree},
	} {
		if _, err := claude.PreTrust(req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The clone of the Citadel (Grão-Mestre) is both the worktree and the root of
// the repository: its trust is not the mirror's.
func TestPreTrustKeepsTheTrustWhenTheWorktreeIsTheRoot(t *testing.T) {
	t.Parallel()
	profile, path := trustFile(t, "")
	claude := providers.NewClaudeCode(providers.Options{})
	req := providers.PreTrustRequest{OS: "linux", Profile: profile, Worktree: "/srv/mestre/cidadela/", RepoRoot: "/srv/mestre/cidadela"}
	for range 2 {
		if _, err := claude.PreTrust(req); err != nil {
			t.Fatal(err)
		}
	}
	projects := decodeNumbers(t, readFile(t, path))["projects"].(map[string]any)
	if e, _ := projects["/srv/mestre/cidadela"].(map[string]any); e["hasTrustDialogAccepted"] != true || len(projects) != 1 {
		t.Errorf("projects = %v", projects)
	}
}

// On Windows the key may have other slashes or case than the path the engine
// has; PreTrust sets that same key, so there are not two.
func TestPreTrustKnowsTheKeyOfWindowsByAnyWriting(t *testing.T) {
	t.Parallel()
	profile, path := trustFile(t, `{"projects": {"c:/mestre/work/3-login": {"allowedTools": []}, "C:/Mestre/Espelhos/projeto.git": {"hasTrustDialogAccepted": true}}}`)
	claude := providers.NewClaudeCode(providers.Options{})
	if _, err := claude.PreTrust(providers.PreTrustRequest{OS: "windows", Profile: profile, Worktree: `C:\Mestre\work\3-login`, RepoRoot: `C:\Mestre\espelhos\projeto.git`}); err != nil {
		t.Fatal(err)
	}
	projects := decodeNumbers(t, readFile(t, path))["projects"].(map[string]any)
	if len(projects) != 2 {
		t.Fatalf("projects = %v, want the two keys that were there", projects)
	}
	wt := projects["c:/mestre/work/3-login"].(map[string]any)
	if wt["hasTrustDialogAccepted"] != true || wt["allowedTools"] == nil {
		t.Errorf("worktree entry = %v", wt)
	}
	if _, has := projects["C:/Mestre/Espelhos/projeto.git"].(map[string]any)["hasTrustDialogAccepted"]; has {
		t.Errorf("mirror entry kept the trust: %v", projects["C:/Mestre/Espelhos/projeto.git"])
	}
}

// On Windows Claude Code reads the key only with forward slashes: PreTrust
// writes it so, a key with backslashes (of an older Mestre) does not count as
// the worktree's, and Untrust takes both out.
func TestPreTrustOnWindowsWritesTheKeyWithForwardSlashes(t *testing.T) {
	t.Parallel()
	profile, path := trustFile(t, `{"projects": {"C:\\Mestre\\work\\3-login": {"hasTrustDialogAccepted": true}}}`)
	claude := providers.NewClaudeCode(providers.Options{})
	req := providers.PreTrustRequest{OS: "windows", Profile: profile, Worktree: `C:\Mestre\work\3-login`}
	if _, err := claude.PreTrust(req); err != nil {
		t.Fatal(err)
	}
	projects := decodeNumbers(t, readFile(t, path))["projects"].(map[string]any)
	if e, _ := projects["C:/Mestre/work/3-login"].(map[string]any); e["hasTrustDialogAccepted"] != true {
		t.Fatalf("projects = %v, want the key C:/Mestre/work/3-login trusted", projects)
	}
	if _, err := claude.(providers.TrustRemover).Untrust(req); err != nil {
		t.Fatal(err)
	}
	if projects := decodeNumbers(t, readFile(t, path))["projects"].(map[string]any); len(projects) != 0 {
		t.Errorf("projects after Untrust = %v, want none of the two writings", projects)
	}
}

// Untrust takes back what PreTrust gave, when the worktree is removed.
func TestUntrustTakesTheWorktreeOutAndNothingElse(t *testing.T) {
	t.Parallel()
	profile, path := trustFile(t, `{"numStartups": 1, "projects": {"/srv/outro": {"hasTrustDialogAccepted": true}}}`)
	claude := providers.NewClaudeCode(providers.Options{})
	req := providers.PreTrustRequest{OS: "linux", Profile: profile, Worktree: trustWorktree, RepoRoot: trustMirror}
	if _, err := claude.PreTrust(req); err != nil {
		t.Fatal(err)
	}
	remover, ok := claude.(providers.TrustRemover)
	if !ok {
		t.Fatal("the Claude Code adapter cannot take the trust back")
	}
	for i := range 2 { // twice: idempotent
		res, err := remover.Untrust(req)
		if err != nil || res.Changed != (i == 0) {
			t.Fatalf("Untrust %d = %+v, %v", i, res, err)
		}
	}
	want := map[string]any{"numStartups": json.Number("1"), "projects": map[string]any{"/srv/outro": map[string]any{"hasTrustDialogAccepted": true}}}
	if got := decodeNumbers(t, readFile(t, path)); !reflect.DeepEqual(got, want) {
		t.Errorf("file after Untrust = %v, want %v", got, want)
	}
}
