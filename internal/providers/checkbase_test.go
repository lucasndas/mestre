package providers_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

func TestCheckBaseRefusesWhatTheStartRefuses(t *testing.T) {
	t.Parallel()
	// The check of the save is the one of every start
	// (docs/orquestracao/bases-e-fichas.md#regras-e-erros): the sheet's agent
	// decides the lists.
	for _, tt := range []struct {
		p    providers.ID
		env  []string
		args []string
	}{
		{providers.ClaudeCode, []string{"CLAUDE_CODE_SIMPLE=1"}, nil},
		{providers.ClaudeCode, []string{"CLAUDE_CODE_DISABLE_HOOKS=1"}, nil},
		{providers.Codex, []string{"CODEX_HOME=/x"}, nil},
		{providers.Antigravity, []string{"HOME=/x"}, nil},
		{providers.Codex, nil, []string{"--yolo"}},
		{providers.Antigravity, nil, []string{"--mode", "auto"}},
	} {
		if err := providers.CheckBase(tt.p, tt.args, tt.env); err == nil {
			t.Errorf("%s %v %v was accepted", tt.p, tt.env, tt.args)
		}
	}
	if err := providers.CheckBase(providers.ClaudeCode, []string{"--model", "sonnet", "--chrome"}, []string{"LANG=pt_BR.UTF-8", "CLAUDE_CODE_DISABLE_MOUSE=1"}); err != nil {
		t.Errorf("a plain base: %v", err)
	}
	if got := providers.ProfileEnv(providers.Codex, "/contas/codex-pessoal"); len(got) != 1 || got[0] != "CODEX_HOME=/contas/codex-pessoal" {
		t.Errorf("profile env = %v", got)
	}
}

func TestInferProviderReadsTheProgramName(t *testing.T) {
	t.Parallel()
	for command, want := range map[string]providers.ID{
		"claude": providers.ClaudeCode, "claude.exe": providers.ClaudeCode, `C:\Tools\Claude.EXE`: providers.ClaudeCode,
		"/usr/local/bin/codex": providers.Codex, "codex.exe": providers.Codex, "agy": providers.Antigravity, "agy.exe": providers.Antigravity,
		"bash": providers.Null, "claude-wrapper": providers.Null,
	} {
		if got, ok := providers.InferProvider(command); got != want || ok != (want != providers.Null) {
			t.Errorf("%q: %q, %v; want %q", command, got, ok, want)
		}
	}
}

func TestEachProviderReadsItsProfileFromItsOwnVariable(t *testing.T) {
	t.Parallel()
	for p, want := range map[providers.ID]string{providers.ClaudeCode: "CLAUDE_CONFIG_DIR=/p", providers.Codex: "CODEX_HOME=/p", providers.Antigravity: "HOME=/p"} {
		if got := providers.ProfileEnv(p, "/p"); len(got) != 1 || got[0] != want {
			t.Errorf("%s: %v, want %s", p, got, want)
		}
	}
	if got := providers.ProfileEnv(providers.Null, "/p"); got != nil {
		t.Errorf("a shell has no profile: %v", got)
	}
}
