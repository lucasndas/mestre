package classes

import (
	"context"
	"fmt"
)

// CharacterLayers are what the Citadel knows about one ordinary character
// besides its class and the ceiling of the workspace.
type CharacterLayers struct {
	AgentID string
	// Class is the class of the character; Table the table it is in (the
	// table scope of the library is looked at first).
	Class, Table string
	// Sheet is the restrictions of its sheet (nil: none); it only hardens.
	Sheet     *Permissions
	DenyTools []string
	Decrees   []Decree
	// Placeholders resolve the markers of the patterns ("mesa.branch").
	Placeholders map[string]string
	Locked       bool
}

// CharacterRules are the effective rules of an ordinary character: the
// approved version of its class, cut by the ceiling of the workspace and
// hardened by its sheet, with the decrees on top. A class without an
// approved version is an error, never a wider document.
func (l *Library) CharacterRules(ctx context.Context, c CharacterLayers) (*Rules, error) {
	if Slug(c.Class) == ReservedOrchestrator {
		return nil, fmt.Errorf("character rules: %q is the class of the orchestrators", c.Class)
	}
	cls, err := l.Approved(ctx, Slug(c.Class), c.Table)
	if err != nil {
		return nil, err
	}
	ceil, err := l.ceiling(ctx)
	if err != nil {
		return nil, err
	}
	return Build(Layers{
		AgentID: c.AgentID, Class: cls.Name, Permissions: cls.Permissions,
		Ceiling: ceil.Maximum(), Sheet: c.Sheet, DenyTools: c.DenyTools, Decrees: c.Decrees,
		Placeholders: c.Placeholders, Locked: c.Locked, Now: l.Clock.Now(),
	})
}

// Domains are the network.domains of the approved class and of the ceiling of
// the workspace: a domain in either list counts for the chain of approval
// (docs/orquestracao/cadeia-de-aprovacao.md#salvaguardas).
func (l *Library) Domains(ctx context.Context, class, table string) ([]string, error) {
	ceil, err := l.ceiling(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	add := func(p *Permissions) {
		if p != nil && p.Network != nil && p.Network.Domains != nil {
			out = append(out, *p.Network.Domains...)
		}
	}
	add(ceil.Maximum())
	if class != "" && Slug(class) != ReservedOrchestrator {
		cls, err := l.Approved(ctx, Slug(class), table)
		if err != nil {
			return nil, err
		}
		add(cls.Permissions)
	}
	return dedupe(out), nil
}
