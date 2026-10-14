package providers

import (
	"errors"
	"fmt"
)

// The operations of the adapter contract that prepare a character before
// it starts (docs/provedores/adaptador.md#contrato): HookFiles, PreTrust and
// DeliverMestreLayer; and ReadUsage, which reads what it spent. Each
// provider document says what its adapter does.

// ErrNotImplemented is an operation a provider adapter does not have yet.
var ErrNotImplemented = errors.New("not implemented in this change")

func notImplemented(p ID, op string) error {
	return fmt.Errorf("%s %s: %w", p, op, ErrNotImplemented)
}

// ManagedFile is a file the Mestre keeps part of. Merge builds the new
// content from the current one (nil when the file does not exist): it takes
// out the part the Mestre owns, adds the current one and keeps everything
// else. It is idempotent. An error means the file must not be touched (a
// settings file that is not valid JSON): the characters of the provider do
// not start in that folder (docs/sessao-zero/mesa.md#mescla-gerenciada).
type ManagedFile struct {
	Path  string
	Merge func(current []byte) ([]byte, error)
}

// HookFilesRequest is what HookFiles receives.
type HookFilesRequest struct {
	// OS is the system of the machine ("linux", "windows").
	OS string
	// Root is the worktree (Claude Code, Antigravity) or the folder of the
	// account profile (Codex).
	Root string
	// Binary is the stable path of the `mestre` binary. Empty: the hook
	// finds it by MESTRE_CLI, the variable every character has.
	Binary string
	// Fallbacks are the "Se falhar" paths the conformance test switched on.
	Fallbacks NativeFallbacks
}

// PreTrustRequest is what PreTrust receives.
type PreTrustRequest struct {
	// OS is the system of the machine ("linux", "windows").
	OS string
	// Profile is the folder of the account profile (CLAUDE_CONFIG_DIR for
	// Claude Code).
	Profile string
	// Worktree is the absolute path of the worktree.
	Worktree string
	// RepoRoot is the root of the repository the worktree came from (the
	// --bare mirror of a table).
	RepoRoot string
}

// Trust is what PreTrust wrote: the file and the entries it set or cleared.
type Trust struct {
	Path string
	// Trusted are the entries now trusted, Cleared those that lost the
	// trust they had (the mirror's).
	Trusted, Cleared []string
	// Changed is false when the file already held all of it.
	Changed bool
}

// TrustRemover is implemented by an adapter that can take back the trust it
// gave a worktree (docs/sessao-zero/mesa.md#confiança-de-pasta-por-provedor:
// when the worktree is removed, and when the conformance test ends).
type TrustRemover interface {
	Untrust(req PreTrustRequest) (Trust, error)
}

// LayerRequest is what DeliverMestreLayer receives.
type LayerRequest struct {
	// Path is the MESTRE.md of the character, in
	// .mestre/agents/<id>/MESTRE.md (absolute, or relative to the worktree).
	Path string
	// CharacterSkills is true when the character has skills of its own
	// (docs/sessao-zero/personagem.md#skills-por-personagem).
	CharacterSkills bool
	// OS is the system of the machine ("" is this one); Codex bounds the
	// layer by the Windows command line.
	OS string
}

// Layer is what DeliverMestreLayer gives BuildLaunch: arguments for
// LaunchRequest.ProviderArgs and extra files to write.
type Layer struct {
	Args  []string
	Files []ManagedFile
}

// UsageRequest is what ReadUsage receives.
type UsageRequest struct {
	AgentID string
	// Last is the last usage event of the character (Claude Code: the
	// statusLine call).
	Last Event
	// Payload is the raw JSON of the usage source, when the engine kept it.
	// It wins over Last.
	Payload []byte
	// Profile is the folder of the account profile (Codex: CODEX_HOME, the
	// only place a transcript is read from).
	Profile string
	// Cursor is where the previous reading of the session stopped, as the
	// previous Usage.Cursor gave it; "" is the start (Codex).
	Cursor string
}

// ErrNoUsage is a usage source with nothing to read.
var ErrNoUsage = errors.New("no usage to read")
