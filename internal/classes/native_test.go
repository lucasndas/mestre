package classes_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
)

// translate gives the native controls of the three providers for the rules
// of a character of this machine, with each file merged into nothing.
func translate(t testing.TB, r *classes.Rules) map[providers.ID]native {
	t.Helper()
	m := newMachine()
	reg := providers.NewRegistry(providers.Options{})
	out := map[providers.ID]native{}
	for _, id := range []providers.ID{providers.ClaudeCode, providers.Codex, providers.Antigravity} {
		a, err := reg.For(id)
		if err != nil {
			t.Fatal(err)
		}
		tr, ok := providers.NativeOf(a)
		if !ok {
			t.Fatalf("%s has no native translation", id)
		}
		ch := classes.NativeCharacter{AgentID: agentID, Provider: id, Worktree: m.wt, ProfileHome: filepath.Join(m.accounts, "agy-1")}
		nc, err := tr.Native(classes.NativePolicy(r, ch, providers.NativeFallbacks{}))
		if err != nil {
			t.Fatal(err)
		}
		n := native{args: nc.Args, files: map[string]string{}}
		for _, f := range nc.Files {
			data, err := f.Merge(nil)
			if err != nil {
				t.Fatal(err)
			}
			n.files[f.Path] = string(data)
		}
		out[id] = n
	}
	return out
}

type native struct {
	args  []string
	files map[string]string
}

// claudeRules are permissions.allow and permissions.deny of the --settings
// file.
func (n native) claudeRules(t testing.TB) (allow, deny []string) {
	t.Helper()
	path := providers.ClaudeSettingsPath(newMachine().wt, agentID)
	var s struct {
		Permissions struct{ Allow, Deny []string } `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(n.files[path]), &s); err != nil {
		t.Fatalf("settings %q: %v", n.files[path], err)
	}
	return s.Permissions.Allow, s.Permissions.Deny
}

// claudePath is a path of this machine in the form of the Claude path rules.
func claudePath(p string) string {
	s := filepath.ToSlash(p)
	if len(s) >= 2 && s[1] == ':' {
		s = "/" + strings.ToLower(s[:1]) + s[2:]
	}
	return "/" + s
}

// CA-NATIVO-01
func TestTheQAClassBecomesTheNativeControlsOfTheThreeProviders(t *testing.T) {
	t.Parallel()
	r := rules(t, "version: 1\nfiles: {write: [\"tests/**\"]}\nnetwork: {internet: false}\n")
	n := translate(t, r)
	allow, deny := n[providers.ClaudeCode].claudeRules(t)
	if e := "Edit(" + claudePath(newMachine().wt) + "/tests/**)"; !slices.Contains(allow, e) {
		t.Fatalf("claude allow %v lacks %s", allow, e)
	}
	if !slices.Contains(deny, "Bash(git push *)") {
		t.Fatalf("claude deny %v lacks Bash(git push *)", deny)
	}
	// Codex without internet: on Linux the network of the sandbox stays on
	// for the local channel and only the hook blocks the internet; on
	// Windows it is off (docs/provedores/codex.md#canal-local-dentro-do-sandbox).
	codex, network := n[providers.Codex].args, "sandbox_workspace_write.network_access=true"
	if runtime.GOOS == "windows" {
		network = "sandbox_workspace_write.network_access=false"
	}
	if !slices.Contains(codex, "workspace-write") || codex[slices.Index(codex, "workspace-write")-1] != "-s" ||
		!slices.Contains(codex, network) || slices.Contains(codex, "--search") {
		t.Fatalf("codex args %v; want -s workspace-write, %s and no hosted search", codex, network)
	}
	// Antigravity without internet: --sandbox only on Windows; on Linux the
	// namespaces it needs are restricted for mestre-agente, so it runs
	// without it and the internet is blocked only by the dispatcher
	// (docs/provedores/antigravity.md#flags).
	if got, wantSandbox := slices.Contains(n[providers.Antigravity].args, "--sandbox"), runtime.GOOS == "windows"; got != wantSandbox {
		t.Fatalf("antigravity args %v; --sandbox = %v, want %v", n[providers.Antigravity].args, got, wantSandbox)
	}
}

// CA-NATIVO-02
func TestAskAndToolsNeverBecomeNativeRules(t *testing.T) {
	t.Parallel()
	r := rules(t, `version: 1
files: {write: ["**"]}
commands: {allow: ["npm test"], ask: ["npm install *"], default: ask}
tools: {allow: ["mcp__playwright__*"]}
network: {internet: true}
`)
	for id, n := range translate(t, r) {
		all := strings.Join(n.args, " ")
		for _, f := range n.files {
			all += "\n" + f
		}
		for _, s := range []string{"npm install", "mcp__playwright", "mcp__"} {
			if strings.Contains(all, s) {
				t.Errorf("%s: the native controls carry %q:\n%s", id, s, all)
			}
		}
	}
}

// CA-NATIVO-03
func TestADecreeRegeneratesTheSettingsOfABusyClaudeAndRestartsItWhenFree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const class = "version: 1\nfiles: {write: [\"**\"]}\ncommands: {allow: [\"make *\"], deny: [\"make deploy *\"], default: ask}\n"
	files := &memFiles{m: map[string][]byte{}}
	busy := &activity{busy: true}
	restarts := &restarter{}
	ns := &classes.NativeSync{
		Adapters: providers.NewRegistry(providers.Options{}), Files: files,
		Restarts: &classes.RestartQueue{Restarter: restarts, Activity: busy},
	}
	ch := classes.NativeCharacter{AgentID: agentID, Provider: providers.ClaudeCode, Worktree: newMachine().wt}
	settings := providers.ClaudeSettingsPath(ch.Worktree, agentID)

	args, err := ns.Prepare(ctx, ch, rules(t, class))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(args, "--settings") || !slices.Contains(args, settings) {
		t.Fatalf("args %v lack --settings %s", args, settings)
	}
	before := files.get(settings)
	if !strings.Contains(before, `"Bash(make deploy *)"`) {
		t.Fatalf("settings before the decree:\n%s", before)
	}

	decree := classes.Decree{ID: 12, Level: classes.LevelAdmin, Effect: classes.Effect{Kind: classes.EffectPermission, Allow: &classes.Grant{Commands: []string{"make deploy *"}}}}
	changed, err := ns.Update(ctx, ch, rules(t, class, decree))
	if err != nil || !changed {
		t.Fatalf("update = %v, %v; want a change", changed, err)
	}
	if after := files.get(settings); after == before || strings.Contains(after, "make deploy") {
		t.Fatalf("settings were not regenerated without the native deny:\n%s", after)
	}
	if restarts.count() != 0 || !ns.Restarts.Pending(agentID) {
		t.Fatalf("restarts %d, pending %v; want the restart waiting for the end of the turn", restarts.count(), ns.Restarts.Pending(agentID))
	}
	busy.set(false)
	if ran, err := ns.Restarts.Free(ctx, agentID); err != nil || !ran {
		t.Fatalf("free = %v, %v", ran, err)
	}
	if restarts.count() != 1 || ns.Restarts.Pending(agentID) {
		t.Fatalf("restarts %d; want one restart with resume", restarts.count())
	}
	if ran, _ := ns.Restarts.Free(ctx, agentID); ran || restarts.count() != 1 {
		t.Fatal("a second end of turn restarted again")
	}
	// The restart builds the launch again; rules that change nothing native
	// then restart nothing.
	if _, err := ns.Prepare(ctx, ch, rules(t, class, decree)); err != nil {
		t.Fatal(err)
	}
	if changed, err := ns.Update(ctx, ch, rules(t, class, decree)); err != nil || changed || ns.Restarts.Pending(agentID) {
		t.Fatalf("the same rules again: changed %v, %v", changed, err)
	}
}

// CA-NATIVO-04
func TestTheAntigravityAgentFileOffersNoWriteOrWebToolsToAClassWithout(t *testing.T) {
	t.Parallel()
	r := rules(t, "version: 1\nfiles: {write: []}\nnetwork: {internet: false}\n")
	path := providers.AntigravityAgentPath(newMachine().wt, agentID)
	file := translate(t, r)[providers.Antigravity].files[path]
	var tools []string
	for _, l := range strings.Split(file, "\n") {
		if v, ok := strings.CutPrefix(l, "tools: "); ok {
			if err := json.Unmarshal([]byte(v), &tools); err != nil {
				t.Fatalf("tools %q: %v", v, err)
			}
		}
	}
	if !slices.Contains(tools, "run_command") || !slices.Contains(tools, "view_file") {
		t.Fatalf("tools %v; want the shell and read tools", tools)
	}
	for _, name := range tools {
		for _, banned := range []string{"write_to_file", "replace_file_content", "multi_replace_file_content", "search_web", "read_url_content", "browser_"} {
			if strings.HasPrefix(name, banned) {
				t.Fatalf("tools %v offer %s", tools, name)
			}
		}
	}
}

// CA-NATIVO-05
func TestAnAskToolStaysInTheAntigravityAgentFile(t *testing.T) {
	t.Parallel()
	path := providers.AntigravityAgentPath(newMachine().wt, agentID)
	tools := func(class string) []string {
		t.Helper()
		var out []string
		for _, l := range strings.Split(translate(t, rules(t, class))[providers.Antigravity].files[path], "\n") {
			if v, ok := strings.CutPrefix(l, "tools: "); ok {
				if err := json.Unmarshal([]byte(v), &out); err != nil {
					t.Fatalf("tools %q: %v", v, err)
				}
			}
		}
		return out
	}
	// generate_image is a native tool of kind other that matches no pattern.
	const class = "version: 1\ntools: {allow: [\"mcp__playwright__*\"]}\n"
	if got := tools(class); !slices.Contains(got, "generate_image") {
		t.Fatalf("without tools.default: tools %v; want generate_image, which asks", got)
	}
	if got := tools("version: 1\ntools: {allow: [\"mcp__playwright__*\"], default: deny}\n"); slices.Contains(got, "generate_image") {
		t.Fatalf("with tools.default deny: tools %v still offer generate_image", got)
	}
}

func TestANativeBlockADecreeOrExceptionFreesIsLifted(t *testing.T) {
	t.Parallel()
	m := newMachine()
	const class = "version: 1\nfiles: {write: [], deny: [\"secrets/**\"]}\ncommands: {deny: [\"curl *\", \"git push *\"]}\nnetwork: {internet: false}\n"
	ch := classes.NativeCharacter{AgentID: agentID, Worktree: m.wt}
	p := classes.NativePolicy(rules(t, class), ch, providers.NativeFallbacks{})
	if !p.NoWrite || !p.Offline || len(p.DenyFiles) != 1 || len(p.DenyCommands) != 2 {
		t.Fatalf("policy without decrees: %+v", p)
	}
	freed := rules(t, class,
		classes.Decree{ID: 1, Level: classes.LevelOwner, Effect: classes.Effect{Kind: classes.EffectPermission, Allow: &classes.Grant{Commands: []string{"git push *"}, Domains: []string{"example.com"}}}},
		classes.Decree{ID: 2, Kind: classes.DecreeException, Level: classes.LevelAdmin, Action: &classes.Action{Kind: providers.KindEdit, Paths: []string{"secrets/a.txt"}}},
	)
	p = classes.NativePolicy(freed, ch, providers.NativeFallbacks{})
	if p.NoWrite || p.Offline || p.AnyDomain || len(p.DenyFiles) != 0 || !slices.Equal(p.DenyCommands, []string{"curl *"}) {
		t.Fatalf("policy with the decree and the exception: %+v", p)
	}
	if !slices.Contains(p.FixedDenyCommands, "git push *") {
		t.Fatalf("a decree lifted a fixed rule: %v", p.FixedDenyCommands)
	}
}

func TestOSNativeFilesWriteOnlyWhatChanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "a", "settings.json")
	var f classes.OSNativeFiles
	merge := func(cur []byte) ([]byte, error) { return []byte("x"), nil }
	if c, err := f.Update(ctx, path, merge); err != nil || !c {
		t.Fatalf("first update = %v, %v", c, err)
	}
	if c, err := f.Update(ctx, path, merge); err != nil || c {
		t.Fatalf("same content = %v, %v; want unchanged", c, err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "x" {
		t.Fatalf("file = %q, %v", b, err)
	}
}

// memFiles is NativeFiles in memory.
type memFiles struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (f *memFiles) Update(_ context.Context, path string, merge func([]byte) ([]byte, error)) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.m[path]
	next, err := merge(cur)
	if err != nil {
		return false, err
	}
	if ok && string(cur) == string(next) {
		return false, nil
	}
	f.m[path] = next
	return true, nil
}

func (f *memFiles) get(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.m[path])
}

type activity struct {
	mu   sync.Mutex
	busy bool
}

func (a *activity) Busy(string) bool { a.mu.Lock(); defer a.mu.Unlock(); return a.busy }
func (a *activity) set(b bool)       { a.mu.Lock(); a.busy = b; a.mu.Unlock() }

type restarter struct {
	mu  sync.Mutex
	ids []string
}

func (r *restarter) RestartResume(_ context.Context, id string) error {
	r.mu.Lock()
	r.ids = append(r.ids, id)
	r.mu.Unlock()
	return nil
}

func (r *restarter) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.ids) }

// CA-NATIVO-06
func TestTheDefaultClassBecomesTheNativeControlsOfAPlainTerminal(t *testing.T) {
	t.Parallel()
	n := translate(t, rules(t, "version: 1\n"))
	allow, _ := n[providers.ClaudeCode].claudeRules(t)
	if e := "Edit(" + claudePath(newMachine().wt) + "/**)"; !slices.Contains(allow, e) {
		t.Fatalf("claude allow %v lacks %s", allow, e)
	}
	if args := strings.Join(n[providers.ClaudeCode].args, " "); strings.Contains(args, "WebFetch") || strings.Contains(args, "WebSearch") {
		t.Fatalf("claude args %q take the web tools away", args)
	}
	codex := n[providers.Codex].args
	if !slices.Contains(codex, "workspace-write") || !slices.Contains(codex, "sandbox_workspace_write.network_access=true") || !slices.Contains(codex, "--search") {
		t.Fatalf("codex args %v; want -s workspace-write, the network on and the hosted search", codex)
	}
	if slices.Contains(n[providers.Antigravity].args, "--sandbox") {
		t.Fatalf("antigravity args %v: --sandbox cuts the network of the default class", n[providers.Antigravity].args)
	}
	file := n[providers.Antigravity].files[providers.AntigravityAgentPath(newMachine().wt, agentID)]
	for _, tool := range []string{"write_to_file", "read_url_content"} {
		if !strings.Contains(file, tool) {
			t.Fatalf("antigravity tools lack %s:\n%s", tool, file)
		}
	}
}
