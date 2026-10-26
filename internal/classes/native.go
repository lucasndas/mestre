package classes

import (
	"slices"
	"strings"

	"github.com/mestre-ia/mestre/internal/providers"
)

// FixedDenyCommands are the commands of the fixed rules that every provider
// that can expresses natively (docs/classes/reforco-nativo.md#tradução-por-provedor):
// git push and changing the remotes.
var FixedDenyCommands = []string{"git push *", "git remote *"}

// NativeCharacter is the character whose native controls are translated.
type NativeCharacter struct {
	AgentID  string
	Provider providers.ID
	OS       string
	// Worktree is its worktree, absolute; ProfileHome the HOME of its account
	// profile.
	Worktree, ProfileHome string
	// GitDir and Mirror are the engine's record of the worktree, as
	// gitx.Real.Record gave them when the worktree was created: its git
	// folder in the mirror and the mirror. Empty for the main clone.
	GitDir, Mirror string
	// HookEnv are the MESTRE_CLI and MESTRE_ENDPOINT of the character, pinned
	// in the settings file of Claude Code (providers.NativePolicy.HookEnv).
	HookEnv []string
}

// NativePolicy is the part of the effective rules the native controls can
// hold. Decrees never become native rules (they change at any moment), but
// a native block that a listed decree or exception frees is left out, so
// the native barrier never stops what the dispatcher allows: the decrees and
// exceptions that free files lift the native denial of their paths and of
// writing, those of commands the denial of their commands, those of domains
// or URLs the network. The fixed rules are never lifted. ask, tools of kind
// mcp and the locked system stay with the dispatcher.
func NativePolicy(r *Rules, ch NativeCharacter, fb providers.NativeFallbacks) providers.NativePolicy {
	lf := liftsOf(r, ch.Worktree)
	p := providers.NativePolicy{
		AgentID: ch.AgentID, OS: ch.OS, Worktree: ch.Worktree, ProfileHome: ch.ProfileHome,
		GitDir: ch.GitDir, GitCommon: ch.Mirror, HookEnv: slices.Clone(ch.HookEnv),
		Write:             slices.Clone(r.Files.Write),
		NoWrite:           len(r.Files.Write) == 0 && !lf.write,
		ProtectedWrite:    slices.Clone(protectedWrite),
		AllowCommands:     slices.Clone(r.Commands.Allow),
		FixedDenyCommands: slices.Clone(FixedDenyCommands),
		Offline:           !r.Network.Internet && !lf.web,
		AnyDomain:         r.Network.Internet && len(r.Network.Domains) == 0,
		Fallbacks:         fb,
	}
	if r.Network.Internet {
		p.Domains = slices.Clone(r.Network.Domains)
	}
	for _, g := range r.Files.Deny {
		if !slices.ContainsFunc(lf.files, func(l string) bool { return mayOverlapGlob(g, l) }) {
			p.DenyFiles = append(p.DenyFiles, g)
		}
	}
	for _, c := range r.Commands.Deny {
		if !slices.ContainsFunc(lf.commands, func(l string) bool { return mayOverlapText(c, l) }) {
			p.DenyCommands = append(p.DenyCommands, c)
		}
	}
	tools := r.Tools
	p.ToolDenied = func(name string) bool {
		if slices.ContainsFunc(lf.tools, func(l string) bool { return matchText(l, name) }) {
			return false
		}
		has := func(l []string) bool {
			return slices.ContainsFunc(l, func(t string) bool { return matchText(t, name) })
		}
		switch {
		case has(tools.Deny):
			return true
		case has(tools.Allow), has(tools.Ask):
			return false
		}
		return tools.Default == Deny
	}
	return p
}

type lifts struct {
	files, commands, tools []string
	write, web             bool
}

func liftsOf(r *Rules, worktree string) lifts {
	var lf lifts
	for _, d := range r.Decrees {
		if d.Kind == DecreeTableRule {
			continue // a table rule never lifts a native block
		}
		if d.Kind == DecreeException {
			if a := d.Action; a != nil {
				switch a.Kind {
				case providers.KindShell:
					lf.commands = append(lf.commands, a.Command)
				case providers.KindWrite, providers.KindEdit:
					lf.write = true
					lf.files = append(lf.files, relPaths(a.Paths, worktree)...)
				case providers.KindRead, providers.KindSearch:
					lf.files = append(lf.files, relPaths(a.Paths, worktree)...)
				case providers.KindWeb:
					lf.web = true
				default:
					if a.Tool != "" {
						lf.tools = append(lf.tools, a.Tool)
					}
				}
			}
			continue
		}
		if d.Effect.Kind != EffectPermission || d.Effect.Allow == nil {
			continue
		}
		g := d.Effect.Allow
		if len(g.Files) > 0 {
			lf.write = true
			lf.files = append(lf.files, g.Files...)
		}
		lf.commands = append(lf.commands, g.Commands...)
		lf.tools = append(lf.tools, g.Tools...)
		if len(g.Domains) > 0 {
			lf.web = true
		}
	}
	return lf
}

func relPaths(paths []string, worktree string) []string {
	wt := strings.TrimSuffix(strings.ReplaceAll(worktree, `\`, "/"), "/")
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		s := strings.ReplaceAll(p, `\`, "/")
		if r, ok := strings.CutPrefix(s, wt+"/"); ok && wt != "" {
			s = r
		}
		out = append(out, s)
	}
	return out
}

// mayOverlapGlob is false only when no path can match both globs: their
// literal leading segments differ. It errs toward an overlap.
func mayOverlapGlob(a, b string) bool {
	sa, sb := splitSegs(a), splitSegs(b)
	for i := 0; i < len(sa) && i < len(sb); i++ {
		if strings.ContainsAny(sa[i], "*?[") || strings.ContainsAny(sb[i], "*?[") {
			return true
		}
		if !strings.EqualFold(sa[i], sb[i]) {
			return false
		}
	}
	return true
}

// mayOverlapText is false only when no text can match both patterns: the
// literal texts before their first * do not start one another.
func mayOverlapText(a, b string) bool {
	lit := func(s string) string {
		if i := strings.IndexByte(s, '*'); i >= 0 {
			return s[:i]
		}
		return s
	}
	x, y := strings.TrimSuffix(lit(a), " "), strings.TrimSuffix(lit(b), " ")
	return strings.HasPrefix(x, y) || strings.HasPrefix(y, x)
}
