package app_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/app"
	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
)

// withCitadelClasses gives the world the Citadel side of the effective rules
// as production has it: the builder over the libraries and the sheets, the
// publisher that keeps the document on the machine before a character starts,
// and the profiles of the gate from the registry and those rules.
func withCitadelClasses(w *commWorld, oo *app.OrchestrationOptions) {
	t := w.t
	reg := characters.Registry{DB: oo.Guild}
	cc, err := app.NewCitadelClasses(app.CitadelClassesOptions{Registry: reg, Libraries: func(string) *classes.Library { return w.lib }, Presets: oo.Presets})
	if err != nil {
		t.Fatal(err)
	}
	mdb, err := guildtpl.OpenMachine(t.Context(), filepath.Join(t.TempDir(), "maquina.db"), store.Options{Synchronous: "OFF", Clock: w.clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mdb.Close() })
	w.agents = t.TempDir()
	w.cc = cc
	w.local = &classes.Store{DB: mdb, Clock: w.clk, Agents: w.agents}
	w.pub = &app.RulesPublisher{Classes: cc, Local: w.local, Machine: oo.Machine}
	oo.ChainClasses, oo.ChainRulesPublisher = cc, w.pub
	oo.Machines = app.PublishingMachines(w.mach, w.pub)
	w.profiles = app.RegistryProfiles(reg, cc)
}

// approveClass puts an approved class in the workspace of the world.
func (w *commWorld) approveClass(name, perms string, extra ...classes.File) {
	w.t.Helper()
	files, err := classes.NewFiles(append([]classes.File{
		{Path: "CLASS.md", Data: []byte("---\nname: " + name + "\ndescription: Classe de teste\napp: novo\n---\nFaça o que a mesa pede.\n")},
		{Path: "permissions.yaml", Data: []byte(perms)},
	}, extra...))
	if err != nil {
		w.t.Fatal(err)
	}
	rec := classes.ClassRecord{Key: classes.ClassKey{Scope: classes.ClassWorkspace, Name: name}, Hash: files.Hash(), Files: files, ApprovedAt: w.clk.Now(),
		By: classes.Author{Level: classes.LevelAdmin, ID: "u1"}}
	if err := w.lib.Records.Approve(w.ctx, w.slug, rec, 0); err != nil {
		w.t.Fatal(err)
	}
}

const devPerms = "version: 1\nfiles:\n  read: [\"**\"]\n  write: [\"**\"]\ncommands:\n  allow: [\"npm test\"]\n  ask: [\"npm install *\"]\n  default: ask\ncli:\n  allow: [\"sheet list\"]\n"

const plainPerms = "version: 1\nfiles:\n  read: [\"**\"]\n  write: [\"**\"]\ncommands:\n  allow: [\"npm test\"]\n  default: ask\n"

func newCitadelWorld(t *testing.T) *commWorld {
	t.Helper()
	w := newCommWorld(t, withCitadelClasses)
	w.approveClass("dev", devPerms)
	w.approveClass("revisor", plainPerms)
	return w
}

// CA-ORQ-01
func TestTheCitadelBuildsTheRulesOfTheOrchestratorsFromTheBuiltInClassInEveryWorkspace(t *testing.T) {
	t.Parallel()
	w := newCitadelWorld(t)
	// A second workspace with the same ceiling and its own Grand Master and table.
	other, err := w.orc.Workspaces.Create(w.ctx, workspaces.CreateRequest{Repo: "lucas/Outro_App"})
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range workspaces.Stages {
		if st != workspaces.StageMirror {
			if err := w.orc.Workspaces.Register(st, func(context.Context, workspaces.Workspace) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.orc.Workspaces.Prepare(w.ctx, other.Slug); err != nil {
		t.Fatal(err)
	}
	res, err := w.orc.Characters.Create(w.ctx, characters.CreateRequest{Sheet: "Grão-Mestre", Workspace: other.Slug, Role: characters.RoleGrandMaster,
		Creator: characters.Creator{Kind: characters.CreatedByEngine}})
	if err != nil {
		t.Fatal(err)
	}
	gm2 := res.Character
	_, master2 := w.openTable(43)

	var all []*classes.Rules
	for _, c := range []characters.Character{w.gm, w.master, gm2, master2} {
		r, err := w.cc.Rules(w.ctx, c.ID)
		if err != nil {
			t.Fatalf("rules of %s: %v", c.Name, err)
		}
		if r.Class != classes.ReservedOrchestrator {
			t.Errorf("%s: class = %q, want the built-in %q", c.Name, r.Class, classes.ReservedOrchestrator)
		}
		if len(r.Files.Write) != 0 {
			t.Errorf("%s: files.write = %v, want empty", c.Name, r.Files.Write)
		}
		if r.Commands.Default != classes.Ask || r.Tools.Default != classes.Ask {
			t.Errorf("%s: commands.default = %s, tools.default = %s, want ask, ask", c.Name, r.Commands.Default, r.Tools.Default)
		}
		if r.Git.Commit != classes.Deny || r.Git.RequestPush != classes.Deny {
			t.Errorf("%s: git.commit = %s, git.request_push = %s, want deny, deny", c.Name, r.Git.Commit, r.Git.RequestPush)
		}
		r.AgentID = "x"
		// the branch of the table is the one placeholder that is not the same
		// in two tables
		r.Git.Branches = nil
		all = append(all, r)
	}
	// The Grand Master of the two workspaces has the same document (same ceiling),
	// and so have the two Masters.
	if !reflect.DeepEqual(all[0], all[2]) {
		t.Errorf("the Grand Masters differ between the workspaces:\n%+v\n%+v", all[0], all[2])
	}
	if !reflect.DeepEqual(all[1], all[3]) {
		t.Errorf("the Masters differ between the tables:\n%+v\n%+v", all[1], all[3])
	}
}

// CA-RTM-05
func TestTheCitadelEffectiveRulesCarryTheTableRuleAsADecreeAndTheSnapshotDoesNotUseIt(t *testing.T) {
	t.Parallel()
	w := newCitadelWorld(t)
	dev := w.recruit("Dev-R", "Dev Claude")
	until := w.clk.Now().Add(2 * time.Hour)
	act := classes.Action{Kind: providers.KindShell, Command: "npm install x"}
	pat, err := classes.CheckApprovablePattern("npm install *", act, dev.Class, w.table.ID)
	if err != nil {
		t.Fatal(err)
	}
	err = w.orc.TableRules.Create(w.ctx, chain.TempRule{Request: 7, Workspace: w.slug, Table: w.table.ID, Class: dev.Class, Requester: dev.ID, Pattern: &pat,
		Action: act, Risk: classes.RiskLow, Until: until, By: w.master.ID, Level: chain.Level{Kind: chain.LevelMesa, Agent: w.master.ID}})
	if err != nil {
		t.Fatal(err)
	}
	// the Citadel builds the document from the layers
	rules, err := w.cc.Rules(w.ctx, dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Decrees []struct {
			Kind, Level, Until string
			Effect             struct {
				Kind  string
				Allow struct{ Commands []string }
			}
		}
	}
	if err := json.Unmarshal(doc, &got); err != nil || len(got.Decrees) != 1 {
		t.Fatalf("document = %s (%v)", doc, err)
	}
	d := got.Decrees[0]
	at, perr := time.Parse(time.RFC3339, d.Until)
	if d.Kind != "table_rule" || d.Level != "table_master" || d.Effect.Kind != "permission" || !slices.Equal(d.Effect.Allow.Commands, []string{"npm install *"}) ||
		perr != nil || !at.Equal(until) {
		t.Errorf("decree = %+v (%s)", d, doc)
	}
	// it is what the machine keeps, and the snapshot the hook reads with the
	// engine down frees nothing
	if err := w.pub.Publish(w.ctx, dev.ID); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(w.agents, dev.ID, classes.SnapshotName))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := classes.ParseRules(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Decrees) != 1 || snap.Decrees[0].Kind != classes.DecreeTableRule {
		t.Fatalf("the snapshot decrees = %+v", snap.Decrees)
	}
	env, err := w.cc.Env(w.ctx, dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	tool := &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: "npm install y"}
	ev := providers.Event{Version: providers.EventVersion, Event: providers.EventPreTool, AgentID: dev.ID, TableID: dev.Table, Cwd: env.Worktree, Tool: tool, ReceivedAt: w.clk.Now()}
	if dec := classes.DecideDegraded(w.ctx, env, snap, ev); dec.Verdict == classes.Allow {
		t.Errorf("degraded = %+v, want not allowed", dec)
	}
	// with the engine up, the gate of the table rules lets the same call through
	live := (&classes.Policy{Source: w.local, Env: func(context.Context, string) (classes.Env, error) {
		e := env
		e.TableRules = w.cc.TableRules()
		return e, nil
	}}).Decide(w.ctx, ev)
	if live.Verdict != classes.Allow {
		t.Errorf("live = %+v, want allowed by the rule of the table", live)
	}
}

// The profile of the gate is the registry's level and the cli.allow of the
// effective rules the Citadel builds from the class (CA-CLASSES, cli.allow).
func TestTheGateTakesTheLevelFromTheRegistryAndTheCLIAllowFromTheClass(t *testing.T) {
	t.Parallel()
	w := newCitadelWorld(t)
	withAllow := w.recruit("Dev-A", "Dev Claude")  // class dev grants `sheet list`
	without := w.recruit("Rev-A", "Revisor Codex") // class revisor does not
	if r := w.run(withAllow, "sheet", "list", "--json"); r.code != 0 {
		t.Errorf("sheet list by a character whose class grants it = %+v", r)
	}
	r := w.run(without, "sheet", "list", "--json")
	if v := decodeJSON(t, r); r.code != 3 || v.Error.Code != "class_denied" {
		t.Errorf("sheet list by a character whose class does not = %d %s", r.code, r.stdout)
	}
	// the levels come from the registry
	if r := w.run(w.master, "sheet", "list", "--json"); r.code != 0 {
		t.Errorf("sheet list by the Master of the table = %+v", r)
	}
	if r := w.run(w.gm, "sheet", "list", "--json"); r.code != 0 {
		t.Errorf("sheet list by the Grand Master = %+v", r)
	}
	// whoever the registry does not know allows nothing
	if p := app.RegistryProfiles(characters.Registry{DB: w.orc.Registry.DB}, w.cc).Profile(w.ctx, "ninguem"); p.AllowsCLI != nil || p.Role != 0 {
		t.Errorf("unknown character: %+v", p)
	}
}

// A character starts only with its rules kept on its machine, and a class
// that cannot be read stops the start instead of widening anything.
func TestACharacterStartsWithItsEffectiveRulesKeptOnItsMachine(t *testing.T) {
	t.Parallel()
	w := newCitadelWorld(t)
	dev := w.recruit("Dev-S", "Dev Claude")
	kept, err := w.local.Rules(w.ctx, dev.ID)
	if err != nil || kept.Class != "dev" {
		t.Fatalf("rules kept = %+v, %v", kept, err)
	}
	if kept, err := w.local.Rules(w.ctx, w.master.ID); err != nil || kept.Class != classes.ReservedOrchestrator {
		t.Errorf("the Master's rules kept = %+v, %v", kept, err)
	}
	// a class with no approved version: the character is not started
	res, err := w.orc.Characters.Create(w.ctx, characters.CreateRequest{Name: "Ghost", Sheet: "Dev Codex", Class: "fantasma", Workspace: w.slug, Table: w.table.ID, Role: characters.RoleCharacter,
		Creator: characters.Creator{Kind: characters.CreatedByCharacter, ID: w.master.ID}})
	if err == nil {
		t.Errorf("the character of a class that was never approved was created: %+v", res)
	}
	for _, st := range w.mach.Started() {
		if st.Request.Name == "Ghost" {
			t.Errorf("the character of a class that was never approved was started: %+v", st)
		}
	}
	alive, aerr := w.orc.Registry.AliveOf(w.ctx, w.slug)
	if aerr != nil {
		t.Fatal(aerr)
	}
	for _, c := range alive {
		if c.Name == "Ghost" {
			if _, kerr := w.local.Rules(w.ctx, c.ID); kerr == nil {
				t.Error("rules were kept for a class that was never approved")
			}
		}
	}
}
