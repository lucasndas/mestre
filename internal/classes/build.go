package classes

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Layers are the inputs of the effective rules of one character, as the
// Citadel has them (docs/arquitetura/precedencia.md#combinação-na-decisão-de-permissão).
type Layers struct {
	AgentID, Class string
	// Permissions is the class's permissions.yaml (required).
	Permissions *Permissions
	// Ceiling is the permissions block of the workspace ceiling, read as a
	// maximum; nil limits nothing.
	Ceiling *Permissions
	// Sheet is the restrictions of the character's sheet: it only hardens.
	Sheet *Permissions
	// Decrees are the active decrees and exceptions that apply to the
	// character; the expired ones are dropped.
	Decrees []Decree
	// Placeholders resolve the markers of the patterns ("mesa.branch").
	Placeholders map[string]string
	// Locked is the locked system (/travar).
	Locked bool
	// Now is generated_at.
	Now time.Time
}

// Build combines the layers into the effective rules: the class cut by the
// ceiling and hardened by the sheet. The cut is conservative: a pattern of
// the class that a maximum does not surely contain is narrowed to the
// maximum's pattern it contains, or dropped. deny and ask lists add up;
// a default, a flag or a mode takes the most restrictive value. Every
// pattern or field that does not come from the class records its layer in
// origin. A placeholder without a value is an error: the document never
// carries one.
func Build(l Layers) (*Rules, error) {
	if l.Permissions == nil {
		return nil, fmt.Errorf("effective rules: no permissions for class %q", l.Class)
	}
	if !ValidAgentID(l.AgentID) {
		return nil, fmt.Errorf("effective rules: invalid agent id %q", l.AgentID)
	}
	b := builder{origin: map[string]Layer{}}
	caps := []layered{{LayerCeiling, l.Ceiling}, {LayerSheet, l.Sheet}}
	cls := l.Permissions

	r := &Rules{
		Version: RulesVersion, AgentID: l.AgentID, Class: l.Class, GeneratedAt: l.Now.UTC(),
		FixedRulesVersion: FixedRulesVersion, Locked: l.Locked,
	}

	// files
	r.Files.Read = b.capped("files.read", or(filesList(cls, "read"), []string{"**"}), caps, func(p *Permissions) *[]string { return filesPtr(p, "read") }, coversGlob)
	r.Files.Write = b.capped("files.write", filesList(cls, "write"), caps, func(p *Permissions) *[]string { return filesPtr(p, "write") }, coversGlob)
	r.Files.Deny = b.union("files.deny", filesList(cls, "deny"), caps, func(p *Permissions) *[]string { return filesPtr(p, "deny") })

	// commands
	cmd := func(p *Permissions) *CommandsSpec {
		if p == nil {
			return nil
		}
		return p.Commands
	}
	r.Commands.Deny = b.union("commands.deny", deref(ptrOf(cmd(cls), "deny")), caps, func(p *Permissions) *[]string { return ptrOf(cmd(p), "deny") })
	r.Commands.Ask = b.union("commands.ask", deref(ptrOf(cmd(cls), "ask")), caps, func(p *Permissions) *[]string { return ptrOf(cmd(p), "ask") })
	r.Commands.Allow = b.capped("commands.allow", deref(ptrOf(cmd(cls), "allow")), caps, func(p *Permissions) *[]string { return ptrOf(cmd(p), "allow") }, coversText)
	r.Commands.Allow = b.dropDenied("commands.allow", r.Commands.Allow, r.Commands.Deny, coversText)
	r.Commands.Default = b.verdict("commands.default", commandsDefault(cls), caps, func(p *Permissions) *Verdict {
		if c := cmd(p); c != nil {
			return c.Default
		}
		return nil
	})

	// tools
	tl := func(p *Permissions) *ToolsSpec {
		if p == nil {
			return nil
		}
		return p.Tools
	}
	toolsPtr := func(p *Permissions, which string) *[]string {
		t := tl(p)
		if t == nil {
			return nil
		}
		return map[string]*[]string{"allow": t.Allow, "ask": t.Ask, "deny": t.Deny}[which]
	}
	r.Tools.Deny = b.union("tools.deny", deref(toolsPtr(cls, "deny")), caps, func(p *Permissions) *[]string { return toolsPtr(p, "deny") })
	r.Tools.Ask = b.union("tools.ask", deref(toolsPtr(cls, "ask")), caps, func(p *Permissions) *[]string { return toolsPtr(p, "ask") })
	r.Tools.Allow = b.capped("tools.allow", deref(toolsPtr(cls, "allow")), caps, func(p *Permissions) *[]string { return toolsPtr(p, "allow") }, coversText)
	r.Tools.Allow = b.dropDenied("tools.allow", r.Tools.Allow, r.Tools.Deny, coversText)
	r.Tools.Default = b.verdict("tools.default", valueOr(cls.Default, Deny), caps, func(p *Permissions) *Verdict {
		if p == nil {
			return nil
		}
		return p.Default
	})

	// git
	gitOf := func(p *Permissions) *GitSpec {
		if p == nil {
			return nil
		}
		return p.Git
	}
	g := gitOf(cls)
	var err error
	resolve := func(list []string) []string {
		out, e := resolvePlaceholders(list, l.Placeholders)
		if e != nil && err == nil {
			err = e
		}
		return out
	}
	var branches []string
	if g != nil {
		branches = resolve(deref(g.Branches))
	}
	resolvedCaps := make([]layered, 0, len(caps))
	for _, c := range caps {
		if gc := gitOf(c.p); gc != nil && gc.Branches != nil {
			rb := resolve(*gc.Branches)
			cp := *c.p
			gcopy := *gc
			gcopy.Branches = &rb
			cp.Git = &gcopy
			c.p = &cp
		}
		resolvedCaps = append(resolvedCaps, c)
	}
	if err != nil {
		return nil, err
	}
	r.Git.Branches = b.capped("git.branches", branches, resolvedCaps, func(p *Permissions) *[]string {
		if gc := gitOf(p); gc != nil {
			return gc.Branches
		}
		return nil
	}, coversGlob)
	r.Git.Commit = b.flag("git.commit", g != nil && valueOr(g.Commit, false), caps, func(p *Permissions) *bool {
		if gc := gitOf(p); gc != nil {
			return gc.Commit
		}
		return nil
	})
	if r.Git.Commit {
		// A deny pattern of any layer that takes git commit turns the flag off.
		for _, d := range r.Commands.Deny {
			if matchText(d, "git commit") {
				r.Git.Commit = false
				if layer := b.layerOf("commands.deny", d); layer != LayerClass {
					b.origin["git.commit"] = layer
				}
				break
			}
		}
	}
	r.Git.RequestPush = b.flag("git.request_push", g != nil && valueOr(g.RequestPush, false), caps, func(p *Permissions) *bool {
		if gc := gitOf(p); gc != nil {
			return gc.RequestPush
		}
		return nil
	})
	r.Git.Workflows = WorkflowsNever
	if g != nil && g.Workflows != nil {
		r.Git.Workflows = *g.Workflows
	}
	for _, c := range caps {
		if gc := gitOf(c.p); gc != nil && gc.Workflows != nil && gc.Workflows.rank() < r.Git.Workflows.rank() {
			r.Git.Workflows = *gc.Workflows
			b.origin["git.workflows"] = c.layer
		}
	}

	// network
	netOf := func(p *Permissions) *NetworkSpec {
		if p == nil {
			return nil
		}
		return p.Network
	}
	n := netOf(cls)
	r.Network.Internet = b.flag("network.internet", n != nil && valueOr(n.Internet, false), caps, func(p *Permissions) *bool {
		if nc := netOf(p); nc != nil {
			return nc.Internet
		}
		return nil
	})
	var domains, creds []string
	if n != nil {
		domains, creds = deref(n.Domains), deref(n.Credentials)
	}
	r.Network.Domains = slices.Clone(domains)
	for _, c := range caps {
		nc := netOf(c.p)
		if nc == nil || nc.Domains == nil || len(*nc.Domains) == 0 {
			continue // absent, or any domain
		}
		if len(r.Network.Domains) == 0 {
			// Any domain, capped: the maximum's list.
			r.Network.Domains = slices.Clone(*nc.Domains)
			for _, d := range *nc.Domains {
				b.origin["network.domains:"+d] = c.layer
			}
			continue
		}
		r.Network.Domains = b.intersect("network.domains", r.Network.Domains, *nc.Domains, c.layer, coversDomain)
		if len(r.Network.Domains) == 0 && r.Network.Internet {
			// Nothing left: no domain, not "any domain".
			r.Network.Internet = false
			b.origin["network.internet"] = c.layer
		}
	}
	r.Network.Credentials = slices.Clone(creds)
	for _, c := range caps {
		if nc := netOf(c.p); nc != nil && nc.Credentials != nil {
			r.Network.Credentials = slices.DeleteFunc(r.Network.Credentials, func(s string) bool { return !slices.Contains(*nc.Credentials, s) })
		}
	}

	// cli
	cliOf := func(p *Permissions) *[]string {
		if p == nil || p.CLI == nil {
			return nil
		}
		return p.CLI.Allow
	}
	r.CLI.Allow = b.capped("cli.allow", deref(cliOf(cls)), caps, cliOf, coversText)

	r.Operator = b.flag("operator", valueOr(cls.Operator, false), caps, func(p *Permissions) *bool {
		if p == nil {
			return nil
		}
		return p.Operator
	})

	for _, d := range l.Decrees {
		if d.active(l.Now) {
			r.Decrees = append(r.Decrees, d)
		}
	}
	r.Origin = b.origin
	normalizeNil(r)
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return r, nil
}

type layered struct {
	layer Layer
	p     *Permissions
}

type builder struct{ origin map[string]Layer }

func (b *builder) layerOf(list, pattern string) Layer {
	if l, ok := b.origin[list+":"+pattern]; ok {
		return l
	}
	return LayerClass
}

// capped is a list of what may be given, cut by every maximum present.
func (b *builder) capped(name string, class []string, caps []layered, get func(*Permissions) *[]string, covers func(t, c string) bool) []string {
	out := dedupe(class)
	for _, c := range caps {
		if c.p == nil {
			continue
		}
		if max := get(c.p); max != nil {
			out = b.intersect(name, out, *max, c.layer, covers)
		}
	}
	return out
}

// intersect keeps each item that a pattern of max covers; an item that
// covers patterns of max is narrowed to them (with the layer as origin);
// the rest is dropped.
func (b *builder) intersect(name string, items, max []string, layer Layer, covers func(t, c string) bool) []string {
	var out []string
	for _, it := range items {
		kept := false
		for _, t := range max {
			if covers(t, it) {
				out = append(out, it)
				kept = true
				break
			}
		}
		if kept {
			continue
		}
		for _, t := range max {
			if covers(it, t) && !slices.Contains(out, t) {
				out = append(out, t)
				b.origin[name+":"+t] = layer
			}
		}
	}
	return dedupe(out)
}

// union adds the lists of the maximums and of the sheet to the class's.
func (b *builder) union(name string, class []string, caps []layered, get func(*Permissions) *[]string) []string {
	out := dedupe(class)
	for _, c := range caps {
		if c.p == nil {
			continue
		}
		for _, s := range deref(get(c.p)) {
			if !slices.Contains(out, s) {
				out = append(out, s)
				b.origin[name+":"+s] = c.layer
			}
		}
	}
	return out
}

// dropDenied removes the allowed patterns a deny of another layer covers:
// the deny decides anyway, and the document says it once.
func (b *builder) dropDenied(name string, allow, deny []string, covers func(t, c string) bool) []string {
	return slices.DeleteFunc(allow, func(a string) bool {
		for _, d := range deny {
			if b.layerOf(strings.Replace(name, ".allow", ".deny", 1), d) != LayerClass && covers(d, a) {
				return true
			}
		}
		return false
	})
}

func (b *builder) verdict(name string, class Verdict, caps []layered, get func(*Permissions) *Verdict) Verdict {
	v := class
	for _, c := range caps {
		if c.p == nil {
			continue
		}
		if m := get(c.p); m != nil && m.rank() > v.rank() {
			v = *m
			b.origin[name] = c.layer
		}
	}
	return v
}

// flag is a permission flag: true only if the class gives it and no
// maximum present takes it away.
func (b *builder) flag(name string, class bool, caps []layered, get func(*Permissions) *bool) bool {
	v := class
	for _, c := range caps {
		if c.p == nil {
			continue
		}
		if m := get(c.p); m != nil && !*m && v {
			v = false
			b.origin[name] = c.layer
		}
	}
	return v
}

func resolvePlaceholders(list []string, values map[string]string) ([]string, error) {
	out := make([]string, 0, len(list))
	for _, s := range list {
		var missing string
		r := placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
			key := m[1 : len(m)-1]
			v, ok := values[key]
			if !ok || v == "" || strings.ContainsAny(v, "{}") {
				missing = key
				return m
			}
			return v
		})
		if missing != "" {
			return nil, fmt.Errorf("effective rules: placeholder {%s} has no value", missing)
		}
		out = append(out, r)
	}
	return out, nil
}

func filesPtr(p *Permissions, which string) *[]string {
	if p == nil || p.Files == nil {
		return nil
	}
	switch which {
	case "read":
		return p.Files.Read
	case "write":
		return p.Files.Write
	}
	return p.Files.Deny
}

func filesList(p *Permissions, which string) []string {
	l := filesPtr(p, which)
	if l == nil {
		return nil
	}
	out := make([]string, len(*l))
	for i, g := range *l {
		out[i] = strings.ReplaceAll(g, `\`, "/")
	}
	return out
}

func ptrOf(c *CommandsSpec, which string) *[]string {
	if c == nil {
		return nil
	}
	switch which {
	case "allow":
		return c.Allow
	case "ask":
		return c.Ask
	}
	return c.Deny
}

// commandsDefault is the class default for unmatched commands: absent is
// deny.
func commandsDefault(p *Permissions) Verdict {
	if p.Commands != nil && p.Commands.Default != nil {
		return *p.Commands.Default
	}
	return Deny
}

func or(l, def []string) []string {
	if l == nil {
		return def
	}
	return l
}

func valueOr[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

func dedupe(l []string) []string {
	out := make([]string, 0, len(l))
	for _, s := range l {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// normalizeNil makes the lists of the document [] rather than null.
func normalizeNil(r *Rules) {
	for _, l := range []*[]string{
		&r.Files.Read, &r.Files.Write, &r.Files.Deny, &r.Commands.Allow, &r.Commands.Ask, &r.Commands.Deny,
		&r.Tools.Allow, &r.Tools.Ask, &r.Tools.Deny, &r.Git.Branches, &r.Network.Domains, &r.Network.Credentials, &r.CLI.Allow,
	} {
		if *l == nil {
			*l = []string{}
		}
	}
	if r.Decrees == nil {
		r.Decrees = []Decree{}
	}
}
