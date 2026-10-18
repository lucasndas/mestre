package localchan

import "github.com/lucasndas/mestre/internal/winsvc"

// DefaultAccess on Windows is the ACL of cli.sock of an instance
// (docs/arquitetura/canal-local.md#endpoint): the engine, whose account owns
// the socket, and SYSTEM everything; the account of the agents of that
// instance (mestre-agente, or mestre-agente-<inst>) read and write; nothing
// inherited. winsvc.CLIAccess applies it, and Listen does so every time the
// socket is created. The installer sets the same ACL on the folder at install
// time (M7).
func DefaultAccess(instance string) Access {
	return winsvc.CLIAccess{AgentAccount: AgentAccount(instance)}
}
