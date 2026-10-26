package classes

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"sync"

	"github.com/mestre-ia/mestre/internal/providers"
)

//go:embed builtin/orquestrador/permissions.yaml
var builtinFS embed.FS

// The two roles that take their effective rules from the built-in class
// (docs/classes/classe-orquestrador.md).
const (
	RoleGrandMaster = LevelGrandMaster
	RoleTableMaster = LevelTableMaster
)

var (
	orchOnce  sync.Once
	orchFiles Files
	orchPerms *Permissions
	orchErr   error
)

func loadOrchestrator() {
	data, err := builtinFS.ReadFile("builtin/orquestrador/" + PermissionsFile)
	if err == nil {
		orchFiles, err = NewFiles(Files{{Path: PermissionsFile, Data: data}})
	}
	if err == nil {
		orchPerms, err = ParsePermissions(data)
	}
	orchErr = err
}

// Orchestrator is the built-in class `orquestrador`: the same in every
// workspace, read-only, with no scope and no CLASS.md (the instructions of
// the orchestrators are the ones of their level). The caller gets a copy
// and may keep it.
func Orchestrator() (*Class, error) {
	orchOnce.Do(loadOrchestrator)
	if orchErr != nil {
		return nil, fmt.Errorf("built-in class %s: %w", ReservedOrchestrator, orchErr)
	}
	p := *orchPerms
	files := make(Files, len(orchFiles))
	for i, f := range orchFiles {
		files[i] = File{Path: f.Path, Data: append([]byte(nil), f.Data...)}
	}
	return &Class{
		Name:        ReservedOrchestrator,
		Meta:        Meta{Name: ReservedOrchestrator, Description: "Regras efetivas do Grão-Mestre e dos Mestres da mesa."},
		Permissions: &p, Files: files, Hash: files.Hash(),
	}, nil
}

// ErrBuiltIn: the class is built into the binary; nobody edits it or
// proposes a change to it.
var ErrBuiltIn = errors.New("built_in: a classe `orquestrador` é embutida no binário e não se edita")

// OrchestratorLayers are what the Citadel knows about one orchestrator
// besides the built-in class and the ceiling of the workspace.
type OrchestratorLayers struct {
	// Role is RoleGrandMaster or RoleTableMaster.
	Role    Level
	AgentID string
	// Sheet is the restrictions of its sheet (nil: none); it only hardens.
	Sheet     *Permissions
	DenyTools []string
	Decrees   []Decree
	// Placeholders resolve the markers of the patterns ("mesa.branch").
	Placeholders map[string]string
	Locked       bool
}

// OrchestratorRules are the effective rules of an orchestrator: the built-in
// class cut by the ceiling of the workspace and hardened by its sheet, with
// the decrees on top (docs/classes/classe-orquestrador.md#camadas). The
// ceiling and the sheet only restrict; nothing widens the class.
func (l *Library) OrchestratorRules(ctx context.Context, o OrchestratorLayers) (*Rules, error) {
	if o.Role != RoleGrandMaster && o.Role != RoleTableMaster {
		return nil, fmt.Errorf("orchestrator rules: %q is not an orchestrator role", o.Role)
	}
	c, err := Orchestrator()
	if err != nil {
		return nil, err
	}
	ceil, err := l.ceiling(ctx)
	if err != nil {
		return nil, err
	}
	r, err := Build(Layers{
		AgentID: o.AgentID, Class: ReservedOrchestrator, Permissions: c.Permissions,
		Ceiling: ceil.Maximum(), Sheet: o.Sheet, DenyTools: o.DenyTools, Decrees: o.Decrees,
		Placeholders: o.Placeholders, Locked: o.Locked, Now: l.Clock.Now(),
	})
	if err != nil {
		return nil, err
	}
	r.ClassHooks = &ShippedHooks{Hash: c.Hash} // the orchestrators have no extra hooks
	return r, nil
}

// Attenuates is the attenuation of the approval chain
// (docs/orquestracao/cadeia-de-aprovacao.md#salvaguardas): an agent approves
// only an action that its own effective rules would not deny (allow or ask),
// in the worktree of whoever asked (env is the requester's), without
// counting the agent's own exceptions. The Decision says why, when not.
func Attenuates(ctx context.Context, env Env, approver *Rules, ev providers.Event) (bool, Decision) {
	env.Exceptions, env.TableRules = nil, nil
	ev.AgentID = "" // the event is the requester's; the rules are the approver's
	d := Decide(ctx, env, approver, ev)
	return d.Verdict != Deny, d
}
