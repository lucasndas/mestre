package sessionzero

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/providers"
)

// The variables of a character that are not the account profile's
// (docs/sessao-zero/personagem.md#variáveis-de-ambiente); the ones of the
// local channel are localchan's.
const (
	EnvAgentID   = "MESTRE_AGENT_ID"
	EnvWorkspace = "MESTRE_WORKSPACE"
	EnvTable     = "MESTRE_MESA"
	EnvTmp       = "MESTRE_TMP"
	EnvTmpDir    = "TMPDIR"
	EnvTemp      = "TEMP" // Windows reads TEMP and TMP, not TMPDIR
	EnvTmpWin    = "TMP"
	EnvGitConfig = "GIT_CONFIG_GLOBAL"
)

// EnvVar is a variable, KEY=VALUE apart.
type EnvVar struct{ Name, Value string }

// providerKeys are the names of the API key variables of each provider, the
// only secrets an environment may carry (docs/plataforma/cofre.md).
var providerKeys = map[providers.ID][]string{
	providers.ClaudeCode:  {"ANTHROPIC_API_KEY"},
	providers.Codex:       {"OPENAI_API_KEY", "CODEX_API_KEY"},
	providers.Antigravity: {"GEMINI_API_KEY", "GOOGLE_API_KEY"},
}

// gitIdentity are the variables that would win over the gitconfig of the
// character: the engine never sets them and takes them out of the base.
func gitIdentity(kv string) bool {
	k, _, _ := strings.Cut(kv, "=")
	k = strings.ToUpper(k)
	return strings.HasPrefix(k, "GIT_AUTHOR_") || strings.HasPrefix(k, "GIT_COMMITTER_")
}

// withoutGitIdentity drops the author and committer variables.
func withoutGitIdentity(env []string) []string {
	return slices.DeleteFunc(slices.Clone(env), gitIdentity)
}

// TerminalEnv is the environment a terminal started with `spawn` gets: only
// the workspace, the table and the variables of the base (without a git
// identity), and nothing of the character
// (docs/sessao-zero/personagem.md#variáveis-de-ambiente).
func TerminalEnv(workspace, table string, base []string) []string {
	env := []string{EnvWorkspace + "=" + workspace, EnvTable + "=" + table}
	return append(env, withoutGitIdentity(base)...)
}

// charEnv is what engineEnv builds the environment of a character from.
type charEnv struct {
	provider       providers.ID
	agentID        string
	workspace      string
	table          string
	endpoint, cli  string
	tmp, gitconfig string
	profile        string
	key            *EnvVar
}

// engineEnv is the environment the engine adds to a character: the ones of
// the Mestre, the temporary folder, the git identity file and the account
// profile. The identity comes only from the gitconfig (never GIT_AUTHOR_*
// or GIT_COMMITTER_*), and no variable holds a secret except the API key of
// the provider when the profile uses one.
func engineEnv(e charEnv) ([]string, error) {
	env := []string{
		localchan.EnvEndpoint + "=" + e.endpoint,
		localchan.EnvCLI + "=" + e.cli,
		EnvWorkspace + "=" + e.workspace,
		EnvTable + "=" + e.table, // empty for the Grand Master
		EnvTmp + "=" + e.tmp,
		EnvTmpDir + "=" + e.tmp,
		EnvTemp + "=" + e.tmp,
		EnvTmpWin + "=" + e.tmp,
		EnvGitConfig + "=" + e.gitconfig,
	}
	env = append(env, providers.ProfileEnv(e.provider, e.profile)...)
	if e.key != nil {
		if !slices.Contains(providerKeys[e.provider], e.key.Name) {
			return nil, fmt.Errorf("the variable %s is not the API key of %s", e.key.Name, e.provider)
		}
		env = append(env, e.key.Name+"="+e.key.Value)
	}
	return env, nil
}

// checkSecrets refuses variables with a value the detector of secrets
// recognizes. The error names the variable, never the value. It is run on the
// variables of the base: the rest are paths and ids of the engine, which a
// long slug could make look like a secret, and the API key of the provider is
// added apart, under the names of providerKeys only.
func checkSecrets(m *logging.Masker, env []string) error {
	if m == nil {
		return errors.New("no detector of secrets: the environment of a character is not checked, so it does not start")
	}
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		if m.Mask(value) != value {
			return fmt.Errorf("the variable %s looks like a secret; no variable of a character may carry one", name)
		}
	}
	return nil
}
