package providers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/pkg/clock"
)

func agy(t testing.TB, opts providers.Options) providers.Adapter {
	t.Helper()
	return providers.NewAntigravity(opts)
}

// mergedM is merged for the managed files of the provisioning.
func mergedM(t testing.TB, f providers.ManagedFile, cur string) string {
	t.Helper()
	return merged(t, providers.NativeFile(f), cur)
}

// topLevel lists the keys of a JSON object in file order, with the number of
// times each one appears.
func topLevel(t testing.TB, doc string) (keys []string, count map[string]int) {
	t.Helper()
	count = map[string]int{}
	dec := json.NewDecoder(strings.NewReader(doc))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not a JSON object: %v\n%s", err, doc)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, doc)
		}
		key := tok.(string)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, doc)
		}
		if count[key] == 0 {
			keys = append(keys, key)
		}
		count[key]++
	}
	return keys, count
}

func decode(t testing.TB, doc string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(doc), &m); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, doc)
	}
	return m
}

const stableBinary = "/opt/mestre/bin/mestre"

// CA-AGY-01
func TestHookFilesKeepOtherHooksAndWriteTheMestreGroupOnce(t *testing.T) {
	t.Parallel()
	prov := agy(t, providers.Options{})
	other := `{"lint-checker": {"PostToolUse": [{"matcher": "run_command", "hooks": [{"type": "command", "command": "./lint.sh", "timeout": 10}]}]}}`
	for _, goos := range []string{"linux", "windows"} {
		binary := stableBinary
		if goos == "windows" {
			binary = `C:\Mestre\bin\mestre.exe`
		}
		files, err := prov.HookFiles(providers.HookFilesRequest{Root: "/wt", OS: goos, Binary: binary})
		if err != nil || len(files) != 1 || files[0].Path != filepath.Join("/wt", ".agents", "hooks.json") {
			t.Fatalf("%s: files = %+v, %v", goos, files, err)
		}
		first := mergedM(t, files[0], other)
		second := mergedM(t, files[0], first) // HookFiles runs again over its own result
		if second != first {
			t.Fatalf("%s: the second run changed the file:\n%s\n---\n%s", goos, first, second)
		}
		keys, count := topLevel(t, second)
		if !slices.Equal(keys, []string{"lint-checker", "mestre"}) || count["mestre"] != 1 {
			t.Fatalf("%s: keys %v, count %v", goos, keys, count)
		}
		var want, got map[string]any
		_ = json.Unmarshal([]byte(other), &want)
		got = decode(t, second)
		if !jsonEqual(want["lint-checker"], got["lint-checker"]) {
			t.Fatalf("%s: the other hook changed: %v", goos, got["lint-checker"])
		}
		group, _ := got["mestre"].(map[string]any)
		if group["enabled"] != true || len(group) != 6 {
			t.Fatalf("%s: the mestre group = %v", goos, group)
		}
		// The five events: PreToolUse and PostToolUse in {matcher, hooks}
		// groups, the other three plain lists; the timeouts of the doc.
		for _, e := range []struct {
			event   string
			grouped bool
			timeout float64
		}{{"PreToolUse", true, 30}, {"PostToolUse", true, 60}, {"PreInvocation", false, 30}, {"PostInvocation", false, 30}, {"Stop", false, 60}} {
			list, _ := group[e.event].([]any)
			if len(list) != 1 {
				t.Fatalf("%s: %s = %v", goos, e.event, group[e.event])
			}
			h, _ := list[0].(map[string]any)
			if e.grouped {
				if h["matcher"] != "*" {
					t.Fatalf("%s: %s group = %v", goos, e.event, h)
				}
				hooks, _ := h["hooks"].([]any)
				h, _ = hooks[0].(map[string]any)
			}
			cmd, _ := h["command"].(string)
			if h["type"] != "command" || h["timeout"] != e.timeout || !strings.Contains(cmd, "hook --provider antigravity --event "+e.event) {
				t.Fatalf("%s: %s handler = %v", goos, e.event, h)
			}
			// agy runs the Windows line by cmd /c with the quoting of Go, which
			// breaks any " in it; and the branch of a character (with
			// MESTRE_AGENT_ID) never answers allow by itself: a guard that
			// fails (the quotes of agy 1.4.2) must not let the tool through
			// without the dispatcher.
			if goos == "windows" {
				_, character, _ := strings.Cut(cmd, ") else if exist ")
				if strings.Contains(cmd, `"`) || character == "" || strings.Contains(character, "allow") {
					t.Fatalf("windows: %s line has a quote, or its branch with MESTRE_AGENT_ID answers allow by itself: %s", e.event, cmd)
				}
			}
		}
	}
	// An invalid file is never rewritten, and a missing one is created.
	files, _ := prov.HookFiles(providers.HookFilesRequest{Root: "/wt", OS: "linux", Binary: stableBinary})
	for _, bad := range []string{`{"a": 1`, `[1]`, `{"a": 1} {"b": 2}`, `null`} {
		if _, err := files[0].Merge([]byte(bad)); !errors.Is(err, providers.ErrNativeFile) {
			t.Errorf("%q: err = %v, want ErrNativeFile", bad, err)
		}
	}
	if out := mergedM(t, files[0], ""); !strings.Contains(out, `"mestre"`) {
		t.Errorf("a new file:\n%s", out)
	}
	// A duplicated key collapses into one.
	if _, count := topLevel(t, mergedM(t, files[0], `{"mestre": {}, "x": 1, "mestre": {"enabled": false}}`)); count["mestre"] != 1 {
		t.Errorf("mestre appears %d times", count["mestre"])
	}
	for _, bad := range []providers.HookFilesRequest{
		{Root: "wt", OS: "linux", Binary: stableBinary},
		{Root: "/wt", OS: "linux", Binary: "mestre"},
		{Root: "/wt", OS: "windows", Binary: `C:\Mestre\100%\mestre.exe`},
		// Needs quotes and, not existing, has no 8.3 name.
		{Root: "/wt", OS: "windows", Binary: `C:\Program Files\Mestre-nao-instalado\mestre.exe`},
		{Root: "/wt", OS: "windows", Binary: `C:\Mestre\a(b)\mestre.exe`},
		// No drive or share: relative to the drive or to the folder of cmd.
		{Root: "/wt", OS: "windows", Binary: stableBinary},
		{Root: "/wt", OS: "windows", Binary: `\Mestre\bin\mestre.exe`},
		{Root: "/wt", OS: "windows", Binary: `C:foo\mestre.exe`},
		// Device and extended namespaces, an alternate data stream and ..
		{Root: "/wt", OS: "windows", Binary: `\\?\C:\Mestre\mestre.exe`},
		{Root: "/wt", OS: "windows", Binary: `\\.\C:\Mestre\mestre.exe`},
		{Root: "/wt", OS: "windows", Binary: `\\.\GLOBALROOT\Device\x\mestre.exe`},
		{Root: "/wt", OS: "windows", Binary: `C:\Mestre\a:b`},
		{Root: "/wt", OS: "windows", Binary: `C:\Mestre\mestre.exe:x`},
		{Root: "/wt", OS: "windows", Binary: `C:\Mestre\..\Other\mestre.exe`},
		{Root: "/wt", OS: "windows", Binary: `C:/Mestre/../mestre.exe`},
		{Root: "/wt", OS: "windows", Binary: `\\srv\share\..\m.exe`},
		{Root: "/wt", OS: "windows", Binary: `C:\Mestre\...\mestre.exe`},
		{Root: "/wt", OS: "windows", Binary: `C:\Mestre\.\mestre.exe`},
		{Root: "/wt", OS: "windows", Binary: `C:\Mestre\NUL`},
		{Root: "/wt", OS: "windows", Binary: `C:\Mestre\com1.exe`},
	} {
		if _, err := prov.HookFiles(bad); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
}

// A share names its server and folder: accepted, bare.
func TestTheWindowsHookLineOfAntigravityTakesAShare(t *testing.T) {
	t.Parallel()
	files, err := agy(t, providers.Options{}).HookFiles(providers.HookFilesRequest{Root: "/wt", OS: "windows", Binary: `\\srv\share\m.exe`})
	if err != nil {
		t.Fatal(err)
	}
	if doc := mergedM(t, files[0], ""); !strings.Contains(doc, `\\\\srv\\share\\m.exe`) {
		t.Errorf("the share is not in the line:\n%s", doc)
	}
}

// A Windows path written with forward slashes goes in the line of cmd with
// backslashes, bare.
func TestTheWindowsHookLineOfAntigravityTakesTheBinaryWithBackslashes(t *testing.T) {
	t.Parallel()
	files, err := agy(t, providers.Options{}).HookFiles(providers.HookFilesRequest{Root: "/wt", OS: "windows", Binary: "C:/Mestre/bin/mestre.exe"})
	if err != nil {
		t.Fatal(err)
	}
	doc := mergedM(t, files[0], "")
	want := quote(`if exist C:\Mestre\bin\mestre.exe (C:\Mestre\bin\mestre.exe hook`)
	if !strings.Contains(doc, want[1:len(want)-1]) || strings.Contains(doc, "C:/Mestre") {
		t.Errorf("the line does not call C:\\Mestre\\bin\\mestre.exe bare:\n%s", doc)
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// CA-AGY-02
func TestNormalizeHookReadsTheRecordedCamelCasePayloadByTheEventOfTheCommandLine(t *testing.T) {
	t.Parallel()
	a := providers.NewAntigravity(providers.Options{Resolve: noLinks})
	body := recorded(t, "antigravity", "PreToolUse")
	var raw map[string]any
	_ = json.Unmarshal(body, &raw)
	for _, k := range []string{"event", "eventName", "hookEventName", "hook_event_name"} {
		if _, has := raw[k]; has {
			t.Fatalf("the recorded payload names its event (%s)", k)
		}
	}
	ev, err := a.NormalizeHook(providers.HookInput{NativeEvent: "PreToolUse", Stdin: body, AgentID: "a1", TableID: "3-login", ReceivedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Event != providers.EventPreTool || ev.NativeEvent != "PreToolUse" || ev.Provider != providers.Antigravity {
		t.Fatalf("event = %s/%s of %s", ev.Event, ev.NativeEvent, ev.Provider)
	}
	// The PreToolUse of a run_command of agy 1.3.5, captured by the
	// conformance test.
	if ev.Tool == nil || ev.Tool.Kind != providers.KindShell || ev.Tool.Command != "echo ok > permitido/y.txt" || ev.Tool.NativeName != "run_command" {
		t.Fatalf("tool = %+v", ev.Tool)
	}
	if ev.SessionID != "ec6fe5e6-d0bd-4127-b1c3-94aaf50f88c2" || ev.Cwd != wantPath("/mconf/mesa") {
		t.Fatalf("session %q, cwd %q", ev.SessionID, ev.Cwd)
	}
}

// CA-AGY-03
func TestPreTrustAddsTheWorktreeOnceAndKeepsTheOtherKeys(t *testing.T) {
	t.Parallel()
	prov := agy(t, providers.Options{})
	home := t.TempDir()
	settings := providers.AntigravitySettingsPath(home)
	wt := filepath.Join(t.TempDir(), "mesa", "3-login")
	req := providers.PreTrustRequest{Profile: home, Worktree: wt + string(filepath.Separator), RepoRoot: filepath.Dir(wt), OS: runtime.GOOS}
	read := func() string {
		b, err := os.ReadFile(settings)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	testkit.WriteFiles(t, home, map[string]string{
		".gemini/antigravity-cli/settings.json": `{"allowNonWorkspaceAccess": true, "trustedWorkspaces": ["/other"], "toolPermission": "request-review"}`,
	})
	first, err := prov.PreTrust(req)
	if err != nil || !first.Changed || first.Path != settings || len(first.Trusted) != 1 || first.Trusted[0] != wt {
		t.Fatalf("first = %+v, %v", first, err)
	}
	afterFirst := read()
	second, err := prov.PreTrust(req)
	if err != nil || second.Changed {
		t.Fatalf("second = %+v, %v; want nothing to do", second, err)
	}
	if read() != afterFirst {
		t.Fatalf("the second run changed the file:\n%s\n---\n%s", afterFirst, read())
	}
	got := decode(t, afterFirst)
	list, _ := got["trustedWorkspaces"].([]any)
	if len(list) != 2 || list[0] != "/other" || list[1] != wt {
		t.Fatalf("trustedWorkspaces = %v, want [/other %s]: the exact path, not the repository root", list, wt)
	}
	if got["allowNonWorkspaceAccess"] != true || got["toolPermission"] != "request-review" {
		t.Fatalf("other keys lost: %v", got)
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(settings); err != nil || st.Mode().Perm()&0o044 != 0o044 {
			t.Fatalf("the settings.json must be readable by the account of the agents: %v, %v", st, err)
		}
	}

	// A file that already trusts it is left as it was, formatting included;
	// a missing file, a null list and a repeated key are handled.
	pretty := "{\n\t\"trustedWorkspaces\": [" + quote(wt) + "]\n}"
	for name, c := range map[string]struct{ in, want string }{
		"already trusted": {pretty, pretty},
	} {
		testkit.WriteFiles(t, home, map[string]string{".gemini/antigravity-cli/settings.json": c.in})
		if tr, err := prov.PreTrust(req); err != nil || tr.Changed || read() != c.want {
			t.Errorf("%s: %+v, %v, file %q", name, tr, err, read())
		}
	}
	for _, in := range []string{`{}`, `{"trustedWorkspaces": null}`, `{"trustedWorkspaces": [` + quote(wt) + `], "trustedWorkspaces": []}`} {
		testkit.WriteFiles(t, home, map[string]string{".gemini/antigravity-cli/settings.json": in})
		if _, err := prov.PreTrust(req); err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		keys, count := topLevel(t, read())
		l, _ := decode(t, read())["trustedWorkspaces"].([]any)
		if len(l) != 1 || l[0] != wt || count["trustedWorkspaces"] != 1 || len(keys) != 1 {
			t.Errorf("%q: file %s", in, read())
		}
	}
	fresh := t.TempDir()
	if tr, err := prov.PreTrust(providers.PreTrustRequest{Profile: fresh, Worktree: wt, OS: runtime.GOOS}); err != nil || !tr.Changed {
		t.Fatalf("a profile without settings.json: %+v, %v", tr, err)
	}
	// On Windows the same folder may differ by case.
	testkit.WriteFiles(t, home, map[string]string{".gemini/antigravity-cli/settings.json": `{"trustedWorkspaces": [` + quote(strings.ToUpper(wt)) + `]}`})
	if tr, err := prov.PreTrust(providers.PreTrustRequest{Profile: home, Worktree: wt, OS: "windows"}); err != nil || tr.Changed {
		t.Errorf("windows, a case-only difference: %+v, %v", tr, err)
	}
	// A file that is not ours to rewrite is an error and stays as it was.
	for _, bad := range []string{`{"trustedWorkspaces": `, `[]`, `{"trustedWorkspaces": "/x"}`, `{"trustedWorkspaces": [1]}`} {
		testkit.WriteFiles(t, home, map[string]string{".gemini/antigravity-cli/settings.json": bad})
		if _, err := prov.PreTrust(req); !errors.Is(err, providers.ErrNativeFile) || read() != bad {
			t.Errorf("%q: err = %v, file %q", bad, err, read())
		}
	}
	for _, bad := range []providers.PreTrustRequest{{Profile: "agy", Worktree: wt}, {Profile: home, Worktree: "wt"}} {
		if _, err := prov.PreTrust(bad); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
}

func launchRequest(extra func(*providers.LaunchRequest)) providers.LaunchRequest {
	req := providers.LaunchRequest{
		Base:    providers.Base{Name: "agy", Command: "agy", Env: []string{"BASE=1"}, Dir: "/work/mesa"},
		OS:      "linux",
		AgentID: "a1", TableID: "3-login", Worktree: "/work/mesa",
		Env: []string{"HOME=/profiles/agy"},
	}
	if extra != nil {
		extra(&req)
	}
	return req
}

// CA-AGY-04
func TestBuildLaunchHasTheAgentAndTheHOMEOfTheProfileAndNeverTheSkipFlag(t *testing.T) {
	t.Parallel()
	a := agy(t, providers.Options{})
	tests := []struct {
		name string
		req  providers.LaunchRequest
	}{
		{"plain", launchRequest(nil)},
		{"model and sandbox", launchRequest(func(r *providers.LaunchRequest) {
			r.Base.Args, r.ProviderArgs = []string{"--model", "gemini-3.8"}, []string{"--sandbox"}
		})},
		{"resume", launchRequest(func(r *providers.LaunchRequest) { r.ResumeSessionID = "b38a07d5-f7a3-4e27-b4d0-43e9110f60bb" })},
		{"windows", launchRequest(func(r *providers.LaunchRequest) {
			r.OS, r.Env, r.Worktree = "windows", []string{`HOME=C:\Mestre\contas\agy`}, `C:\Mestre\wt`
		})},
		{"one dash and =", launchRequest(func(r *providers.LaunchRequest) {
			r.Base.Args = []string{"-model=gemini-3.8", "-mode", "default", "--effort=high", "-disable-slash-commands"}
		})},
	}
	// No form of the skip flag reaches the command: one dash or two, with "="
	// or a value, or behind a positional that would end the flags.
	for _, skip := range [][]string{
		{"--dangerously-skip-permissions"}, {"-dangerously-skip-permissions"}, {"--dangerously-skip-permissions=true"},
		{"-dangerously-skip-permissions=true"}, {"x", "--dangerously-skip-permissions"}, {"--model", "m", "y"},
	} {
		for _, set := range []func(*providers.LaunchRequest){
			func(r *providers.LaunchRequest) { r.Base.Args = skip },
			func(r *providers.LaunchRequest) { r.ProviderArgs = skip },
		} {
			if l, err := a.BuildLaunch(launchRequest(set)); err == nil {
				t.Errorf("%q was accepted: %q", skip, l.Args)
			}
		}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, err := a.BuildLaunch(tt.req)
			if err != nil {
				t.Fatal(err)
			}
			i := slices.Index(l.Args, "--agent")
			if i < 0 || i+1 >= len(l.Args) || l.Args[i+1] != "mestre-a1" {
				t.Errorf("args %q lack --agent mestre-a1", l.Args)
			}
			if slices.Contains(l.Args, "--dangerously-skip-permissions") {
				t.Errorf("args %q have the forbidden flag", l.Args)
			}
			home, _ := lastEnv(l.Env, "HOME")
			if want, _ := lastEnv(tt.req.Env, "HOME"); home != want {
				t.Errorf("HOME = %q, want the profile's %q", home, want)
			}
			if tt.req.OS == "windows" {
				if up, _ := lastEnv(l.Env, "USERPROFILE"); up != home {
					t.Errorf("USERPROFILE = %q, want %q", up, home)
				}
			}
			if l.Verify == nil {
				t.Error("no check of the agent file before the start")
			}
		})
	}
}

func lastEnv(env []string, key string) (string, bool) {
	v, ok := "", false
	for _, kv := range env {
		if k, val, found := strings.Cut(kv, "="); found && k == key {
			v, ok = val, true
		}
	}
	return v, ok
}

// The base and the sheet never get the flags of the doc past the adapter, and
// a launch without the profile HOME, the worktree or a valid id does not exist.
func TestBuildLaunchRefusesWhatTheDocForbids(t *testing.T) {
	t.Parallel()
	a := agy(t, providers.Options{})
	for _, bad := range [][]string{
		{"--dangerously-skip-permissions"}, {"-p", "hi"}, {"--print"}, {"--prompt=hi"}, {"--add-dir", "/etc"}, {"--add-dir=/etc"},
		{"--agent", "other"}, {"--conversation", "x"}, {"--continue"}, {"-c"},
		// The forms of the M3 audit: one dash (Go flag reads -x as --x), "="
		// and the flags the first list missed, and a positional, after which
		// agy reads nothing as a flag, not even the --agent of the Mestre.
		{"-dangerously-skip-permissions"}, {"-dangerously-skip-permissions=true"}, {"--dangerously-skip-permissions=1"},
		{"-print", "x"}, {"-prompt=x"}, {"-add-dir", "/"}, {"-add-dir=/"}, {"-agent", "other"}, {"-agent=other"},
		{"-conversation", "x"}, {"-continue"}, {"--c"}, {"-c=true"},
		{"--remote-control"}, {"-remote-control"}, {"--remote-control=true"},
		{"--mode", "accept-edits"}, {"--mode=accept-edits"}, {"-mode", "plan"}, {"-mode=plan"}, {"--mode"},
		{"--sandbox=false"}, {"-sandbox=0"}, {"-i", "x"}, {"--output-format", "json"}, {"--new-project"},
		{"positional", "--dangerously-skip-permissions"}, {"--", "x"}, {"-"}, {"--model"}, {"--unknown"}, {"-h"}, {"---x"},
	} {
		for where, set := range map[string]func(*providers.LaunchRequest){
			"base":     func(r *providers.LaunchRequest) { r.Base.Args = bad },
			"provider": func(r *providers.LaunchRequest) { r.ProviderArgs = bad },
		} {
			if l, err := a.BuildLaunch(launchRequest(set)); err == nil {
				t.Errorf("%v in the %s args was accepted: %q", bad, where, l.Args)
			}
		}
	}
	for name, set := range map[string]func(*providers.LaunchRequest){
		"no HOME":       func(r *providers.LaunchRequest) { r.Env = nil },
		"relative HOME": func(r *providers.LaunchRequest) { r.Env = []string{"HOME=profiles"} },
		"no worktree":   func(r *providers.LaunchRequest) { r.Worktree = "" },
		"no id":         func(r *providers.LaunchRequest) { r.AgentID = "" },
		"id with path":  func(r *providers.LaunchRequest) { r.AgentID = "../x" },
		"id with a dot": func(r *providers.LaunchRequest) { r.AgentID = "a.b" },
	} {
		if _, err := a.BuildLaunch(launchRequest(set)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// fakeExec answers the /usage command with out and records what was run.
type fakeExec struct {
	out  []byte
	err  error
	last providers.Command
}

func (f *fakeExec) Run(_ context.Context, c providers.Command) ([]byte, error) {
	f.last = c
	return f.out, f.err
}

// CA-AGY-05
func TestReadUsageWindowsAreTheUseAndTheResetOfTheRecordedUsage(t *testing.T) {
	t.Parallel()
	fx := &fakeExec{out: recorded(t, "antigravity", "Usage")}
	prov := agy(t, providers.Options{Exec: fx.Run})
	u, err := prov.ReadUsage(providers.UsageRequest{
		AgentID: "a1", Command: "agy", OS: "windows", Env: []string{"HOME=" + filepath.FromSlash("/profiles/agy"), "MESTRE_AGENT_ID=a1", "MESTRE_ENDPOINT=unix:///x", "Mestre_Cli=/m", "PATH=/bin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []providers.LimitWindow{
		{Window: "5h", Bucket: "gemini-5h", UsedPct: 38, ResetsAt: time.Date(2026, 10, 14, 17, 0, 0, 0, time.UTC)},
		{Window: "weekly", Bucket: "gemini-weekly", UsedPct: 10, ResetsAt: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)},
		{Window: "5h", Bucket: "claude-gpt-5h", UsedPct: 75, ResetsAt: time.Date(2026, 10, 14, 16, 30, 0, 0, time.UTC)},
		{Window: "weekly", Bucket: "claude-gpt-weekly", UsedPct: 25, ResetsAt: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)},
	}
	if len(u.LimitWindows) != len(want) {
		t.Fatalf("windows = %+v", u.LimitWindows)
	}
	for i, w := range want {
		g := u.LimitWindows[i]
		if g.Window != w.Window || g.Bucket != w.Bucket || g.UsedPct != w.UsedPct || !g.ResetsAt.Equal(w.ResetsAt) {
			t.Errorf("window %d = %+v, want %+v (used = 1 − remaining_fraction, in percent)", i, g, w)
		}
	}
	if u.LimitsReached || u.InputTokens != 0 || u.OutputTokens != 0 {
		t.Errorf("usage = %+v: no window is empty and tokens per turn are not read", u)
	}
	// The command: print mode, JSON, the HOME of the profile, and nothing
	// that makes it a character.
	c := fx.last
	if c.Path != "agy" || !slices.Equal(c.Args, []string{"-p", "/usage", "--output-format", "json"}) {
		t.Errorf("command = %s %q", c.Path, c.Args)
	}
	if h, _ := lastEnv(c.Env, "HOME"); h != filepath.FromSlash("/profiles/agy") {
		t.Errorf("env %q lacks the HOME of the profile", c.Env)
	}
	if up, _ := lastEnv(c.Env, "USERPROFILE"); up != filepath.FromSlash("/profiles/agy") {
		t.Errorf("env %q lacks the USERPROFILE of the profile", c.Env)
	}
	if i := slices.IndexFunc(c.Env, func(kv string) bool { return strings.HasPrefix(strings.ToUpper(kv), "MESTRE_") }); i >= 0 || c.Dir != filepath.FromSlash("/profiles/agy") {
		t.Errorf("the command ran as a character: dir %q, env %q", c.Dir, c.Env)
	}

	// A window with nothing left.
	fx.out = recorded(t, "antigravity", "Usage-exhausted")
	u, err = prov.ReadUsage(providers.UsageRequest{Env: []string{"HOME=/profiles/agy"}})
	if err != nil || !u.LimitsReached || u.LimitWindows[0].UsedPct != 100 || u.LimitWindows[1].UsedPct != 60 {
		t.Errorf("exhausted: %+v, %v", u, err)
	}
	if fx.last.Path != "agy" {
		t.Errorf("the default command is %q", fx.last.Path)
	}
}

func TestReadUsageErrors(t *testing.T) {
	t.Parallel()
	req := providers.UsageRequest{Env: []string{"HOME=/profiles/agy"}}
	for name, c := range map[string]struct {
		out  string
		err  error
		env  []string
		want string
	}{
		"not JSON":               {out: "Usage: ...", want: "not the JSON of /usage"},
		"no windows":             {out: `{"command": {"data": {"groups": []}}}`, want: "no windows"},
		"no fraction":            {out: `{"command": {"data": {"groups": [{"buckets": [{"id": "x", "window": "5h"}]}]}}}`, want: "remaining_fraction"},
		"bad reset":              {out: `{"command": {"data": {"groups": [{"buckets": [{"id": "x", "window": "5h", "remaining_fraction": 1, "reset_time": "soon"}]}]}}}`, want: "RFC 3339"},
		"the command fails":      {err: errors.New("exit status 1"), want: "failed"},
		"no HOME of the profile": {out: "{}", env: []string{"PATH=/bin"}, want: "HOME"},
	} {
		fx := &fakeExec{out: []byte(c.out), err: c.err}
		prov := agy(t, providers.Options{Exec: fx.Run})
		r := req
		if c.env != nil {
			r.Env = c.env
		}
		if _, err := prov.ReadUsage(r); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
	// A reset time as seconds since the epoch is read too.
	u, err := providers.ParseAntigravityUsage([]byte(`{"command": {"data": {"groups": [{"buckets": [{"id": "x", "window": "weekly", "remaining_fraction": 0.5, "reset_time": 1792000000}]}]}}}`))
	if err != nil || u.LimitWindows[0].ResetsAt.Unix() != 1792000000 || u.LimitWindows[0].UsedPct != 50 {
		t.Errorf("epoch reset: %+v, %v", u, err)
	}
}

// profileFiles applies native files in order with the real writer of the
// engine, as the start of a character does.
func profileFiles(t testing.TB, groups ...[]providers.ManagedFile) {
	t.Helper()
	var w classes.OSNativeFiles
	for _, g := range groups {
		for _, f := range g {
			if _, err := w.Update(t.Context(), f.Path, f.Merge); err != nil {
				t.Fatalf("%s: %v", f.Path, err)
			}
		}
	}
}

// managed turns the native files into managed ones, the same pair.
func managed(nf []providers.NativeFile) []providers.ManagedFile {
	out := make([]providers.ManagedFile, len(nf))
	for i, f := range nf {
		out[i] = providers.ManagedFile(f)
	}
	return out
}

func writeMestreMD(t testing.TB, dir, text string) string {
	t.Helper()
	path := filepath.Join(dir, "MESTRE.md")
	testkit.WriteFiles(t, dir, map[string]string{"MESTRE.md": text})
	return path
}

// CA-AGY-07
func TestTheAgentFileIsNamedLikeTheAgentFlagAndTheStartFailsWithoutIt(t *testing.T) {
	t.Parallel()
	a := agy(t, providers.Options{})
	prov := a
	wt := t.TempDir()
	mestreMD := writeMestreMD(t, t.TempDir(), "Os decretos ativos valem acima de tudo.\r\n\r\nUse the mestre CLI.\r\n")
	l, err := a.BuildLaunch(launchRequest(func(r *providers.LaunchRequest) { r.Worktree = wt }))
	if err != nil {
		t.Fatal(err)
	}
	agentFlag := l.Args[slices.Index(l.Args, "--agent")+1]

	// The file does not exist: the start fails instead of opening the
	// default agent.
	if err := l.Verify(); !errors.Is(err, providers.ErrAgentFile) {
		t.Fatalf("no agent file: Verify = %v, want ErrAgentFile", err)
	}

	layer, err := prov.DeliverMestreLayer(providers.LayerRequest{AgentID: "a1", Worktree: wt, Path: mestreMD})
	if err != nil {
		t.Fatal(err)
	}
	if len(layer.Args) != 0 || layer.HookContext != "" || layer.FirstMessage != "" || len(layer.Files) != 1 {
		t.Fatalf("layer = %+v: the agent file alone carries the main channel", layer)
	}
	// The old file has the keys the Mestre never writes and a tools field of
	// the native controls, which the layer keeps.
	path := providers.AntigravityAgentPath(wt, "a1")
	testkit.WriteFiles(t, wt, map[string]string{
		".agents/agents/mestre-a1.md": "---\nname: other\npermissionMode: auto\nexcludeDefaultComponents: true\ntools: [\"view_file\"]\n---\nold body\n",
	})
	profileFiles(t, layer.Files)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	front, body, _ := strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---\n")
	for _, want := range []string{"name: " + agentFlag + "\n", "mainAgent: true\n", "subagent: false\n", `tools: ["view_file"]`} {
		if !strings.Contains(front+"\n", want) {
			t.Errorf("frontmatter lacks %q:\n%s", want, text)
		}
	}
	for _, never := range []string{"excludeDefaultComponents", "permissionMode"} {
		if strings.Contains(text, never) {
			t.Errorf("the agent file has %s:\n%s", never, text)
		}
	}
	if strings.Contains(body, "old body") || !strings.HasPrefix(body, "Os decretos ativos valem acima de tudo.\n\nUse the mestre CLI.") {
		t.Errorf("the body is not the MESTRE.md:\n%s", body)
	}
	if err := l.Verify(); err != nil {
		t.Errorf("Verify with the file = %v", err)
	}

	// The file of another name, or not the main agent, is the default agent
	// in disguise.
	for name, content := range map[string]string{
		"another name":              "---\nname: mestre-b2\nmainAgent: true\n---\nx\n",
		"not main":                  "---\nname: mestre-a1\nmainAgent: false\n---\nx\n",
		"no frontmatter":            "just text\n",
		"excluded factory sections": "---\nname: mestre-a1\nmainAgent: true\nexcludeDefaultComponents: true\n---\nx\n",
	} {
		testkit.WriteFiles(t, wt, map[string]string{".agents/agents/mestre-a1.md": content})
		if err := l.Verify(); !errors.Is(err, providers.ErrAgentFile) {
			t.Errorf("%s: Verify = %v, want ErrAgentFile", name, err)
		}
	}
}

// The layer and the native controls write the same file and converge in any
// order, each run changes nothing the second time, and the tools survive.
func TestTheLayerAndTheNativeToolsShareTheAgentFileInAnyOrder(t *testing.T) {
	t.Parallel()
	a := agy(t, providers.Options{})
	prov := a
	tr, _ := providers.NativeOf(a)
	mestreMD := writeMestreMD(t, t.TempDir(), "Mestre layer.\n")
	var results []string
	for _, layerFirst := range []bool{true, false} {
		wt := t.TempDir()
		nc, err := tr.Native(providers.NativePolicy{AgentID: "a1", Worktree: wt, ProfileHome: t.TempDir(), OS: "linux", NoWrite: true, Offline: true})
		if err != nil {
			t.Fatal(err)
		}
		layer, err := prov.DeliverMestreLayer(providers.LayerRequest{AgentID: "a1", Worktree: wt, Path: mestreMD})
		if err != nil {
			t.Fatal(err)
		}
		if layerFirst {
			profileFiles(t, layer.Files, managed(nc.Files[:1]), layer.Files)
		} else {
			profileFiles(t, managed(nc.Files[:1]), layer.Files, managed(nc.Files[:1]))
		}
		b, _ := os.ReadFile(providers.AntigravityAgentPath(wt, "a1"))
		results = append(results, string(b))
	}
	if results[0] != results[1] {
		t.Fatalf("the order changed the file:\n%s\n---\n%s", results[0], results[1])
	}
	if !strings.Contains(results[0], "tools: [") || strings.Contains(results[0], "write_to_file") || !strings.HasSuffix(results[0], "---\nMestre layer.\n") {
		t.Fatalf("agent file:\n%s", results[0])
	}
}

// The "Se falhar" paths of the layer, switched on by the conformance test.
func TestTheLayerFallbacks(t *testing.T) {
	t.Parallel()
	prov := agy(t, providers.Options{})
	mestreMD := writeMestreMD(t, t.TempDir(), "Mestre layer.\n")
	req := func(f providers.LayerFallbacks) providers.LayerRequest {
		return providers.LayerRequest{AgentID: "a1", Worktree: "/wt", Path: mestreMD, Fallbacks: f}
	}
	body := func(l providers.Layer) string {
		out := mergedM(t, l.Files[0], "")
		_, b, _ := strings.Cut(strings.TrimPrefix(out, "---\n"), "\n---\n")
		return b
	}
	main, _ := prov.DeliverMestreLayer(req(providers.LayerFallbacks{ReadRootAgents: true}))
	if b := body(main); !strings.HasPrefix(b, "Mestre layer.\n") || !strings.Contains(b, "read the AGENTS.md at the root of the workspace") {
		t.Errorf("read the root AGENTS.md first:\n%s", b)
	}
	hook, err := prov.DeliverMestreLayer(req(providers.LayerFallbacks{Channel: providers.LayerHook}))
	if err != nil || hook.HookContext != "Mestre layer.\n" || hook.FirstMessage != "" || strings.Contains(body(hook), "Mestre layer.") {
		t.Errorf("hook channel: %+v, %v", hook, err)
	}
	first, err := prov.DeliverMestreLayer(req(providers.LayerFallbacks{Channel: providers.LayerFirstMessage, ReadRootAgents: true}))
	if err != nil || !strings.HasPrefix(first.FirstMessage, "Mestre layer.\n") || !strings.Contains(first.FirstMessage, "AGENTS.md") || first.HookContext != "" {
		t.Errorf("first message channel: %+v, %v", first, err)
	}
	// Both keep the frontmatter, so --agent still finds the agent.
	for _, l := range []providers.Layer{hook, first} {
		if out := mergedM(t, l.Files[0], ""); !strings.Contains(out, "name: mestre-a1\n") || !strings.Contains(out, "mainAgent: true\n") {
			t.Errorf("frontmatter of a fallback:\n%s", out)
		}
	}
	for name, r := range map[string]providers.LayerRequest{
		"channel":  req(providers.LayerFallbacks{Channel: "smoke"}),
		"missing":  {AgentID: "a1", Worktree: "/wt", Path: filepath.Join(t.TempDir(), "none.md")},
		"id":       {AgentID: "a 1", Worktree: "/wt", Path: mestreMD},
		"worktree": {AgentID: "a1", Worktree: "wt", Path: mestreMD},
		"empty":    {AgentID: "a1", Worktree: "/wt", Path: writeMestreMD(t, t.TempDir(), " \n")},
	} {
		if _, err := prov.DeliverMestreLayer(r); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The screen table: Antigravity has no permission_request hook, so the
// native permission screen is answered with the decision recorded for the
// call (deny without one), and the review of an artifact or plan escalates.
func TestNativeScreens(t *testing.T) {
	t.Parallel()
	const permission = "run_command wants to run `git status`\n❯ 1. Yes, allow tool call\n  2. Yes, and always allow in this conversation\n  3. No, deny tool call"
	tests := []struct {
		name     string
		screen   string
		decision map[string]string
		typed    []string
		blocked  bool
	}{
		{"permission, allowed", permission, map[string]string{"permission": "allow"}, []string{"\r"}, false},
		{"permission, denied", permission, map[string]string{"permission": "deny"}, []string{"\x1b"}, false},
		{"permission, no decision denies", permission, nil, []string{"\x1b"}, false},
		{"permission, unknown choice escalates", permission, map[string]string{"permission": "maybe"}, nil, true},
		{"artifact review escalates", "Review artifact implementation_plan.md", nil, nil, true},
		{"plan review escalates", "Review plan before continuing", nil, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clk := clock.NewFake(at)
			w := &world{decision: tt.decision}
			sup := supervisor(clk, providers.NewAntigravity(providers.Options{}), w, nil)
			defer sup.Close()
			sup.Start(t.Context())
			sup.Screen(tt.screen, true)
			sup.Screen(tt.screen, true) // the same screen again: acted on once
			s := w.snapshot()
			if !slices.Equal(s.typed, tt.typed) {
				t.Errorf("typed = %q, want %q", s.typed, tt.typed)
			}
			if got := len(s.blocked) > 0 && s.blocked[len(s.blocked)-1] == "a1/input"; got != tt.blocked {
				t.Errorf("blocked = %v, want %v", s.blocked, tt.blocked)
			}
		})
	}
	a := providers.NewAntigravity(providers.Options{})
	if r := a.Screens(providers.ScreenInput{Text: "Antigravity\n\n> ", Stable: true}); r.State != providers.StateReady || r.Known != nil {
		t.Errorf("the idle prompt = %+v", r)
	}
}

// The allow of a shell command repeats the action in permissionOverrides, so
// the native permission screen does not ask again; the neutral answer, the
// denials and the commands that would not parse carry none.
func TestAllowCarriesTheDecidedCommandInPermissionOverrides(t *testing.T) {
	t.Parallel()
	a := agy(t, providers.Options{})
	shell := func(cmd string) *providers.Tool {
		return &providers.Tool{NativeName: "run_command", Kind: providers.KindShell, Command: cmd}
	}
	for _, c := range []struct {
		name  string
		reply providers.Reply
		want  []any
	}{
		{"shell allow", providers.Reply{Kind: providers.ReplyAllow, Tool: shell("npm test")}, []any{"command(npm test)"}},
		{"a wildcard is not repeated", providers.Reply{Kind: providers.ReplyAllow, Tool: shell("rm *.log")}, nil},
		{"a parenthesis is not repeated", providers.Reply{Kind: providers.ReplyAllow, Tool: shell("echo $(date)")}, nil},
		{"not a shell tool", providers.Reply{Kind: providers.ReplyAllow, Tool: &providers.Tool{Kind: providers.KindWrite, Paths: []string{"/wt/a"}}}, nil},
		{"no tool", providers.Reply{Kind: providers.ReplyAllow}, nil},
		{"neutral", providers.Reply{Kind: providers.ReplyNeutral, Tool: shell("npm test")}, nil},
	} {
		out := a.FormatResponse("PreToolUse", c.reply)
		m := decodeObject(t, out)
		got, _ := m["permissionOverrides"].([]any)
		if out.Exit != 0 || m["decision"] != "allow" || !slices.Equal(got, c.want) {
			t.Errorf("%s: exit %d, stdout %s", c.name, out.Exit, out.Stdout)
		}
	}
	m := decodeObject(t, a.FormatResponse("PreToolUse", providers.Reply{Kind: providers.ReplyDeny, Reason: "no", Tool: shell("npm test")}))
	if _, has := m["permissionOverrides"]; has || m["decision"] != "deny" {
		t.Errorf("a denial: %v", m)
	}
}

// "Se falhar" of the bracketed paste: a text with line breaks is not typed.
func TestWithoutBracketedPasteAMultiLineTextGoesThroughTheFile(t *testing.T) {
	t.Parallel()
	nopaste := providers.NewAntigravity(providers.Options{AntigravityNoPaste: true})
	if _, err := nopaste.TypeMessage("linha 1\nlinha 2"); !errors.Is(err, providers.ErrTooLong) {
		t.Errorf("multi-line without paste: err = %v, want ErrTooLong", err)
	}
	if b, err := nopaste.TypeMessage("uma linha"); err != nil || string(b) != "\x1b[200~uma linha\x1b[201~\r" {
		t.Errorf("one line = %q, %v", b, err)
	}
}
