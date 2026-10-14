package classes

import (
	"errors"
	"fmt"
	"slices"
)

// CeilingDiff is what a new version of the ceiling changes against the
// approved one. The comparison is conservative: whatever the new version
// does not surely keep as limited widens the ceiling.
type CeilingDiff struct {
	// Widens lists what the new version frees beyond the approved one.
	Widens []string
	// Restricts lists what it limits more.
	Restricts []string
	// OperatorOn is true when it turns operator on.
	OperatorOn bool
}

// OnlyRestricts reports whether the new version widens nothing: an
// Administrator approves it on the panel.
func (d CeilingDiff) OnlyRestricts() bool { return len(d.Widens) == 0 && !d.OperatorOn }

// DiffCeilings compares a new version of the ceiling with the approved one.
func DiffCeilings(approved, next *Ceiling) CeilingDiff {
	var d CeilingDiff
	o, n := approved.Maximum(), next.Maximum()
	widen := func(f string, a ...any) { d.Widens = append(d.Widens, fmt.Sprintf(f, a...)) }
	restrict := func(f string, a ...any) { d.Restricts = append(d.Restricts, fmt.Sprintf(f, a...)) }

	// What may be given: absent limits nothing.
	allowList := func(field string, ol, nl *[]string, covers func(t, c string) bool, emptyIsAny bool) {
		absent := func(l *[]string) bool { return l == nil || (emptyIsAny && len(*l) == 0) }
		switch {
		case absent(ol) && absent(nl):
		case absent(ol):
			restrict("%s: passa a limitar a %s", field, list(*nl))
		case absent(nl):
			widen("%s: deixa de limitar (era %s)", field, list(*ol))
		default:
			for _, it := range *nl {
				if !slices.ContainsFunc(*ol, func(t string) bool { return covers(t, it) }) {
					widen("%s: %s", field, it)
				}
			}
			for _, it := range *ol {
				if !slices.ContainsFunc(*nl, func(t string) bool { return covers(t, it) }) {
					restrict("%s: tira %s", field, it)
				}
			}
		}
	}
	// What every class gets denied or asked: an old pattern the new lists no
	// longer surely hold widens (a deny that becomes an ask widens too).
	limitList := func(field string, ol, nl *[]string, stronger *[]string, covers func(t, c string) bool) {
		for _, it := range deref(ol) {
			held := func(l *[]string) bool {
				return slices.ContainsFunc(deref(l), func(t string) bool { return covers(t, it) })
			}
			if !held(nl) && !held(stronger) {
				widen("%s: deixa de valer %s", field, it)
			}
		}
		for _, it := range deref(nl) {
			if !slices.ContainsFunc(deref(ol), func(t string) bool { return covers(t, it) }) {
				restrict("%s: %s", field, it)
			}
		}
	}
	verdict := func(field string, ov, nv *Verdict) {
		a, b := valueOr(ov, Allow), valueOr(nv, Allow)
		switch {
		case b.rank() < a.rank():
			widen("%s: %s → %s", field, a, b)
		case b.rank() > a.rank():
			restrict("%s: %s → %s", field, a, b)
		}
	}
	flag := func(field string, ov, nv *bool) {
		limited := func(p *bool) bool { return p != nil && !*p }
		switch {
		case limited(ov) && !limited(nv):
			widen("%s: false → livre", field)
		case !limited(ov) && limited(nv):
			restrict("%s: → false", field)
		}
	}

	of := ceilingFields(o)
	nf := ceilingFields(n)
	allowList("files.read", of.read, nf.read, coversGlob, false)
	allowList("files.write", of.write, nf.write, coversGlob, false)
	limitList("files.deny", of.fdeny, nf.fdeny, nil, coversGlob)
	allowList("commands.allow", of.callow, nf.callow, coversText, false)
	limitList("commands.deny", of.cdeny, nf.cdeny, nil, coversText)
	limitList("commands.ask", of.cask, nf.cask, nf.cdeny, coversText)
	verdict("commands.default", of.cdefault, nf.cdefault)
	allowList("tools.allow", of.tallow, nf.tallow, coversText, false)
	limitList("tools.deny", of.tdeny, nf.tdeny, nil, coversText)
	limitList("tools.ask", of.task, nf.task, nf.tdeny, coversText)
	verdict("tools.default", of.tdefault, nf.tdefault)
	flag("git.commit", of.commit, nf.commit)
	allowList("git.branches", of.branches, nf.branches, coversGlob, false)
	flag("git.request_push", of.push, nf.push)
	ow, nw := valueOr(of.workflows, WorkflowsAllow), valueOr(nf.workflows, WorkflowsAllow)
	switch {
	case nw.rank() > ow.rank():
		widen("git.workflows: %s → %s", ow, nw)
	case nw.rank() < ow.rank():
		restrict("git.workflows: %s → %s", ow, nw)
	}
	flag("network.internet", of.internet, nf.internet)
	allowList("network.domains", of.domains, nf.domains, coversDomain, true)
	allowList("network.credentials", of.creds, nf.creds, func(t, c string) bool { return t == c }, false)
	allowList("cli.allow", of.cli, nf.cli, coversText, false)
	if !*o.Operator && *n.Operator {
		d.OperatorOn = true
		widen("operator: false → true")
	} else if *o.Operator && !*n.Operator {
		restrict("operator: → false")
	}

	// Cost: an absent unit or field has no ceiling.
	var oc, nc map[Unit]*CostLimits
	if approved != nil {
		oc = approved.Cost.units()
	}
	if next != nil {
		nc = next.Cost.units()
	}
	for _, u := range []Unit{UnitUSD, UnitTokens, UnitHours} {
		ol, nl := oc[u], nc[u]
		for _, s := range []Scope{ScopeTable, ScopeDay} {
			a, b := ol.of(s), nl.of(s)
			switch {
			case a == nil && b == nil:
			case a == nil:
				restrict("cost.%s.%s: %v", u, s, *b)
			case b == nil:
				widen("cost.%s.%s: deixa de limitar (era %v)", u, s, *a)
			case *b > *a:
				widen("cost.%s.%s: %v → %v", u, s, *a, *b)
			case *b < *a:
				restrict("cost.%s.%s: %v → %v", u, s, *a, *b)
			}
		}
	}
	return d
}

type fields struct {
	read, write, fdeny, callow, cdeny, cask, tallow, tdeny, task, branches, domains, creds, cli *[]string
	cdefault, tdefault                                                                          *Verdict
	commit, push, internet                                                                      *bool
	workflows                                                                                   *Workflows
}

func ceilingFields(p *Permissions) fields {
	var f fields
	if p.Files != nil {
		f.read, f.write, f.fdeny = p.Files.Read, p.Files.Write, p.Files.Deny
	}
	if c := p.Commands; c != nil {
		f.callow, f.cdeny, f.cask, f.cdefault = c.Allow, c.Deny, c.Ask, c.Default
	}
	if t := p.Tools; t != nil {
		f.tallow, f.tdeny, f.task, f.tdefault = t.Allow, t.Deny, t.Ask, t.Default
	}
	if g := p.Git; g != nil {
		f.commit, f.branches, f.push, f.workflows = g.Commit, g.Branches, g.RequestPush, g.Workflows
	}
	if n := p.Network; n != nil {
		f.internet, f.domains, f.creds = n.Internet, n.Domains, n.Credentials
	}
	if p.CLI != nil {
		f.cli = p.CLI.Allow
	}
	return f
}

// Via is how a change of the ceiling arrives.
type Via string

// The ways a ceiling changes: the panel (restrictions; widening there
// creates a decree) or a permanent decree.
const (
	ViaPanel  Via = "panel"
	ViaDecree Via = "decree"
)

// Authority is who changes the ceiling, and how
// (docs/classes/teto-do-workspace.md#quem-muda-o-teto).
type Authority struct {
	// Level of the user. The Grand Master and the Mestre da mesa only
	// propose: they are not an authority.
	Level Level
	Via   Via
	// Decree is the permanent decree, with ViaDecree.
	Decree int64
}

// Errors of a change of the ceiling.
var (
	// ErrCeilingProposeOnly: below Administrator, a change is only a
	// proposal taken to the users.
	ErrCeilingProposeOnly = errors.New("only an Administrator or the Owner changes the ceiling; the others propose")
	// ErrCeilingNeedsDecree: widening is only by permanent decree (widening
	// on the panel creates that decree, with the author).
	ErrCeilingNeedsDecree = errors.New("widening the ceiling needs a permanent decree")
	// ErrCeilingOwnerOnly: only the Owner turns operator on, by a ceiling
	// decree with PIN. It is refused before any confirmation.
	ErrCeilingOwnerOnly = errors.New("only the Owner turns operator on in the ceiling")
)

// AuthorizeCeiling checks who may make the change: an Administrator or the
// Owner restricts on the panel or by decree; widening is only by permanent
// decree; turning operator on, only the Owner. The decree flow calls it
// before asking for the confirmation, so a refused change never reaches the
// PIN.
func AuthorizeCeiling(d CeilingDiff, by Authority) error {
	if by.Level != LevelOwner && by.Level != LevelAdmin {
		return fmt.Errorf("%w (level %q)", ErrCeilingProposeOnly, by.Level)
	}
	switch by.Via {
	case ViaPanel:
	case ViaDecree:
		if by.Decree <= 0 {
			return fmt.Errorf("%w: no decree given", ErrCeilingNeedsDecree)
		}
	default:
		return fmt.Errorf("ceiling change: unknown way %q", by.Via)
	}
	if d.OperatorOn && by.Level != LevelOwner {
		return ErrCeilingOwnerOnly
	}
	if !d.OnlyRestricts() && by.Via != ViaDecree {
		return fmt.Errorf("%w: %v", ErrCeilingNeedsDecree, d.Widens)
	}
	return nil
}
