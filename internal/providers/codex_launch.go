package providers

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// The start flags of the Codex base (docs/provedores/codex.md#flags): the
// adapter adds the mandatory ones the base and the engine did not give
// (--no-daemon, -a on-request, -m with the class's model, and
// features.hooks on) and refuses the forbidden ones, and the start without
// the sandbox, which Native gives (-s). Codex fails open on a hook failure,
// so the sandbox is the barrier left; the base never runs without it.

// codexOption is one option of the Codex command line (codex-rs, commit
// 2f05c0a: tui/src/cli.rs, utils/cli/src/shared_options.rs and
// config_override.rs), parsed the way clap does: --name value, --name=value,
// and the short form -x value, -xvalue or -x=value (every short option of
// Codex but -h and -V takes a value). refused, when set, is why no start
// has it; engine marks the ones only the engine's arguments give.
type codexOption struct {
	value   bool
	refused string
	engine  bool
}

var codexLong = map[string]codexOption{
	"config":           {value: true},
	"sandbox":          {value: true},
	"ask-for-approval": {value: true},
	"model":            {value: true},
	"no-daemon":        {},
	"no-alt-screen":    {},
	"strict-config":    {},
	"oss":              {},
	"local-provider":   {value: true},
	// The hosted search does not go through a hook: only the native
	// controls of a class with internet and no list of domains give it.
	"search": {engine: true},
	"dangerously-bypass-approvals-and-sandbox": {refused: "it runs without the sandbox and without approvals"},
	"yolo":                          {refused: "it runs without the sandbox and without approvals"},
	"full-auto":                     {refused: "it is obsolete and changes the approvals"},
	"approve-for-me":                {refused: "it sends the approvals to an automatic reviewer"},
	"not-so-yolo":                   {refused: "it sends the approvals to an automatic reviewer"},
	"dangerously-bypass-hook-trust": {refused: "it runs hooks nobody trusted"},
	"add-dir":                       {value: true, refused: "it opens more folders to writing"},
	"cd":                            {value: true, refused: "the working folder is the worktree of the character"},
	"worktree":                      {refused: "the working folder is the worktree of the character"},
	"profile":                       {value: true, refused: "a profile can change the sandbox, the approvals and the hooks"},
	"enable":                        {value: true, refused: "the features are the engine's (the hooks stay on)"},
	"disable":                       {value: true, refused: "the features are the engine's (the hooks stay on)"},
	"image":                         {value: true, refused: "the first message is the engine's"},
}

var codexShort = map[byte]string{'c': "config", 's': "sandbox", 'a': "ask-for-approval", 'm': "model",
	'p': "profile", 'C': "cd", 'i': "image"}

// codexConfigKeys are the configuration keys (-c, first segment) a start
// may give: the model and how the TUI shows things. Every other key is the
// engine's or can open the sandbox, the approvals, the hooks or the tools:
// features (the hooks), hooks (their state), sandbox_*, approval*,
// permissions, projects (the trust), profile(s), the instructions,
// web_search, mcp_servers, tools…
var codexConfigKeys = []string{
	"model", "model_provider", "model_reasoning_effort", "model_reasoning_summary", "model_verbosity",
	"model_context_window", "model_auto_compact_token_limit", "plan_mode_reasoning_effort", "review_model",
	"personality", "service_tier", "hide_agent_reasoning", "show_raw_agent_reasoning", "tui",
	"disable_paste_burst", "file_opener", "check_for_update_on_startup", "notice", "analytics", "feedback",
	"oss_provider", "suppress_unstable_features_warning", "tool_output_token_limit",
}

// codexEngineKeys are the keys only the engine's arguments give: the Mestre
// layer (DeliverMestreLayer) and the native controls (Native).
var codexEngineKeys = []string{
	"developer_instructions", "sandbox_workspace_write.network_access", "sandbox_workspace_write.writable_roots",
	CodexWindowsSandboxKey,
}

// CodexWindowsSandboxKey is the key that picks the sandbox of Codex on
// Windows, reserved to the native controls: the default mode (elevated)
// runs every command through one runner of the machine, as the sandbox
// user, which does not separate two characters. The only mode the base uses
// is CodexWindowsSandboxMode, which runs the command as the user of the
// terminal, with a DACL that names the logon session of the process that
// called codex.exe (docs/provedores/codex-no-windows.md#modo-do-sandbox).
const CodexWindowsSandboxKey = "windows.sandbox"

// CodexWindowsSandboxMode is the one value of CodexWindowsSandboxKey a start
// may have.
const CodexWindowsSandboxMode = "unelevated"

// codexDeepRefused are segments refused at any depth of a key or of an
// inline table of its value, also under a key a start may give.
var codexDeepRefused = []string{"features", "hooks", "writable_roots", "trust_level", "sandbox_mode",
	"approval_policy", "permissions", "profile", "profiles", "model_instructions_file", "developer_instructions", "windows"}

// codexArgs is what the check found in the arguments of a start.
type codexArgs struct {
	sandbox, approval, noDaemon, model bool
	// winSandbox: the engine gave windows.sandbox="unelevated".
	winSandbox bool
}

// checkCodexArgs checks the arguments of a start: the base's, or the
// engine's (engine true: the Mestre layer and the native controls). Every
// argument must be an option the check knows: a positional (a subcommand
// such as exec, or a prompt), "--" or an unknown option is refused, since
// clap would read it in a way the check does not.
func checkCodexArgs(args []string, engine bool, found *codexArgs) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		var name, value string
		var inline bool
		switch {
		case a == "--" || a == "-" || !strings.HasPrefix(a, "-"):
			return fmt.Errorf("codex: argument %q is not an option; a start has no subcommand, prompt or \"--\"", a)
		case strings.HasPrefix(a, "--"):
			name, value, inline = strings.Cut(a[2:], "=")
		default:
			long, ok := codexShort[a[1]]
			if !ok {
				return fmt.Errorf("codex: option %q is unknown to the check of the start", a)
			}
			name = long
			if rest := a[2:]; rest != "" {
				value, inline = strings.TrimPrefix(rest, "="), true
			}
		}
		opt, ok := codexLong[name]
		switch {
		case !ok:
			return fmt.Errorf("codex: option %q is unknown to the check of the start", a)
		case opt.refused != "":
			return fmt.Errorf("codex: %s is forbidden: %s (docs/provedores/codex.md#flags)", a, opt.refused)
		case opt.engine && !engine:
			return fmt.Errorf("codex: --%s is given by the native controls of the class only", name)
		case !opt.value && inline:
			return fmt.Errorf("codex: --%s takes no value", name)
		case opt.value && !inline:
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return fmt.Errorf("codex: %s without a value", a)
			}
			i++
			value = args[i]
		}
		switch name {
		case "no-daemon":
			found.noDaemon = true
		case "sandbox":
			// clap refuses an option given twice, so a base -s would make
			// every start fail: the sandbox is the native controls' only.
			if !engine {
				return fmt.Errorf("codex: the base passes %s; the sandbox (-s) is given by the native controls of the class only", a)
			}
			if found.sandbox {
				return errors.New("codex: the sandbox (-s) given twice")
			}
			if err := codexSandbox(value); err != nil {
				return err
			}
			found.sandbox = true
		case "ask-for-approval":
			if value != "on-request" {
				return fmt.Errorf("codex: approval policy %q; the base uses on-request, so escalations go through PermissionRequest", value)
			}
			found.approval = true
		case "model":
			found.model = true
		case "config":
			if err := checkCodexConfig(value, engine, found); err != nil {
				return err
			}
		}
	}
	return nil
}

func codexSandbox(mode string) error {
	switch mode {
	case "workspace-write", "read-only":
		return nil
	}
	return fmt.Errorf("codex: sandbox %q; the base runs with workspace-write or read-only, never without the sandbox", mode)
}

// checkCodexConfig checks one -c key=value the way Codex reads it: the key
// up to the first "=", split at the dots, and the value as TOML (a value
// that is not TOML is a plain string). The key must be one a start may
// give (for the engine, also one of codexEngineKeys), and no segment of the
// key or of an inline table of the value may be one of codexDeepRefused.
func checkCodexConfig(kv string, engine bool, found *codexArgs) error {
	rawKey, value, ok := strings.Cut(kv, "=")
	if !ok {
		return fmt.Errorf("codex: -c %q without \"=\"", kv)
	}
	key := codexKeyPath(rawKey)
	if slices.Contains(key, "") {
		return fmt.Errorf("codex: -c %q has an empty key", kv)
	}
	joined := strings.Join(key, ".")
	if engine && slices.Contains(codexEngineKeys, joined) {
		if joined == CodexWindowsSandboxKey {
			// Only this value, in the one form the engine writes: any other
			// mode (elevated, mxc) or a value the check cannot read is no
			// isolation between characters.
			if strings.TrimSpace(value) != `"`+CodexWindowsSandboxMode+`"` {
				return fmt.Errorf("codex: -c %s=%s; the only mode of the base is %q", joined, value, CodexWindowsSandboxMode)
			}
			found.winSandbox = true
		}
		return nil
	}
	if !slices.Contains(codexConfigKeys, key[0]) {
		return fmt.Errorf("codex: -c %s is forbidden: a start sets only the model and the display; the sandbox, the approvals, the hooks, the instructions and the tools are the engine's (docs/provedores/codex.md#flags)", joined)
	}
	paths, err := tomlValueKeys(value)
	if err != nil {
		return fmt.Errorf("codex: -c %s: a value the check cannot read: %w", joined, err)
	}
	for _, p := range append(paths, nil) {
		for _, seg := range slices.Concat(key, p) {
			if slices.Contains(codexDeepRefused, seg) {
				return fmt.Errorf("codex: -c %s touches %s, which a start never sets", joined, seg)
			}
		}
	}
	if joined == "model" {
		found.model = true
	}
	return nil
}

// codexKeyPath splits a -c key at the dots, each segment without blanks
// and quotes (Codex splits the raw key; a quoted segment there names no
// key, here it is read as the key it spells, which only refuses more).
func codexKeyPath(k string) []string {
	parts := strings.Split(strings.TrimSpace(k), ".")
	for i, p := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(p), `"'`)
	}
	return parts
}

// tomlValueKeys reads a TOML value and returns the key paths of the inline
// tables in it (also inside arrays). A value that is not TOML is a plain
// string for Codex, unless it opens an array or an inline table: then it
// is an error here, since the toml crate may read what this reader does
// not.
func tomlValueKeys(v string) ([][]string, error) {
	v = strings.TrimSpace(v)
	rest, keys, err := walkTOMLValue(v, 0)
	if err == nil {
		if r := strings.TrimLeft(rest, " \t"); r != "" && !strings.HasPrefix(r, "#") {
			err = errors.New("text after the value")
		}
	}
	if err != nil {
		if strings.HasPrefix(v, "[") || strings.HasPrefix(v, "{") {
			return nil, err
		}
		return nil, nil // a plain string
	}
	return keys, nil
}

// skipTOMLBlank skips blanks, line breaks and comments (inside arrays).
func skipTOMLBlank(s string) string {
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		if !strings.HasPrefix(s, "#") {
			return s
		}
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			return ""
		}
		s = s[i+1:]
	}
}

// walkTOMLValue reads one value at the start of s and returns what follows
// it and the key paths of its inline tables.
func walkTOMLValue(s string, depth int) (string, [][]string, error) {
	if depth > 32 {
		return "", nil, errors.New("too deep")
	}
	s = strings.TrimLeft(s, " \t")
	switch {
	case s == "":
		return "", nil, errors.New("no value")
	case strings.HasPrefix(s, `"""`) || strings.HasPrefix(s, "'''"):
		end := closeMultiline(s, 3, s[:3])
		if end < 0 {
			return "", nil, errors.New("unterminated multi-line string")
		}
		return s[end:], nil, nil
	case s[0] == '"':
		end := closingQuote(s)
		if end < 0 {
			return "", nil, errors.New("unterminated string")
		}
		return s[end+1:], nil, nil
	case s[0] == '\'':
		end := strings.IndexAny(s[1:], "'\n")
		if end < 0 || s[1+end] != '\'' {
			return "", nil, errors.New("unterminated string")
		}
		return s[end+2:], nil, nil
	case s[0] == '[':
		return walkTOMLArray(s[1:], depth)
	case s[0] == '{':
		return walkTOMLTable(s[1:], depth)
	}
	n := strings.IndexAny(s, ",]} \t\r\n#")
	if n == 0 {
		return "", nil, errors.New("no value")
	}
	if n < 0 {
		n = len(s)
	}
	return s[n:], nil, nil
}

// walkTOMLArray reads the rest of an array, after its "[".
func walkTOMLArray(s string, depth int) (string, [][]string, error) {
	var keys [][]string
	s = skipTOMLBlank(s)
	for !strings.HasPrefix(s, "]") {
		rest, sub, err := walkTOMLValue(s, depth+1)
		if err != nil {
			return "", nil, err
		}
		keys = append(keys, sub...)
		s = skipTOMLBlank(rest)
		switch {
		case strings.HasPrefix(s, ","):
			s = skipTOMLBlank(s[1:])
		case !strings.HasPrefix(s, "]"):
			return "", nil, errors.New(`array without "," or "]"`)
		}
	}
	return s[1:], keys, nil
}

// walkTOMLTable reads the rest of an inline table, after its "{".
func walkTOMLTable(s string, depth int) (string, [][]string, error) {
	var keys [][]string
	s = strings.TrimLeft(s, " \t")
	for !strings.HasPrefix(s, "}") {
		path, rest, err := parseKeyPath(s)
		if err != nil {
			return "", nil, err
		}
		rest = strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(rest, "=") {
			return "", nil, errors.New(`inline table key without "="`)
		}
		rest, sub, err := walkTOMLValue(rest[1:], depth+1)
		if err != nil {
			return "", nil, err
		}
		keys = append(keys, path)
		for _, k := range sub {
			keys = append(keys, slices.Concat(path, k))
		}
		s = strings.TrimLeft(rest, " \t")
		switch {
		case strings.HasPrefix(s, ","):
			s = strings.TrimLeft(s[1:], " \t")
		case !strings.HasPrefix(s, "}"):
			return "", nil, errors.New(`inline table without "," or "}"`)
		}
	}
	return s[1:], keys, nil
}

// codexStartArgs checks the arguments of a start (the base's and the
// engine's) and adds what the base always has, the hooks switched on last.
func codexStartArgs(base, engine []string, model, goos string) ([]string, error) {
	var found codexArgs
	if err := checkCodexArgs(base, false, &found); err != nil {
		return nil, err
	}
	if err := checkCodexArgs(engine, true, &found); err != nil {
		return nil, err
	}
	if !found.sandbox {
		return nil, errors.New("codex: no sandbox (-s) among the flags; the native controls give it, and the base never starts without it")
	}
	// On Windows the start without the unelevated mode is refused: the
	// default one would run the commands of every character through the
	// same runner (fail closed).
	if goos == "windows" && !found.winSandbox {
		return nil, fmt.Errorf("codex: no -c %s=%q among the flags on Windows; the native controls give it, and the base never starts without it", CodexWindowsSandboxKey, CodexWindowsSandboxMode)
	}
	out := slices.Concat(base, engine)
	if !found.noDaemon {
		out = append(out, "--no-daemon")
	}
	if !found.approval {
		out = append(out, "-a", "on-request")
	}
	if model != "" && !found.model {
		out = append(out, "-m", model)
	}
	// The hooks of the Mestre stay on whatever a config.toml of the
	// repository says: a -c override is above every configuration file.
	return append(out, "-c", CodexHooksOn), nil
}

// CodexHooksOn is the override every Codex start ends with.
const CodexHooksOn = "features.hooks=true"

// codexEnv puts the account profile in CODEX_HOME, replacing any other
// value; a start without a profile would use the account's own ~/.codex,
// where the Mestre hooks are not. The name is compared without case, as
// Windows does.
func codexEnv(env []string, profile string) ([]string, error) {
	isHome := func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return strings.EqualFold(k, "CODEX_HOME")
	}
	if profile == "" {
		for _, kv := range env {
			if _, v, _ := strings.Cut(kv, "="); isHome(kv) && v != "" {
				return env, nil
			}
		}
		return nil, errors.New("codex: no account profile (CODEX_HOME)")
	}
	return append(slices.DeleteFunc(slices.Clone(env), isHome), "CODEX_HOME="+profile), nil
}
