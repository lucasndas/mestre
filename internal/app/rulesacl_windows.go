//go:build windows

package app

import (
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/safefile"
)

// lockRulesFromAgents names the account of the agents of the instance to safefile, which
// denies it the write on .mestre\regras\ whenever the engine tightens the rules.
func lockRulesFromAgents(instance string) {
	safefile.SetRulesAgentAccount(localchan.AgentAccount(instance))
}
