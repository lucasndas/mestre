// Package hook is the hook dispatcher, `mestre hook`
// (docs/provedores/despachante-de-hooks.md): the one command the hook files
// of the three providers call, with the same logic for all of them.
//
// The dispatcher side is Run, in the process the provider starts (as
// `mestre-agente`): it reads the payload, normalizes it through the
// adapter (internal/providers), asks the engine of the machine over the
// local channel (internal/localchan) for the decision or the context, falls
// back to the degraded decision over the snapshot of the effective rules
// when the engine does not answer in time, and formats the answer for the
// provider. On error or doubt in a decision it blocks.
//
// The engine side is Engine, the handler of `cmd: "hook"` on the local
// channel: the permission pipeline of internal/classes for the verified
// character, the approval request on ask, the context of the turn and the
// synchronous hooks of the class. The pieces later milestones own (the
// terminal registry, the approvals, the inbox, the context) come in as
// interfaces.
//
// Besides both sides, the package has the hook queue (the dispatcher only
// creates files in it; the engine drains it) and the plan of the folders of
// the Linux installation the dispatcher reads (Plan).
package hook
