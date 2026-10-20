package hook

import (
	"context"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/providers"
)

// secretFindings are the secrets in what the tool of the event writes; the
// paths are relative to the worktree of the character when the engine knows it.
func (e *Engine) secretFindings(ctx context.Context, ev providers.Event) []secretFinding {
	if ev.Tool == nil {
		return nil
	}
	switch ev.Tool.Kind {
	case providers.KindWrite, providers.KindEdit, providers.KindShell:
	default:
		return nil
	}
	worktree := ""
	if env, err := e.registryEnv(ctx, ev.AgentID); err == nil {
		worktree = env.Worktree
	}
	return writtenSecrets(e.Masker, ev, worktree)
}

// maxListed is how many places a message lists.
const maxListed = 5

func listFindings(fs []secretFinding) string {
	var parts []string
	for i, f := range fs {
		if i == maxListed {
			parts = append(parts, fmt.Sprintf("e mais %d", len(fs)-maxListed))
			break
		}
		parts = append(parts, f.String())
	}
	return strings.Join(parts, "; ")
}

// secretDenial is what the agent reads when a write is denied for a key.
func secretDenial(fs []secretFinding) string {
	return "regra fixa de segurança: o conteúdo traz uma chave (" + listFindings(fs) +
		"); não escreva o valor no arquivo: leia-o de uma variável de ambiente ou peça a credencial pelo Mestre, e escreva de novo sem ele"
}

// secretWarning is what the agent reads after a write that went through with a key.
func secretWarning(fs []secretFinding) string {
	return "Aviso do Mestre: o que você escreveu traz uma chave (" + listFindings(fs) +
		"). Tire o valor do arquivo agora, leia-o de uma variável de ambiente e confira com git diff antes de qualquer commit: o envio do PR recusa chave no diff."
}

// keepWarning keeps, for a provider whose post_tool cannot speak to the agent
// (Antigravity answers `{}`), the warning for its next invocation: PreInvocation
// is a turn_start, and its injectSteps reach the agent.
func (e *Engine) keepWarning(agentID, msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.warnings == nil {
		e.warnings = map[string]string{}
	}
	switch old := e.warnings[agentID]; {
	case old == "":
		e.warnings[agentID] = msg
	case !strings.Contains(old, msg) && len(old) < providers.MaxText:
		e.warnings[agentID] = old + "\n" + msg
	}
}

// takeWarning gives and forgets the warning kept for the character.
func (e *Engine) takeWarning(agentID string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	msg := e.warnings[agentID]
	delete(e.warnings, agentID)
	return msg
}

// writeWarning is the answer to a post_tool whose write carried a key: the text
// goes to the agent now, or at its next invocation when the provider cannot
// speak after a tool.
func (e *Engine) writeWarning(ctx context.Context, ev providers.Event) string {
	if ev.ToolResult != nil && !ev.ToolResult.OK {
		return ""
	}
	found := e.secretFindings(ctx, ev)
	if len(found) == 0 {
		return ""
	}
	msg := secretWarning(found)
	if ev.Provider == providers.Antigravity {
		e.keepWarning(ev.AgentID, msg)
	}
	return msg
}
