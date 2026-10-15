package classes

import (
	"fmt"
	"slices"
)

// SheetReview is what the restrictions of a character sheet say against the
// permissions of its class (docs/classes/permissoes.md, "a ficha só
// restringe"). The messages are for the Owner, who reads them when the sheet
// is saved.
type SheetReview struct {
	// Refused are the places where the sheet gives what the class forbids
	// (an allow that a deny of the class covers, a switch the class turned
	// off): the sheet is not saved.
	Refused []string
	// Ignored are the places where the sheet allows what the class does not
	// give: Build ignores them, and saving the sheet warns.
	Ignored []string
}

// OK is true when nothing is refused.
func (r SheetReview) OK() bool { return len(r.Refused) == 0 }

// ReviewSheet reads the restrictions of a sheet against the permissions of
// its class. A nil class (not found, or not yet written) is read as a class
// that gives nothing special: only what the sheet itself cannot mean is
// refused, which is nothing, so the result is empty.
func ReviewSheet(class, sheet *Permissions) SheetReview {
	var r SheetReview
	if class == nil || sheet == nil {
		return r
	}
	refuse := func(format string, a ...any) { r.Refused = append(r.Refused, fmt.Sprintf(format, a...)) }
	ignore := func(format string, a ...any) { r.Ignored = append(r.Ignored, fmt.Sprintf(format, a...)) }

	// Lists of patterns: an allow that the class denies is a release.
	type pair struct {
		field        string
		sheet, deny  []string
		allowed      []string
		checkAllowed bool
		covers       func(t, c string) bool
	}
	var pairs []pair
	if s := sheet.Commands; s != nil {
		pairs = append(pairs, pair{"commands.allow", deref(s.Allow), deref(ptrOf(class.Commands, "deny")), deref(ptrOf(class.Commands, "allow")), true, coversText})
	}
	if s := sheet.Tools; s != nil {
		var deny, allow []string
		if class.Tools != nil {
			deny, allow = deref(class.Tools.Deny), deref(class.Tools.Allow)
		}
		pairs = append(pairs, pair{"tools.allow", deref(s.Allow), deny, allow, true, coversText})
	}
	if s := sheet.CLI; s != nil {
		pairs = append(pairs, pair{"cli.allow", deref(s.Allow), nil, deref(cliAllow(class)), true, coversText})
	}
	if s := sheet.Files; s != nil {
		deny := deref(filesPtr(class, "deny"))
		pairs = append(pairs,
			pair{"files.read", deref(s.Read), deny, nil, false, coversGlob},
			pair{"files.write", deref(s.Write), deny, nil, false, coversGlob})
	}
	for _, p := range pairs {
		for _, item := range p.sheet {
			if d, ok := firstCovering(p.deny, item, p.covers); ok {
				refuse("%s `%s`: a classe proíbe `%s`; a ficha só restringe", p.field, item, d)
				continue
			}
			if p.checkAllowed {
				if _, ok := firstCovering(p.allowed, item, p.covers); !ok {
					ignore("%s `%s`: a classe não dá isso; a ficha só restringe, então é ignorado", p.field, item)
				}
			}
		}
	}

	// Switches: true in the sheet gives what the class has to give.
	type flag struct {
		field        string
		sheet, class *bool
		// absentIsOff: a class without the field does not give it.
		absentIsOff bool
	}
	flags := []flag{}
	if s := sheet.Git; s != nil {
		c := class.Git
		flags = append(flags,
			flag{"git.commit", s.Commit, gitBool(c, "commit"), true},
			flag{"git.request_push", s.RequestPush, gitBool(c, "request_push"), true})
	}
	if s := sheet.Network; s != nil && class.Network != nil {
		flags = append(flags, flag{"network.internet", s.Internet, class.Network.Internet, false})
	}
	flags = append(flags, flag{"operator", sheet.Operator, class.Operator, true})
	for _, f := range flags {
		if f.sheet == nil || !*f.sheet {
			continue
		}
		switch {
		case f.class != nil && !*f.class:
			refuse("%s: a classe desliga; a ficha só restringe", f.field)
		case f.class == nil && f.absentIsOff:
			ignore("%s: a classe não dá isso; a ficha só restringe, então é ignorado", f.field)
		}
	}

	// Defaults: a looser default than the class's is ignored.
	var cmdDefault, toolDefault *Verdict
	if sheet.Commands != nil {
		cmdDefault = sheet.Commands.Default
	}
	if sheet.Tools != nil {
		toolDefault = sheet.Tools.Default
	}
	for _, d := range []struct {
		field string
		sheet *Verdict
		class Verdict
	}{
		{"commands.default", cmdDefault, commandsDefault(class)},
		{"tools.default", toolDefault, toolsDefault(class)},
	} {
		if d.sheet != nil && d.sheet.rank() < d.class.rank() {
			ignore("%s: %s é mais frouxo que o da classe; a ficha só restringe, então é ignorado", d.field, *d.sheet)
		}
	}
	slices.Sort(r.Refused)
	slices.Sort(r.Ignored)
	return r
}

func firstCovering(list []string, item string, covers func(t, c string) bool) (string, bool) {
	for _, t := range list {
		if covers(t, item) {
			return t, true
		}
	}
	return "", false
}

func cliAllow(p *Permissions) *[]string {
	if p.CLI == nil {
		return nil
	}
	return p.CLI.Allow
}

func gitBool(g *GitSpec, which string) *bool {
	if g == nil {
		return nil
	}
	if which == "commit" {
		return g.Commit
	}
	return g.RequestPush
}
