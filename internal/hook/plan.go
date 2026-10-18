package hook

import (
	"io/fs"
	"path"
	"path/filepath"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/localchan"
)

// Accounts of the Linux installation (docs/arquitetura/seguranca.md#contas-do-sistema-e-isolamento-dos-agentes).
// GroupAgents is the group of the agents of the principal instance; the others
// have mestre-agente-<inst> (localchan.AgentAccount).
const (
	OwnerEngine = "mestre"
	GroupAgents = "mestre-agente"
)

// CharacterID stands for the id of a character in the paths of the plan.
const CharacterID = "<id>"

// PlanEntry is one folder or file of the plan.
type PlanEntry struct {
	Path  string
	Dir   bool
	Owner string
	Group string
	// Mode carries the permission bits and fs.ModeSticky or fs.ModeSetgid.
	Mode fs.FileMode
	// PerCharacter entries have CharacterID in the path: the engine makes
	// them for each character; the installer makes the others.
	PerCharacter bool
	Why          string
}

// LinuxPlan is the plan of the folders the dispatcher reads and writes on a
// Linux installation (docs/arquitetura/persistencia.md#arquivos): agentes/
// with the snapshots, which the hook, running as mestre-agente, reads and
// cannot change, and the hook queue, where the group of the agents only
// creates files. The installer (M7) applies it; the engine makes the
// entries of each character.
func LinuxPlan(instance string) []PlanEntry {
	// Paths of Linux, in slash form wherever the plan is generated.
	agents := filepath.ToSlash(classes.DefaultLayout("linux", "", instance).Agents)
	char := path.Join(agents, CharacterID)
	// the agents of an instance are the account of that instance, not of another
	group := localchan.AgentAccount(instance)
	entry := func(p string, dir bool, mode fs.FileMode, per bool, why string) PlanEntry {
		return PlanEntry{Path: filepath.ToSlash(p), Dir: dir, Owner: OwnerEngine, Group: group, Mode: mode, PerCharacter: per, Why: why}
	}
	return []PlanEntry{
		entry(agents, true, 0o750, false, "the folders of the characters: the agents' group goes in, does not write"),
		entry(char, true, 0o750, true, "the folder of one character"),
		entry(path.Join(char, classes.SnapshotName), false, 0o640, true, "the snapshot of the effective rules: the hook reads it, cannot change it"),
		entry(path.Join(char, EnvName), false, 0o640, true, "the environment of the character for the degraded decision"),
		entry(path.Join(char, "tmp"), true, fs.ModeSetgid|0o770, true, "MESTRE_TMP: written by the character"),
		entry(QueueDir("linux", "", instance), true, fs.ModeSticky|0o730, false, "the hook queue: the agents' group only creates files"),
	}
}
