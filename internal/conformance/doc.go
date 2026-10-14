// Package conformance is the conformance test of the providers
// (docs/provedores/conformidade.md): with the real CLI of a provider and an
// account profile of this machine, it proves that a character gets the Mestre
// layer and the project's (and that the Mestre wins a conflict), sees the
// skills, fires the hooks of the map of events and respects a denial.
//
// The Runner prepares a temporary folder the way a table does (a test
// repository with a --bare mirror and a worktree, the AGENTS.md with the
// project code, the temporary class `conformidade` with the Mestre code and
// its permissions, the skills), provisions the character through the
// operations of the adapter (PreTrust, HookFiles, DeliverMestreLayer, the
// native controls and BuildLaunch), starts it on a real terminal of the Host
// and drives the twelve steps. The verdicts read only the random codes and
// the events the engine side received, never the exact text of the model.
//
// The Host is the engine side the run needs: the terminals (internal/terminal),
// the local channel with the real identification of the caller
// (internal/localchan) and the hook engine (internal/hook) over the effective
// rules of the class in a machine database of its own, with a tap that
// records every event, keeps the raw payloads for the recordings and can
// stop answering one character (step 6).
//
// The pieces of later milestones come in as interfaces: the checklist that
// shows the result (Checklist), the usage record of the system (UsageLog),
// the mode of the AI diagnosis of step 11 (Diagnosis) and the machine sheet,
// whose provider requirement reads the results (Probe). The results live in
// conformance_results of the Guild database (ResultsDB).
package conformance
