package providers

import (
	"fmt"
	"path"
	"strings"
)

// Agent stands for a provider the base alone does not name: CheckBase then
// applies only what every agent shares (the variables that run code before
// the hooks), not the flags and variables of one provider.
const Agent ID = "agent"

// CheckBase checks the arguments and the variables of a base for the provider
// p when the base is saved (docs/orquestracao/bases-e-fichas.md#regras-e-erros).
// It is the same check BuildLaunch runs at every start, so a base that passes
// here is not refused later for what it carries. The engine's own arguments
// are not in the base: only the base's side of each provider's list of what
// is allowed is checked. For the null adapter (a shell) only the Mestre's
// variables are refused.
func CheckBase(p ID, args, env []string) error {
	if err := refuseBaseEnv(p, env); err != nil {
		return err
	}
	if err := refuseLatest(p, args, latestOptions(p)...); err != nil {
		return err
	}
	switch p {
	case ClaudeCode:
		flags, err := parseClaudeFlags(args, false)
		if err != nil {
			return err
		}
		for _, f := range flags {
			if f.name == "--permission-mode" && f.values[0] != "default" {
				return fmt.Errorf("claude-code: --permission-mode %v is forbidden: default is the only mode allowed", f.values)
			}
		}
	case Codex:
		var found codexArgs
		return checkCodexArgs(args, false, &found)
	case Antigravity:
		return refuseAgyFlags(args)
	}
	return nil
}

func latestOptions(p ID) []string {
	switch p {
	case ClaudeCode:
		return []string{"--continue", "-c"}
	case Codex:
		return []string{"--last"}
	}
	return nil
}

// InferProvider names the provider whose CLI a base command starts, from the
// program name (claude, codex, agy, with or without .exe). A command of
// another program is not an agent the Mestre knows.
func InferProvider(command string) (ID, bool) {
	name := strings.ToLower(path.Base(strings.ReplaceAll(command, `\`, "/")))
	name = strings.TrimSuffix(name, ".exe")
	switch name {
	case "claude":
		return ClaudeCode, true
	case "codex":
		return Codex, true
	case "agy":
		return Antigravity, true
	}
	return Null, false
}

// ProfileEnv is the variable that points the provider's CLI at the folder of
// an account profile: CLAUDE_CONFIG_DIR, CODEX_HOME, or a separate HOME for
// Antigravity, whose BuildLaunch adds the USERPROFILE that follows it on
// Windows (docs/orquestracao/bases-e-fichas.md#perfis-de-conta). It is what
// the engine adds to LaunchRequest.Env; the base may not set it
// (refuseBaseEnv).
func ProfileEnv(p ID, dir string) []string {
	switch p {
	case ClaudeCode:
		return []string{"CLAUDE_CONFIG_DIR=" + dir}
	case Codex:
		return []string{"CODEX_HOME=" + dir}
	case Antigravity:
		return []string{"HOME=" + dir}
	}
	return nil
}
