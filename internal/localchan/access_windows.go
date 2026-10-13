package localchan

import "github.com/lucasndas/mestre/internal/winsvc"

// DefaultAccess on Windows is the ACL of cli.sock
// (docs/arquitetura/canal-local.md#endpoint): the engine, whose account owns
// the socket, and SYSTEM everything; mestre-agente read and write; nothing
// inherited. winsvc.CLIAccess applies it, and Listen does so every time the
// socket is created. The installer sets the same ACL on the folder at install
// time (M7).
func DefaultAccess() Access { return winsvc.CLIAccess{AgentAccount: "mestre-agente"} }
