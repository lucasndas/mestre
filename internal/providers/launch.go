package providers

import (
	"fmt"
	"slices"
	"strings"
)

// Base is the base of a terminal already resolved for the machine
// (docs/orquestracao/bases-e-fichas.md#base): the command and arguments of
// the machine's system, its extra variables and the start folder.
type Base struct {
	Name    string
	Command string
	Args    []string
	Env     []string // KEY=VALUE
	Dir     string
}

// LaunchRequest is what BuildLaunch receives.
type LaunchRequest struct {
	Base Base
	// OS is the system of the machine ("linux", "windows").
	OS string
	// AgentID is the character (MESTRE_AGENT_ID); TableID its table
	// (MESTRE_MESA).
	AgentID, TableID string
	// Worktree is the character's worktree, absolute: where the provider
	// files of the character live (Antigravity: the agent file the start
	// checks).
	Worktree string
	// ResumeSessionID is the saved session to resume; "" starts a new one.
	ResumeSessionID string
	// NewSessionID is the id the engine generated for a new Claude Code
	// session (--session-id); the other providers report theirs in the
	// hooks.
	NewSessionID string
	// ProviderArgs are the flags the engine adds for the provider: the
	// Mestre layer (DeliverMestreLayer) and the native rules of the class.
	// The same ones go on a resume.
	ProviderArgs []string
	// Env are the variables the engine adds: the account profile's and the
	// rest of MESTRE_*.
	Env []string
	// Profile is the provider folder of the account profile; the
	// adapter puts it in the provider's variable (CODEX_HOME).
	Profile string
	// Model is the class's preferred model, if it has one.
	Model string
}

// Launch is the command of a character's terminal.
type Launch struct {
	Path string
	Args []string
	Env  []string
	Dir  string
	// SessionID is the session the command starts or resumes, when known
	// before the first hook (Claude Code; a resume).
	SessionID string
	// Resumed is true for the resume command of the table of
	// docs/provedores/adaptador.md#retomada-de-sessão.
	Resumed bool
	// Verify, when set, is the check the engine runs right before it starts
	// the command, after the provider files are written: an error means
	// the start fails instead of opening a different agent.
	Verify func() error
}

// newLaunch fills what every adapter does the same way: base command, the
// variables of the base, of the engine and the two the hooks read. A
// variable of the base that changes how the provider is kept safe is
// refused (refuseBaseEnv), at every start, whatever was checked when the
// base was saved.
func newLaunch(p ID, req LaunchRequest) (Launch, error) {
	if req.Base.Command == "" {
		return Launch{}, fmt.Errorf("base %q: no command", req.Base.Name)
	}
	if err := refuseBaseEnv(p, req.Base.Env); err != nil {
		return Launch{}, fmt.Errorf("base %q: %w", req.Base.Name, err)
	}
	env := slices.Concat(req.Base.Env, req.Env)
	if req.AgentID != "" {
		env = append(env, "MESTRE_AGENT_ID="+req.AgentID)
	}
	if req.TableID != "" {
		env = append(env, "MESTRE_MESA="+req.TableID)
	}
	return Launch{Path: req.Base.Command, Env: env, Dir: req.Base.Dir}, nil
}

// ValidSessionID says whether id can be a provider session id: the UUIDs the
// three providers use, or anything of the same shape (letters, digits, dot,
// colon, underscore and dash, starting with a letter or digit, up to 128).
// The id travels as an argument of the resume command, so a leading dash
// (an option) or a space never passes.
func ValidSessionID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case i > 0 && (c == '.' || c == ':' || c == '_' || c == '-'):
		default:
			return false
		}
	}
	return true
}

// checkResumeID refuses a resume id that is not a session id.
func checkResumeID(p ID, id string) error {
	if id != "" && !ValidSessionID(id) {
		return fmt.Errorf("%s: %q is not a session id; refusing to resume", p, id)
	}
	return nil
}

// refuseLatest refuses the "most recent session" options: several
// characters share a folder, so only a resume by id is safe.
func refuseLatest(p ID, args []string, latest ...string) error {
	for _, a := range args {
		if slices.Contains(latest, a) {
			return fmt.Errorf("%s: option %s resumes the most recent session of the folder; the Mestre resumes by id", p, a)
		}
	}
	return nil
}

// The variables a base may not set (docs/orquestracao/bases-e-fichas.md#regras-e-erros).
// The names are compared in upper case: Windows does not tell case apart.
// A name ending in "_" is a prefix.
var (
	// baseEnvMestre are the Mestre's own, for every terminal: the engine
	// sets them.
	baseEnvMestre = []string{"MESTRE_"}
	// baseEnvAgent run code of the base's choosing inside the agent or
	// every shell it opens, before any hook (the dynamic loader, Node,
	// non-interactive shells), or change the git configuration the engine
	// pins (GIT_CONFIG_GLOBAL of the character): for every provider.
	baseEnvAgent = []string{"LD_PRELOAD", "LD_AUDIT", "LD_LIBRARY_PATH", "DYLD_", "NODE_OPTIONS", "BASH_ENV", "ENV",
		"GIT_CONFIG", "GIT_CONFIG_", "GIT_EXEC_PATH"}
	// baseEnvProvider are the provider's own that skip or move its hooks,
	// permissions, configuration or sandbox, or wrap the commands it runs.
	baseEnvProvider = map[ID][]string{
		ClaudeCode: {
			"CLAUDE_CONFIG_DIR", "CLAUDE_CODE_SIMPLE", "CLAUDE_CODE_SAFE_MODE", "CLAUDE_CODE_RESTRICTED",
			"CLAUDE_CODE_SHELL", "CLAUDE_CODE_SHELL_PREFIX", "CLAUDE_ENV_FILE", "CLAUDE_CODE_PROCESS_WRAPPER",
			"CLAUDE_CODE_GIT_BASH_PATH", "CLAUDE_CODE_USE_POWERSHELL_TOOL", "CLAUDE_CODE_PLUGIN_DIRS",
			"CLAUDE_CODE_PLUGIN_SEED_DIR", "CLAUDE_CODE_PLUGIN_CACHE_DIR", "CLAUDE_CODE_PROJECT_DIR_NAME",
			"CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_MESSAGING_TOKEN",
			"CLAUDE_CODE_AUTO_MODE_SERVER", "CLAUDE_CODE_ENABLE_AUTO_MODE", "CLAUDE_AFK_TIMEOUT_MS", "CLAUDE_CODE_ENABLE_CFC",
			"CLAUDE_CODE_SESSIONEND_HOOKS_TIMEOUT_MS", "CLAUDE_CODE_STOP_HOOK_BLOCK_CAP", "CLAUDE_CODE_TMPDIR", "SHELL",
		},
		Codex:       {"CODEX_"},
		Antigravity: {"HOME", "USERPROFILE"},
	}
	// claudeDisableAllowed are the CLAUDE_CODE_DISABLE_* a base may set:
	// they change only how the CLI shows things or talks to the model. Any
	// other one (the removal checks, the hooks of the permission prompts,
	// the managed skills...) is refused.
	claudeDisableAllowed = []string{
		"TERMINAL_TITLE", "MOUSE", "MOUSE_CLICKS", "ALTERNATE_SCREEN", "VIRTUAL_SCROLL", "FEEDBACK_SURVEY",
		"NONESSENTIAL_TRAFFIC", "EXPERIMENTAL_BETAS", "1M_CONTEXT", "ADAPTIVE_THINKING", "THINKING", "FAST_MODE",
		"LEGACY_MODEL_REMAP", "STRUCTURED_OUTPUTS", "NONSTREAMING_FALLBACK",
	}
)

// refuseBaseEnv refuses the variables of a base that change how the
// provider p is kept safe: the Mestre's, and for an agent the ones that run
// code before the hooks or that skip or move the provider's own hooks,
// permissions, configuration or sandbox. The engine's (the account profile,
// MESTRE_*) come in LaunchRequest.Env, not here.
func refuseBaseEnv(p ID, env []string) error {
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		k := strings.ToUpper(key)
		bad := matchEnv(k, baseEnvMestre)
		if p != Null {
			bad = bad || matchEnv(k, baseEnvAgent) || matchEnv(k, baseEnvProvider[p])
		}
		if d, ok := strings.CutPrefix(k, "CLAUDE_CODE_DISABLE_"); ok && p == ClaudeCode && !slices.Contains(claudeDisableAllowed, d) {
			bad = true
		}
		if bad {
			who := string(p)
			if p == Null {
				who = "the terminal"
			}
			return fmt.Errorf("the variable %s may not come from the base: it is the engine's, or it changes how %s is kept safe (docs/orquestracao/bases-e-fichas.md#regras-e-erros)", key, who)
		}
	}
	return nil
}

// matchEnv reports whether the upper-case name k is one of names, where a
// name ending in "_" is a prefix.
func matchEnv(k string, names []string) bool {
	return slices.ContainsFunc(names, func(n string) bool {
		if strings.HasSuffix(n, "_") {
			return strings.HasPrefix(k, n)
		}
		return k == n
	})
}
