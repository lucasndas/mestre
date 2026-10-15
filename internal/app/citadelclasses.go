package app

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tablerules"
	"github.com/lucasndas/mestre/internal/protocol"
)

// CharacterDecrees gives the decrees and exceptions that apply to a character
// besides the temporary table rules: the exceptions of the approvals, the
// permanent rules and the decrees with validity that only people give, and the
// delegations (milestone 5 keeps them; nil: none).
type CharacterDecrees interface {
	Decrees(ctx context.Context, c characters.Character) ([]classes.Decree, error)
}

// CitadelClassesOptions are the pieces of the Citadel side of the effective
// rules (docs/arquitetura/precedencia.md#combinação-na-decisão-de-permissão).
type CitadelClassesOptions struct {
	Registry characters.Registry
	// Libraries gives the library of classes of a workspace (nil result:
	// the workspace has none, and its characters have no rules).
	Libraries func(workspace string) *classes.Library
	// Presets gives the sheet of each character, whose restrictions only
	// harden the class.
	Presets characters.Presets
	// Decrees: see CharacterDecrees.
	Decrees CharacterDecrees
	// DenyTools are the native tools the conformance test of the machine of
	// the character denies in it (classes.Layers.DenyTools); Locked says the
	// system of the workspace is locked (/travar). Both nil: none.
	DenyTools func(ctx context.Context, c characters.Character) []string
	Locked    func(ctx context.Context, workspace string) bool
	// Env completes the environment of a character with what only its
	// machine knows (home, MESTRE_TMP, the layout); nil: the worktree and
	// the system of this process.
	Env func(ctx context.Context, c characters.Character, env classes.Env) classes.Env
}

// CitadelClasses is the production chain.Classes: the effective rules of a
// character as the Citadel builds them from the layers, with the built-in
// class `orquestrador` for the Grand Master and the Masters of the tables, and
// the class, the sheet, the ceiling and the decrees for the others. The same
// document is what RulesPublisher sends to the machine of the character. It
// fails closed: a layer that cannot be read is an error, never a wider
// document.
type CitadelClasses struct {
	o      CitadelClassesOptions
	tables atomic.Pointer[tablesLookup]
	rules  atomic.Pointer[tablerules.Service]
}

type tablesLookup struct{ t characters.Tables }

// NewCitadelClasses returns the CitadelClasses. NewOrchestration attaches the
// tables and the temporary table rules once they exist (Attach).
func NewCitadelClasses(o CitadelClassesOptions) (*CitadelClasses, error) {
	if o.Libraries == nil || o.Presets == nil {
		return nil, errors.New("app: the classes of the Citadel need the libraries and the presets")
	}
	return &CitadelClasses{o: o}, nil
}

var _ interface{ attach(*Orchestration) } = (*CitadelClasses)(nil)

func (c *CitadelClasses) attach(orc *Orchestration) {
	tbl := orc.Tables
	c.tables.Store(&tablesLookup{tablesRef{&tbl}})
	c.rules.Store(orc.TableRules)
}

// TableRules is the gate of the temporary table rules the engine asks at each
// use (hook.Engine.TableRules); nil until the orchestration is built.
func (c *CitadelClasses) TableRules() classes.TableRuleGate {
	if r := c.rules.Load(); r != nil {
		return r
	}
	return nil
}

func (c *CitadelClasses) library(workspace string) (*classes.Library, error) {
	lib := c.o.Libraries(workspace)
	if lib == nil {
		return nil, fmt.Errorf("the workspace %q has no classes", workspace)
	}
	return lib, nil
}

// Rules implements chain.Classes (and classes.RulesSource).
func (c *CitadelClasses) Rules(ctx context.Context, id string) (*classes.Rules, error) {
	ch, err := c.o.Registry.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	lib, err := c.library(ch.Workspace)
	if err != nil {
		return nil, err
	}
	sheet, err := c.sheet(ctx, ch)
	if err != nil {
		return nil, err
	}
	decrees, err := c.decrees(ctx, ch)
	if err != nil {
		return nil, err
	}
	var deny []string
	if c.o.DenyTools != nil {
		deny = c.o.DenyTools(ctx, ch)
	}
	locked := c.o.Locked != nil && c.o.Locked(ctx, ch.Workspace)
	ph, err := c.placeholders(ctx, ch)
	if err != nil {
		return nil, err
	}
	switch ch.Role {
	case characters.RoleGrandMaster, characters.RoleTableMaster:
		role := classes.RoleTableMaster
		if ch.Role == characters.RoleGrandMaster {
			role = classes.RoleGrandMaster
		}
		return lib.OrchestratorRules(ctx, classes.OrchestratorLayers{Role: role, AgentID: ch.ID, Sheet: sheet, DenyTools: deny, Decrees: decrees,
			Placeholders: ph, Locked: locked})
	case characters.RoleCharacter:
		return lib.CharacterRules(ctx, classes.CharacterLayers{AgentID: ch.ID, Class: ch.Class, Table: ch.Table, Sheet: sheet, DenyTools: deny, Decrees: decrees,
			Placeholders: ph, Locked: locked})
	}
	return nil, fmt.Errorf("character %s has the unknown level %q", ch.ID, ch.Role)
}

func (c *CitadelClasses) sheet(ctx context.Context, ch characters.Character) (*classes.Permissions, error) {
	if ch.Sheet == "" {
		return nil, nil
	}
	snap, err := c.o.Presets.Snapshot(ctx, ch.Sheet)
	if err != nil {
		return nil, fmt.Errorf("the sheet of %s: %w", ch.Name, err)
	}
	p, err := snap.Sheet.Permissions()
	if err != nil {
		return nil, fmt.Errorf("the restrictions of the sheet %q: %w", ch.Sheet, err)
	}
	return p, nil
}

// decrees are the active decrees of a character: the temporary table rules
// of its class in its table and what milestone 5 keeps.
func (c *CitadelClasses) decrees(ctx context.Context, ch characters.Character) ([]classes.Decree, error) {
	var out []classes.Decree
	if rules := c.rules.Load(); rules != nil && ch.Table != "" {
		d, err := rules.Decrees(ctx, ch.Workspace, ch.Table, ch.Class)
		if err != nil {
			return nil, err
		}
		out = append(out, d...)
	}
	if c.o.Decrees != nil {
		d, err := c.o.Decrees.Decrees(ctx, ch)
		if err != nil {
			return nil, err
		}
		out = append(out, d...)
	}
	return out, nil
}

func (c *CitadelClasses) placeholders(ctx context.Context, ch characters.Character) (map[string]string, error) {
	if ch.Table == "" {
		return nil, nil
	}
	t := c.tables.Load()
	if t == nil {
		return nil, errors.New("the tables are not built yet")
	}
	info, err := t.t.Table(ctx, ch.Workspace, ch.Table)
	if err != nil {
		return nil, err
	}
	return map[string]string{"mesa.branch": info.Branch}, nil
}

// Env implements chain.Classes: the worktree of the character, its table and
// the system of its machine. The ledger and the table-rule gate stay empty:
// the chain uses the environment for the attenuation, which counts neither.
func (c *CitadelClasses) Env(ctx context.Context, id string) (classes.Env, error) {
	ch, err := c.o.Registry.Get(ctx, id)
	if err != nil {
		return classes.Env{}, err
	}
	env := classes.Env{AgentID: ch.ID, TableID: ch.Table, Worktree: ch.Dir, OS: runtime.GOOS}
	if c.o.Env != nil {
		env = c.o.Env(ctx, ch, env)
	}
	return env, nil
}

// Domains implements chain.Classes: the network.domains of the class of the
// character and of the ceiling of its workspace.
func (c *CitadelClasses) Domains(ctx context.Context, id string) ([]string, error) {
	ch, err := c.o.Registry.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	lib, err := c.library(ch.Workspace)
	if err != nil {
		return nil, err
	}
	class := ch.Class
	if ch.Role != characters.RoleCharacter {
		class = classes.ReservedOrchestrator
	}
	return lib.Domains(ctx, class, ch.Table)
}

// RulesSender reaches the engine of a Tower (milestone 5: the connection of
// the Citadel).
type RulesSender interface {
	SendRules(ctx context.Context, machine string, msg protocol.Outgoing) error
}

// RulesPublisher puts the effective rules of a character where its hooks read
// them: the store of this machine when the character runs here, an agent.rules
// message to the Tower when it runs there (classes.Message). It runs when the
// character starts, when its class changes and when a temporary table rule is
// made.
type RulesPublisher struct {
	Classes *CitadelClasses
	// Local is the store of the machine of the Citadel and Machine its id.
	Local   *classes.Store
	Machine string
	// Sender reaches the other machines; nil refuses to publish to them.
	Sender RulesSender
}

// Publish builds and sends the rules of one character.
func (p *RulesPublisher) Publish(ctx context.Context, id string) error {
	ch, err := p.Classes.o.Registry.Get(ctx, id)
	if err != nil {
		return err
	}
	return p.publish(ctx, ch)
}

func (p *RulesPublisher) publish(ctx context.Context, ch characters.Character) error {
	r, err := p.Classes.Rules(ctx, ch.ID)
	if err != nil {
		return fmt.Errorf("effective rules of %s: %w", ch.Name, err)
	}
	if ch.Machine == p.Machine {
		if p.Local == nil {
			return errors.New("this machine keeps no effective rules")
		}
		if err := p.Local.Save(ctx, r); err != nil && !errors.Is(err, classes.ErrStale) {
			return err
		}
		return nil
	}
	if p.Sender == nil {
		return fmt.Errorf("the machine %s is not reachable for the rules of %s", ch.Machine, ch.Name)
	}
	msg, err := classes.Message(r)
	if err != nil {
		return err
	}
	return p.Sender.SendRules(ctx, ch.Machine, msg)
}

// PublishTable sends the rules of every living character of a class in a
// table (the temporary table rules of the class changed). It tries all of
// them and returns what failed.
func (p *RulesPublisher) PublishTable(ctx context.Context, workspace, table, class string) error {
	alive, err := p.Classes.o.Registry.AliveOf(ctx, workspace)
	if err != nil {
		return err
	}
	var errs []error
	for _, ch := range alive {
		if ch.Table != table || (class != "" && ch.Class != class) {
			continue
		}
		if err := p.publish(ctx, ch); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// PublishingMachines is the machines of the orchestration with the rules of a
// character published before its terminal starts: a start whose rules cannot
// be built or kept does not happen (the hook would decide without them).
func PublishingMachines(m OrchestrationMachines, p *RulesPublisher) OrchestrationMachines {
	return publishingMachines{OrchestrationMachines: m, pub: p}
}

type publishingMachines struct {
	OrchestrationMachines
	pub *RulesPublisher
}

func (m publishingMachines) Start(ctx context.Context, machine string, cmd characters.StartCommand) (characters.StartResult, error) {
	if err := m.pub.Publish(ctx, cmd.Character); err != nil {
		return characters.StartResult{}, err
	}
	return m.OrchestrationMachines.Start(ctx, machine, cmd)
}
