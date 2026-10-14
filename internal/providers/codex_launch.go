package providers

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// The start flags of the Codex base (docs/provedores/codex.md#flags): the
// adapter adds the mandatory ones the base and the engine did not give
// (--no-daemon, -a on-request, -m with the class's model) and refuses the
// forbidden ones, and the start without the sandbox, which Native gives
// (-s). Codex fails open on a hook failure, so the sandbox is the barrier
// left; the base never runs without it.

// codexForbiddenFlags never go in a Codex start.
var codexForbiddenFlags = []string{
	"--dangerously-bypass-approvals-and-sandbox", "--yolo", "--full-auto",
	"--add-dir", "--dangerously-bypass-hook-trust",
}

// codexArgs is what the check found in the arguments of a start.
type codexArgs struct {
	sandbox, approval, noDaemon, model bool
}

// checkCodexArgs refuses the forbidden flags and configuration keys.
func checkCodexArgs(args []string) (codexArgs, error) {
	var found codexArgs
	for i := 0; i < len(args); i++ {
		name, value, inline := strings.Cut(args[i], "=")
		if !strings.HasPrefix(name, "--") { // -s value; -c key=value is one argument
			name, value, inline = args[i], "", false
		}
		takesValue := slices.Contains([]string{"-s", "--sandbox", "-a", "--ask-for-approval", "-m", "--model", "-c", "--config", "--disable"}, name)
		if takesValue && !inline {
			if i+1 >= len(args) {
				return found, fmt.Errorf("codex: %s without a value", name)
			}
			i++
			value = args[i]
		}
		switch {
		case slices.Contains(codexForbiddenFlags, name):
			return found, fmt.Errorf("codex: %s is forbidden in the base (docs/provedores/codex.md#flags)", name)
		case name == "--no-daemon":
			found.noDaemon = true
		case name == "-s" || name == "--sandbox":
			if err := codexSandbox(value); err != nil {
				return found, err
			}
			found.sandbox = true
		case name == "-a" || name == "--ask-for-approval":
			if value != "on-request" {
				return found, fmt.Errorf("codex: approval policy %q; the base uses on-request, so escalations go through PermissionRequest", value)
			}
			found.approval = true
		case name == "-m" || name == "--model":
			found.model = true
		case name == "--disable" && value == "hooks":
			return found, errors.New("codex: --disable hooks is forbidden in the base")
		case name == "-c" || name == "--config":
			if err := checkCodexConfig(value, &found); err != nil {
				return found, err
			}
		}
	}
	return found, nil
}

func codexSandbox(mode string) error {
	switch mode {
	case "workspace-write", "read-only":
		return nil
	}
	return fmt.Errorf("codex: sandbox %q; the base runs with workspace-write or read-only, never without the sandbox", mode)
}

// checkCodexConfig refuses the -c overrides of the forbidden list.
func checkCodexConfig(kv string, found *codexArgs) error {
	key, value, _ := strings.Cut(kv, "=")
	key = strings.TrimSpace(key)
	value = strings.Trim(strings.TrimSpace(value), `"'`)
	switch key {
	case "features.hooks":
		if value == "false" {
			return errors.New("codex: features.hooks = false is forbidden in the base")
		}
	case "model_instructions_file", "instructions", "experimental_instructions_file":
		return fmt.Errorf("codex: %s is forbidden; the Mestre layer goes in developer_instructions", key)
	case "project_doc_max_bytes":
		if value == "0" {
			return errors.New("codex: project_doc_max_bytes = 0 would drop the AGENTS.md of the project")
		}
	case "sandbox_mode":
		if err := codexSandbox(value); err != nil {
			return err
		}
		found.sandbox = true
	case "approval_policy":
		if value != "on-request" {
			return fmt.Errorf("codex: approval_policy %q; the base uses on-request", value)
		}
		found.approval = true
	case "model":
		found.model = true
	}
	return nil
}

// codexStartArgs checks the arguments of a start and adds what the base
// always has.
func codexStartArgs(args []string, model string) ([]string, error) {
	found, err := checkCodexArgs(args)
	if err != nil {
		return nil, err
	}
	if !found.sandbox {
		return nil, errors.New("codex: no sandbox (-s) among the flags; the native controls give it, and the base never starts without it")
	}
	out := slices.Clone(args)
	if !found.noDaemon {
		out = append(out, "--no-daemon")
	}
	if !found.approval {
		out = append(out, "-a", "on-request")
	}
	if model != "" && !found.model {
		out = append(out, "-m", model)
	}
	return out, nil
}

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
