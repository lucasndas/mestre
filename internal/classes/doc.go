// Package classes holds the permissions of the classes
// (docs/classes/permissoes.md): the permissions.yaml format
// (docs/classes/formato-das-permissoes.md), the fixed security rules built
// into the binary, the effective rules of each character, which the Citadel
// builds by the precedence of docs/arquitetura/precedencia.md and sends to
// the machine in agent.rules, and the decision every tool call of every agent
// goes through.
//
// The decision is a function of the normalized event of the adapter
// (internal/providers), the effective rules and the character's
// environment (Env): Decide for the engine, DecideDegraded for the hook when
// the engine is down. The layers that later milestones own come in as plain
// inputs or small interfaces: the decrees and exceptions as entries of the
// effective rules, the ceiling and the sheet as Permissions given to Build,
// the git state, the edits of the turn and the record of used exceptions as
// interfaces of Env.
package classes
