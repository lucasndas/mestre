package classes_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
)

func permission(id int64, l classes.Level, allow bool, g classes.Grant) classes.Decree {
	d := classes.Decree{ID: id, Level: l, Effect: classes.Effect{Kind: classes.EffectPermission}}
	if allow {
		d.Effect.Allow = &g
	} else {
		d.Effect.Deny = &g
	}
	return d
}

func exceptionFor(id int64, l classes.Level, a classes.Action) classes.Decree {
	return classes.Decree{ID: id, Kind: classes.DecreeException, Level: l, Effect: classes.Effect{Kind: classes.EffectPermission}, Action: &a}
}

// The decisions of the agents of the approval chain only turn an ask into
// allow (docs/arquitetura/precedencia.md#combinação-na-decisão-de-permissão):
// never over a deny of the class, of a person's decree or of a part of the
// line.
func TestAgentDecisionsNeverBeatADeny(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	const denySrc = "version: 1\nfiles: {write: [\"tests/**\"], deny: [\"src/**\"]}\n"
	for _, l := range []classes.Level{classes.LevelGrandMaster, classes.LevelTableMaster} {
		// An exception over the class's files.deny.
		r := rules(t, denySrc, exceptionFor(50, l, classes.Action{Kind: providers.KindEdit, Paths: []string{"src/a.ts"}}))
		want(t, decide(env, r, m.file(providers.KindEdit, m.in("src/a.ts"))), classes.Deny, classes.LayerClass, "files.deny")
		// A rule over the class's files.deny, and over the files.write it lacks.
		r = rules(t, denySrc, permission(51, l, true, classes.Grant{Files: []string{"src/**", "docs/**"}}))
		want(t, decide(env, r, m.file(providers.KindEdit, m.in("src/b.ts"))), classes.Deny, classes.LayerClass, "files.deny")
		want(t, decide(env, r, m.file(providers.KindEdit, m.in("docs/b.md"))), classes.Deny, classes.LayerClass, "files.write")
		// A rule and an exception over commands.deny and a default deny.
		r = rules(t, qaYAML, permission(52, l, true, classes.Grant{Commands: []string{"curl *"}}),
			exceptionFor(53, l, classes.Action{Kind: providers.KindShell, Command: "curl https://x.io"}))
		want(t, decide(env, r, m.shell("curl https://x.io")), classes.Deny, classes.LayerClass, "commands.deny")
		r = rules(t, "version: 1\ncommands: {default: deny}\n", permission(54, l, true, classes.Grant{Commands: []string{"foo *"}}),
			exceptionFor(55, l, classes.Action{Kind: providers.KindShell, Command: "foo --bar"}))
		want(t, decide(env, r, m.shell("foo --bar")), classes.Deny, classes.LayerClass, "padrão é negar")
		// The tools block and the network block.
		r = rules(t, qaYAML, permission(56, l, true, classes.Grant{Tools: []string{"mcp__x__delete_y"}, Domains: []string{"x.io"}}))
		want(t, decide(env, r, toolEvent(providers.KindMCP, "mcp__x__delete_y")), classes.Deny, classes.LayerClass, "tools.deny")
		web := event(&providers.Tool{NativeName: "WebFetch", Kind: providers.KindWeb, URL: "https://x.io/a"}, m.wt)
		want(t, decide(env, r, web), classes.Deny, classes.LayerClass, "internet")
		// A part denied: the exception of the whole line does not count.
		line := "npm install x && curl https://x.io"
		r = rules(t, qaYAML, exceptionFor(57, l, classes.Action{Kind: providers.KindShell, Command: line}))
		want(t, decide(env, r, m.shell(line)), classes.Deny, classes.LayerClass, "commands.deny")
		// Under a deny decree of a person.
		r = rules(t, qaYAML, permission(58, classes.LevelApprover, false, classes.Grant{Commands: []string{"npm install *"}}),
			permission(59, l, true, classes.Grant{Commands: []string{"npm install *"}}),
			exceptionFor(60, l, classes.Action{Kind: providers.KindShell, Command: "npm install x"}))
		want(t, decide(env, r, m.shell("npm install x")), classes.Deny, classes.LayerDecree, "decreto 58")

		// Over an ask they do turn it into allow.
		r = rules(t, qaYAML, permission(61, l, true, classes.Grant{Commands: []string{"npm install *"}}))
		d := decide(env, r, m.shell("npm install y"))
		want(t, d, classes.Allow, classes.LayerDecree)
		if d.Decree != 61 {
			t.Fatalf("decided by decree %d; want 61", d.Decree)
		}
		r = rules(t, qaYAML, exceptionFor(62, l, classes.Action{Kind: providers.KindShell, Command: "npm install z"}))
		want(t, decide(m.env(), r, m.shell("npm install z")), classes.Allow, classes.LayerException)
		// A deny of theirs only hardens.
		r = rules(t, qaYAML, permission(63, l, false, classes.Grant{Commands: []string{"npm test"}}))
		want(t, decide(env, r, m.shell("npm test")), classes.Deny, classes.LayerDecree, "decreto 63")
	}
	// A person's exception still frees what the class denies (step 1).
	r := rules(t, denySrc, exceptionFor(64, classes.LevelApprover, classes.Action{Kind: providers.KindEdit, Paths: []string{"src/a.ts"}}))
	want(t, decide(env, r, m.file(providers.KindEdit, m.in("src/a.ts"))), classes.Allow, classes.LayerException)
}

// A decree of an unknown level is refused by the document and, in rules
// that were not validated, never counts.
func TestDecreeOfAnUnknownLevel(t *testing.T) {
	t.Parallel()
	m := newMachine()
	for _, l := range []classes.Level{"", "whatever", "Owner"} {
		r := rules(t, "version: 1\nfiles: {write: [\"tests/**\"], deny: [\"src/**\"]}\n")
		r.Decrees = append(r.Decrees, permission(70, l, true, classes.Grant{Files: []string{"src/**"}}),
			exceptionFor(71, l, classes.Action{Kind: providers.KindEdit, Paths: []string{"src/a.ts"}}))
		if err := r.Validate(); err == nil {
			t.Fatalf("level %q: the document was accepted", l)
		}
		want(t, decide(m.env(), r, m.file(providers.KindEdit, m.in("src/a.ts"))), classes.Deny, classes.LayerClass)
		want(t, decide(m.env(), r, m.file(providers.KindEdit, m.in("src/b.ts"))), classes.Deny, classes.LayerClass)
	}
}
