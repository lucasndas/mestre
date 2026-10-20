package providers_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

func codexAdapter(opts providers.Options) providers.Adapter { return providers.NewCodex(opts) }

// mergeFile applies the merge of a generated file to the current content.
func mergeFile(t *testing.T, f providers.ManagedFile, cur []byte) []byte {
	t.Helper()
	out, err := f.Merge(cur)
	if err != nil {
		t.Fatalf("merge %s: %v", f.Path, err)
	}
	return out
}

const codexConfigWithComments = `# Codex config of the profile
model = "gpt-5.5-codex"  # the default model
cli_auth_credentials_store = "file"

[projects."/srv/other"]
trust_level = "trusted"

# MCP servers the person added
[mcp_servers.docs]
command = "docs-mcp"
args = [
  "--port", # a comment inside an array
  "[not a table]",
]
notes = """
[projects."/srv/w/1-login"]
trust_level = "untrusted"
"""
`

// pretrust runs PreTrust over a config.toml with the given content and
// returns what is in the file afterwards.
func pretrust(t *testing.T, a providers.Adapter, profile, worktree string, content *string) (providers.Trust, string) {
	t.Helper()
	path := filepath.Join(profile, "config.toml")
	if content != nil {
		if err := os.WriteFile(path, []byte(*content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := a.PreTrust(providers.PreTrustRequest{OS: "linux", Profile: profile, Worktree: worktree, RepoRoot: filepath.Join(profile, "espelho")})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return tr, string(b)
}

// CA-CODEX-02
func TestPreTrustTrustsTheWorktreeAndKeepsTheRestOfTheFile(t *testing.T) {
	t.Parallel()
	a := codexAdapter(providers.Options{})
	profile := t.TempDir()
	worktree := filepath.Join(string(filepath.Separator)+"srv", "w", "1-login")
	content := codexConfigWithComments
	first, once := pretrust(t, a, profile, worktree, &content)
	second, twice := pretrust(t, a, profile, worktree, nil)
	if twice != once || second.Changed || !first.Changed || first.Path != filepath.Join(profile, "config.toml") {
		t.Fatalf("a second PreTrust changed the file (%+v, %+v):\n%s\n---\n%s", first, second, once, twice)
	}
	added := "\n[projects." + tomlKeyOf(worktree) + "]\ntrust_level = \"trusted\"\n"
	if once != codexConfigWithComments+added {
		t.Fatalf("the rest of the file changed, or the entry is not the worktree's:\n%s", once)
	}
	if strings.Contains(once, "espelho") || len(first.Trusted) != 1 || first.Trusted[0] != filepath.Clean(worktree) {
		t.Fatalf("trusted %v: only the worktree, never the repository root", first.Trusted)
	}
}

// tomlKeyOf is how the test expects a path as a quoted TOML key.
func tomlKeyOf(p string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(p, `\`, `\\`), `"`, `\"`) + `"`
}

func TestPreTrustFixesAnUntrustedEntryAndRefusesOtherForms(t *testing.T) {
	t.Parallel()
	a := codexAdapter(providers.Options{})
	wt := "/srv/w"
	key := tomlKeyOf(filepath.Clean(wt))
	for _, c := range []struct{ in, want string }{
		{"[projects." + key + "]  # the worktree\r\n  trust_level = \"untrusted\" # set by the TUI\r\nother = 1\r\n",
			"[projects." + key + "]  # the worktree\r\n  trust_level = \"trusted\"\r\nother = 1\r\n"},
		{"", "[projects." + key + "]\ntrust_level = \"trusted\"\n"},
	} {
		if _, got := pretrust(t, a, t.TempDir(), wt, &c.in); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
	if _, got := pretrust(t, a, t.TempDir(), wt, nil); got != "[projects."+key+"]\ntrust_level = \"trusted\"\n" {
		t.Errorf("new file = %q", got)
	}
	for _, bad := range []string{
		"projects = { " + key + " = { trust_level = \"untrusted\" } }\n",
		"projects." + key + ".trust_level = \"untrusted\"\n",
		"[projects]\n" + key + " = { trust_level = \"untrusted\" }\n",
		"[[projects]]\nx = 1\n",
		"notes = \"\"\"unterminated\n",
	} {
		profile := t.TempDir()
		path := filepath.Join(profile, "config.toml")
		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := a.PreTrust(providers.PreTrustRequest{Profile: profile, Worktree: wt}); !errors.Is(err, providers.ErrNativeFile) {
			t.Errorf("%q: %v; want a refusal", bad, err)
		}
		if b, _ := os.ReadFile(path); string(b) != bad {
			t.Errorf("%q was touched: %q", bad, b)
		}
	}
	if _, err := a.PreTrust(providers.PreTrustRequest{Profile: t.TempDir(), Worktree: "w"}); err == nil {
		t.Error("a relative worktree was trusted")
	}
	if _, err := a.PreTrust(providers.PreTrustRequest{Worktree: wt}); err == nil {
		t.Error("pre-trust without the profile")
	}
	if _, err := a.PreTrust(providers.PreTrustRequest{Profile: filepath.Join(t.TempDir(), "missing"), Worktree: wt}); err == nil {
		t.Error("pre-trust in a profile folder that does not exist")
	}
}

const mestreMD = "# Camada do Mestre\n\nRegra de prioridade: o \"Mestre\" vence.\n\tAção: não usar C:\\Temp nem '\"\"\"'.\r\nFim — ç ã é ü 世界 \x01\n"

// CA-CODEX-03
func TestBuildLaunchCarriesMestreMDAsATOMLString(t *testing.T) {
	t.Parallel()
	a := codexAdapter(providers.Options{})
	path := filepath.Join(t.TempDir(), "MESTRE.md")
	if err := os.WriteFile(path, []byte(mestreMD), 0o600); err != nil {
		t.Fatal(err)
	}
	layer, err := a.DeliverMestreLayer(providers.LayerRequest{OS: "linux", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	l, err := a.BuildLaunch(providers.LaunchRequest{OS: "linux",
		Base: providers.Base{Command: "codex"}, Profile: "/profiles/codex-1",
		ProviderArgs: slices.Concat(layer.Args, []string{"-s", "workspace-write"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	i := slices.Index(l.Args, "-c")
	if i < 0 || i+1 >= len(l.Args) {
		t.Fatalf("args %q have no -c", l.Args)
	}
	value, ok := strings.CutPrefix(l.Args[i+1], "developer_instructions=")
	if !ok {
		t.Fatalf("-c %q is not developer_instructions", l.Args[i+1])
	}
	got, err := readTOMLBasicString(value)
	if err != nil {
		t.Fatalf("value %s is not a TOML string: %v", value, err)
	}
	if got != mestreMD {
		t.Fatalf("read back as TOML:\n%q\nwant the file:\n%q", got, mestreMD)
	}
}

// readTOMLBasicString reads one TOML basic string (the whole of s), by the
// rules of the TOML spec: no raw control character but tab, and the escapes
// \b \t \n \f \r \" \\ \uXXXX \UXXXXXXXX.
func readTOMLBasicString(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", errors.New("not between quotes")
	}
	in := []rune(s[1 : len(s)-1])
	var b strings.Builder
	for i := 0; i < len(in); i++ {
		r := in[i]
		switch {
		case r == '"':
			return "", errors.New("unescaped quote")
		case r < 0x20 && r != '\t' || r == 0x7f:
			return "", errors.New("raw control character " + strconv.QuoteRune(r))
		case r != '\\':
			b.WriteRune(r)
			continue
		}
		i++
		if i >= len(in) {
			return "", errors.New("escape at the end")
		}
		switch in[i] {
		case 'b':
			b.WriteRune('\b')
		case 't':
			b.WriteRune('\t')
		case 'n':
			b.WriteRune('\n')
		case 'f':
			b.WriteRune('\f')
		case 'r':
			b.WriteRune('\r')
		case '"':
			b.WriteRune('"')
		case '\\':
			b.WriteRune('\\')
		case 'u', 'U':
			n := 4
			if in[i] == 'U' {
				n = 8
			}
			if i+1+n > len(in) {
				return "", errors.New("short escape")
			}
			v, err := strconv.ParseUint(string(in[i+1:i+1+n]), 16, 32)
			if err != nil {
				return "", err
			}
			b.WriteRune(rune(v))
			i += n
		default:
			return "", errors.New("unknown escape")
		}
	}
	return b.String(), nil
}

func TestTheMestreLayerFitsTheWindowsCommandLine(t *testing.T) {
	t.Parallel()
	a := codexAdapter(providers.Options{})
	path := filepath.Join(t.TempDir(), "MESTRE.md")
	if err := os.WriteFile(path, []byte(strings.Repeat(`"quoted" `, 4000)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DeliverMestreLayer(providers.LayerRequest{OS: "windows", Path: path}); err == nil {
		t.Fatal("a layer above the Windows command line was delivered")
	}
	if _, err := a.DeliverMestreLayer(providers.LayerRequest{OS: "linux", Path: path}); err != nil {
		t.Fatalf("linux has no such limit: %v", err)
	}
	if err := os.WriteFile(path, []byte{0xff, 0xfe}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DeliverMestreLayer(providers.LayerRequest{Path: path}); err == nil {
		t.Fatal("MESTRE.md that is not UTF-8 was delivered")
	}
}

func TestCodexStartFlags(t *testing.T) {
	t.Parallel()
	a := codexAdapter(providers.Options{})
	req := providers.LaunchRequest{
		Base:    providers.Base{Command: "codex"},
		Env:     []string{"CODEX_HOME=/elsewhere"}, // a base may not set it at all (TestABaseMayNotSetTheVariablesOfTheSafety)
		OS:      "linux",
		Profile: "/profiles/codex-1", Model: "gpt-5.5-codex",
		ProviderArgs: []string{"-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=false"},
	}
	l, err := a.BuildLaunch(req)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=false", "--no-daemon", "-a", "on-request", "-m", "gpt-5.5-codex", "-c", "features.hooks=true"}
	if !slices.Equal(l.Args, want) {
		t.Fatalf("args = %q, want %q", l.Args, want)
	}
	if slices.Contains(l.Env, "CODEX_HOME=/elsewhere") || !slices.Contains(l.Env, "CODEX_HOME=/profiles/codex-1") {
		t.Fatalf("env = %q: CODEX_HOME is the profile's", l.Env)
	}
	// What the base already has is not added twice.
	req.Base.Args = []string{"--no-daemon", "--ask-for-approval=on-request", "--model", "o4"}
	if l, _ = a.BuildLaunch(req); !slices.Equal(l.Args, slices.Concat(req.Base.Args, req.ProviderArgs, []string{"-c", "features.hooks=true"})) {
		t.Fatalf("args = %q", l.Args)
	}
	sandbox := []string{"-s", "read-only"}
	for _, bad := range [][]string{
		{"--dangerously-bypass-approvals-and-sandbox"}, {"--yolo"}, {"--full-auto"}, {"--add-dir", "/x"},
		{"--dangerously-bypass-hook-trust"}, {"--disable", "hooks"}, {"--disable=hooks"},
		{"-c", "features.hooks=false"}, {"-c", `model_instructions_file="/x"`}, {"-c", "project_doc_max_bytes=0"},
		{"-c", `sandbox_mode="danger-full-access"`}, {"-s", "danger-full-access"}, {"--sandbox=danger-full-access"},
		{"-a", "never"}, {"-c", "approval_policy=never"}, {"-c", "instructions=x"}, {"--last"},
		// The bypasses of the M3 audit, in the grammar of clap: attached
		// short values, "=", inline tables, the keys of the engine and of
		// the hooks, other folders and profiles, subcommands and prompts.
		{"-sdanger-full-access"}, {"-s=danger-full-access"}, {"--sandbox", "danger-full-access"},
		{"-cfeatures.hooks=false"}, {"-c", "features={hooks=false}"}, {"-c", "features = { hooks = false }"},
		{"-c", "codex_hooks=false"}, {"-c", "use_legacy_landlock=true"}, {"--config=features.hooks=false"},
		{"-c", `hooks.state."/p/hooks.json:pre_tool_use:0:0".enabled=false`}, {"-c", "hooks={}"},
		{"-c", `sandbox_workspace_write.writable_roots=["/"]`}, {"-c", `sandbox_workspace_write={writable_roots=["/"]}`},
		{"-c", "sandbox_workspace_write.network_access=true"}, {"-c", `developer_instructions="other"`},
		{"-c", "tui={features={hooks=false}}"}, {"-c", "tui=[{hooks=1}]"}, {"-c", "tui={x=[1,"},
		{"-c", `projects."/".trust_level="trusted"`}, {"-c", "permissions={}"}, {"-c", `web_search="live"`},
		{"-cmodel_instructions_file=/tmp/x"}, {"-c", "approval_policy = never"}, {"-c", `approval_policy="never"`},
		{"-c", "approvals_reviewer=auto_review"}, {"-c", "profile=yolo"}, {"-c", "project_doc_max_bytes=1"},
		{"-c", "model"}, {"-c", "=x"}, {"-c"}, {"-c", "-sdanger-full-access"},
		{"-anever"}, {"-a=never"}, {"--ask-for-approval", "never"}, {"--enable", "x"}, {"--disable", "x"},
		{"-C", "/"}, {"-C/"}, {"--cd=/"}, {"--worktree"}, {"--profile", "yolo"}, {"-pyolo"}, {"--profile=yolo"},
		{"--approve-for-me"}, {"--not-so-yolo"}, {"--image", "/x.png"}, {"-i/x.png"}, {"--search"},
		{"exec"}, {"resume", "x"}, {"a prompt"}, {"--", "--yolo"}, {"-"}, {"-x"}, {"--unknown"}, {"-h"}, {"--no-daemon=false"},
		// The sandbox is the native controls': a base -s, even a safe one,
		// would be given twice, which clap refuses (no start at all).
		{"-s", "read-only"}, {"-s", "workspace-write"}, {"-sread-only"}, {"--sandbox=workspace-write"},
	} {
		r := req
		r.Base.Args, r.ProviderArgs = bad, sandbox
		if _, err := a.BuildLaunch(r); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	r0 := req
	r0.Base.Args = []string{"-s", "workspace-write"}
	if _, err := a.BuildLaunch(r0); err == nil || !strings.Contains(err.Error(), "native controls of the class only") {
		t.Errorf("a base -s: %v; want the error that names the native controls", err)
	}
	// The engine's arguments are checked the same way, with the keys of the
	// layer and of the native controls besides.
	for _, bad := range [][]string{{"exec"}, {"-c", "features.hooks=false"}, {"--yolo"}, {"-sdanger-full-access"}, {"-s", "read-only"}} {
		r := req
		r.Base.Args, r.ProviderArgs = nil, append(slices.Clone(sandbox), bad...)
		if _, err := a.BuildLaunch(r); err == nil {
			t.Errorf("engine args %q were accepted", bad)
		}
	}
	r := req
	r.Base.Args = []string{"-mo4", "-c", `model_reasoning_effort="high"`, "-c", "tui={animations=false}", "--no-alt-screen"}
	r.ProviderArgs = []string{"-sworkspace-write", "-c", `developer_instructions="x"`, "-c", `sandbox_workspace_write.writable_roots=["/m"]`, "--search"}
	if l, err := a.BuildLaunch(r); err != nil || l.Args[len(l.Args)-1] != "features.hooks=true" || slices.Contains(l.Args, "gpt-5.5-codex") {
		t.Errorf("a base with the model and the display, and the engine's keys: %q, %v", l.Args, err)
	}
	r = req
	r.Base.Args, r.ProviderArgs = nil, nil
	if _, err := a.BuildLaunch(r); err == nil {
		t.Error("a start without the sandbox was accepted")
	}
	r.ProviderArgs, r.Profile, r.Env = sandbox, "", nil
	if _, err := a.BuildLaunch(r); err == nil {
		t.Error("a start without the account profile was accepted")
	}
}
