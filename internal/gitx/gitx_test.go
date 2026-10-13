package gitx_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/logging"
)

func TestParseVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		out  string
		want gitx.Version
	}{
		{"git version 2.43.0\n", gitx.Version{Major: 2, Minor: 43}},
		{"git version 2.45.1.windows.1", gitx.Version{Major: 2, Minor: 45, Patch: 1}},
		{"git version 2.39.3 (Apple Git-145)", gitx.Version{Major: 2, Minor: 39, Patch: 3}},
		{"git version 2.32", gitx.Version{Major: 2, Minor: 32}},
	}
	for _, c := range cases {
		t.Run(c.out, func(t *testing.T) {
			t.Parallel()
			got, err := gitx.ParseVersion(c.out)
			if err != nil {
				t.Fatal(err)
			}
			got.Raw = ""
			if got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
	if _, err := gitx.ParseVersion("hello"); err == nil {
		t.Fatal("garbage parsed as a version")
	}
}

func TestCheckVersionNamesBothVersions(t *testing.T) {
	t.Parallel()
	if err := gitx.CheckVersion(gitx.Version{Major: 2, Minor: 32}, gitx.MinVersion); err != nil {
		t.Fatalf("2.32 refused: %v", err)
	}
	if err := gitx.CheckVersion(gitx.Version{Major: 3}, gitx.MinVersion); err != nil {
		t.Fatalf("3.0 refused: %v", err)
	}
	err := gitx.CheckVersion(gitx.Version{Major: 2, Minor: 31, Patch: 9}, gitx.MinVersion)
	if !errors.Is(err, gitx.ErrGitTooOld) {
		t.Fatalf("err = %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "2.31.9") || !strings.Contains(msg, "2.32.0") {
		t.Fatalf("message %q should name the found and the required version", msg)
	}
}

// CA-TESTES-05 (git layer): in the fast suite no git process starts.
func TestFastSuiteStartsNoProcess(t *testing.T) {
	t.Parallel()
	g := gitx.New(gitx.Options{Suite: "fast"})
	_, err := g.Version(context.Background())
	if !errors.Is(err, gitx.ErrExternalProcessInFastSuite) {
		t.Fatalf("err = %v, want ErrExternalProcessInFastSuite", err)
	}
	_, err = g.Init(context.Background(), t.TempDir(), gitx.InitOptions{})
	if !errors.Is(err, gitx.ErrExternalProcessInFastSuite) {
		t.Fatalf("Init err = %v, want ErrExternalProcessInFastSuite", err)
	}
}

func TestEnvironmentIsControlled(t *testing.T) {
	t.Parallel()
	g := gitx.New(gitx.Options{Env: []string{"GIT_CONFIG_GLOBAL=/custom/gitconfig"}})
	base := []string{
		"PATH=/usr/bin", "HOME=/home/x", "LANG=pt_BR.UTF-8", "LC_ALL=pt_BR.UTF-8",
		"GIT_DIR=/elsewhere/.git", "GIT_AUTHOR_NAME=Leak", "git_work_tree=/lowercase", "GIT_CONFIG_GLOBAL=/home/x/.gitconfig",
	}
	env := gitx.EnvironFrom(g, base)
	last := func(key string) string {
		v := ""
		for _, kv := range env {
			if k, val, _ := strings.Cut(kv, "="); k == key {
				v = val
			}
		}
		return v
	}
	for _, leak := range []string{"GIT_DIR", "GIT_AUTHOR_NAME", "git_work_tree"} {
		if last(leak) != "" {
			t.Errorf("%s leaked into the git environment", leak)
		}
	}
	if last("PATH") != "/usr/bin" || last("HOME") != "/home/x" {
		t.Errorf("PATH and HOME must pass through: %v", env)
	}
	for k, want := range map[string]string{
		"LC_ALL": "C", "GIT_CONFIG_NOSYSTEM": "1", "GIT_TERMINAL_PROMPT": "0", "GIT_CONFIG_GLOBAL": "/custom/gitconfig",
	} {
		if got := last(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if slices.Contains(env, "LANG=pt_BR.UTF-8") {
		t.Error("LANG leaked: messages must be in English for the error matching")
	}
}

func TestErrorsNeverCarryCredentials(t *testing.T) {
	t.Parallel()
	m := logging.NewMasker()
	m.Register("hunter2-secret")
	g := gitx.New(gitx.Options{Masker: m})
	e := gitx.MaskedError(g, []string{"clone", "https://bot:ghp_abcdefghijklmnopqrstuvwxyz0123456789@github.com/o/r.git"},
		128, "fatal: unable to access 'https://bot:ghp_abcdefghijklmnopqrstuvwxyz0123456789@github.com/o/r.git/': hunter2-secret")
	msg := e.Error()
	for _, leak := range []string{"ghp_", "bot:", "hunter2"} {
		if strings.Contains(msg, leak) {
			t.Errorf("error %q leaks %q", msg, leak)
		}
	}
	if !strings.Contains(msg, "github.com/o/r.git") || !strings.Contains(msg, "exit status 128") {
		t.Errorf("error %q lost the useful part", msg)
	}
	if got := gitx.RedactURL("see https://u:p@h/x and ssh://git@h/y"); strings.Contains(got, "u:p") || strings.Contains(got, "git@") {
		t.Errorf("RedactURL = %q", got)
	}
}

func TestParseStatus(t *testing.T) {
	t.Parallel()
	out := " M a b.txt\x00A  new.go\x00?? dir/x.md\x00R  to.go\x00from.go\x00"
	got := gitx.ParseStatus([]byte(out))
	want := []gitx.StatusEntry{
		{Index: ' ', Worktree: 'M', Path: "a b.txt"},
		{Index: 'A', Worktree: ' ', Path: "new.go"},
		{Index: '?', Worktree: '?', Path: "dir/x.md"},
		{Index: 'R', Worktree: ' ', Path: "to.go", OrigPath: "from.go"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if !got[2].Untracked() || got[0].Untracked() {
		t.Fatal("Untracked wrong")
	}
	if len(gitx.ParseStatus(nil)) != 0 {
		t.Fatal("empty status parsed as entries")
	}
}

func TestParseWorktrees(t *testing.T) {
	t.Parallel()
	out := "worktree /data/espelho.git\nbare\n\n" +
		"worktree /data/mesas/42-login\nHEAD 0123456789abcdef0123456789abcdef01234567\nbranch refs/heads/epico/42-login\n\n" +
		"worktree /data/mesas/det\nHEAD 89abcdef0123456789abcdef0123456789abcdef\ndetached\n\n" +
		"worktree /data/mesas/unborn\nHEAD 0000000000000000000000000000000000000000\nbranch refs/heads/new\n"
	got := gitx.ParseWorktrees([]byte(out))
	if len(got) != 4 {
		t.Fatalf("got %d worktrees: %+v", len(got), got)
	}
	if !got[0].Bare || got[0].Branch != "" || got[0].Head != "" {
		t.Errorf("bare = %+v", got[0])
	}
	if got[1].Branch != "epico/42-login" || got[1].Head != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("branch worktree = %+v", got[1])
	}
	if !got[2].Detached || got[2].Branch != "" {
		t.Errorf("detached = %+v", got[2])
	}
	if got[3].Head != "" || got[3].Branch != "new" {
		t.Errorf("unborn = %+v", got[3])
	}
}

func TestParseBundleHeader(t *testing.T) {
	t.Parallel()
	hdr := "# v3 git bundle\n@object-format=sha1\n-aaaa11 subject of the base\n" +
		"bbbb22 refs/heads/mesa\ncccc33 refs/heads/main\n\nPACK..."
	info, err := gitx.ParseBundleHeader(strings.NewReader(hdr))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(info.Prerequisites, []gitx.Hash{"aaaa11"}) {
		t.Errorf("prerequisites = %v", info.Prerequisites)
	}
	if len(info.Refs) != 2 || info.Refs[0] != (gitx.Ref{Name: "refs/heads/mesa", Hash: "bbbb22"}) {
		t.Errorf("refs = %+v", info.Refs)
	}
	for _, bad := range []string{"", "not a bundle\n", "# v2 git bundle\nbbbb22 refs/heads/x\n"} {
		if _, err := gitx.ParseBundleHeader(strings.NewReader(bad)); !errors.Is(err, gitx.ErrInvalidBundle) {
			t.Errorf("%q: err = %v, want ErrInvalidBundle", bad, err)
		}
	}
}

func TestParseLog(t *testing.T) {
	t.Parallel()
	rec := func(h, p, msg string) string {
		return strings.Join([]string{h, p, "Ana", "ana@x.invalid", "1767225600", "Bot", "bot@x.invalid", "1767229200", msg}, "\x1f")
	}
	out := rec("h2", "h1", "second\n\nbody line\n") + "\x00" + rec("h1", "", "first\n") + "\x00"
	got, err := gitx.ParseLog([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Hash != "h2" || got[0].Parents[0] != "h1" || len(got[1].Parents) != 0 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Message != "second\n\nbody line" || got[0].Subject() != "second" {
		t.Errorf("message = %q", got[0].Message)
	}
	if got[0].Author.When.Unix() != 1767225600 || got[0].Committer.When.Unix() != 1767229200 || got[0].Committer.Name != "Bot" {
		t.Errorf("signatures = %+v / %+v", got[0].Author, got[0].Committer)
	}
	if _, err := gitx.ParseLog([]byte("garbage")); err == nil {
		t.Fatal("garbage parsed")
	}
}

func TestExcludeBlockKeepsTheRestOfTheFile(t *testing.T) {
	t.Parallel()
	user := "# user lines\r\n*.log\r\n"
	step1 := gitx.ApplyExcludeBlock(user, "mestre", []string{"/.mestre/"})
	if !strings.HasPrefix(step1, user) {
		t.Fatalf("user lines changed: %q", step1)
	}
	if got, found := gitx.ParseExcludeBlock(step1, "mestre"); !found || !slices.Equal(got, []string{"/.mestre/"}) {
		t.Fatalf("block = %q, %v", got, found)
	}
	step2 := gitx.ApplyExcludeBlock(step1, "mestre", []string{"/.mestre/run/"})
	if got, _ := gitx.ParseExcludeBlock(step2, "mestre"); !slices.Equal(got, []string{"/.mestre/run/"}) {
		t.Fatalf("replace left %q", step2)
	}
	if !strings.HasPrefix(step2, user) || strings.Count(step2, "# >>> ") != 1 {
		t.Fatalf("replace broke the file: %q", step2)
	}
	other := gitx.ApplyExcludeBlock(step2, "other", []string{"x"})
	if got, _ := gitx.ParseExcludeBlock(other, "mestre"); !slices.Equal(got, []string{"/.mestre/run/"}) {
		t.Fatalf("a second block disturbed the first: %q", other)
	}
	if removed := gitx.ApplyExcludeBlock(step1, "mestre", nil); removed != user {
		t.Fatalf("removal = %q, want the user lines only", removed)
	}
	if again := gitx.ApplyExcludeBlock(step1, "mestre", []string{"/.mestre/"}); again != step1 {
		t.Fatalf("rewriting the same block changed the file: %q", again)
	}
	noNewline := gitx.ApplyExcludeBlock("*.tmp", "mestre", []string{"a"})
	if !strings.HasPrefix(noNewline, "*.tmp\n# >>>") {
		t.Fatalf("missing newline before the block: %q", noNewline)
	}
	if err := gitx.ValidateExcludeBlock("mestre", []string{"ok", "# <<< mestre:mestre <<<"}); !errors.Is(err, gitx.ErrInvalidArgument) {
		t.Fatalf("marker injection accepted: %v", err)
	}
}

func TestRefValidationRejectsOptions(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"", "-D", "--upload-pack=evil", "a\nb"} {
		if err := gitx.ValidateRef(bad); !errors.Is(err, gitx.ErrInvalidArgument) {
			t.Errorf("ValidateRef(%q) = %v", bad, err)
		}
	}
	if err := gitx.ValidateRef("refs/mestre/torres/t1/epico/42-login"); err != nil {
		t.Errorf("valid ref refused: %v", err)
	}
	if b, tip, err := gitx.ParseRevs("abc..main"); err != nil || b != "abc" || tip != "main" {
		t.Errorf("ParseRevs = %q, %q, %v", b, tip, err)
	}
	if _, _, err := gitx.ParseRevs("abc.."); err == nil {
		t.Error("open range accepted")
	}
}
