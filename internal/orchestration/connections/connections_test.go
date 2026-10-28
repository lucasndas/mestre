package connections_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/connections"
)

// The automatic connections of the document: table master and Grand Master
// (mestre), a character and its table master (mesa), a character and who
// recruited it (criador), with several reasons on one connection.
func TestTheEngineMakesTheAutomaticConnectionsWithTheirReasons(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.gm()
	tm := w.master("3-login", gm)
	dev := w.dev("3-login", "Dev", by(tm))
	qa := w.dev("3-login", "QA", characters.Creator{Kind: characters.CreatedByEngine})
	late := w.dev("3-login", "Tarde", characters.Creator{Kind: characters.CreatedByUser, ID: "bia", Level: characters.LevelAdministrator})
	for _, tt := range []struct {
		a, b characters.Character
		want string
	}{
		{tm, gm, "mestre"},
		{dev, tm, "criador,mesa"},
		{qa, tm, "mesa"},
		{late, tm, "mesa"},
		{dev, qa, ""},
		{dev, gm, ""},
	} {
		if got := w.reasons(ch(tt.a), ch(tt.b)); got != tt.want {
			t.Errorf("%s and %s: %q, want %q", tt.a.Name, tt.b.Name, got, tt.want)
		}
	}
	other := w.master("4-api", gm)
	odev := w.dev("4-api", "Dev", by(other))
	if got := w.reasons(ch(odev), ch(tm)); got != "" {
		t.Errorf("a character of another table and this table master: %q, want none", got)
	}
}

// "O motor cria (e recria, se faltarem)": Born twice does not duplicate, a
// connection that went missing comes back, and a new table master gets the
// whole party of its table.
func TestTheAutomaticConnectionsAreMadeAgainWhenTheyAreMissing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.gm()
	tm := w.master("3-login", gm)
	dev := w.dev("3-login", "Dev", by(tm))
	if err := w.svc.Born(w.ctx, dev); err != nil {
		t.Fatal(err)
	}
	if got := w.reasons(ch(dev), ch(tm)); got != "criador,mesa" {
		t.Fatalf("after Born twice: %q", got)
	}
	if _, err := w.svc.Store().RemoveNode(w.ctx, ch(tm)); err != nil {
		t.Fatal(err)
	}
	if got := w.reasons(ch(dev), ch(tm)) + w.reasons(ch(tm), ch(gm)); got != "" {
		t.Fatalf("after removing them: %q", got)
	}
	if err := w.svc.Reconcile(w.ctx, workspace); err != nil {
		t.Fatal(err)
	}
	if got := w.reasons(ch(dev), ch(tm)); got != "criador,mesa" {
		t.Errorf("Dev and the table master after Reconcile: %q", got)
	}
	if got := w.reasons(ch(tm), ch(gm)); got != "mestre" {
		t.Errorf("table master and Grand Master after Reconcile: %q", got)
	}

	// The table master is replaced: the new one is connected to the party.
	if _, _, err := w.reg.Transition(w.ctx, tm.ID, characters.Stopped, characters.Change{At: w.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Ended(w.ctx, tm); err != nil {
		t.Fatal(err)
	}
	tm2 := w.master("3-login", gm)
	if got := w.reasons(ch(dev), ch(tm2)); got != "mesa" {
		t.Errorf("Dev and the new table master: %q, want mesa", got)
	}
}

// A character that ends loses every connection; the scrolls it was connected
// to stay, and so do the connections between them.
func TestWhenACharacterEndsAllItsConnectionsGoAndTheScrollsStay(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.gm()
	tm := w.master("3-login", gm)
	dev := w.dev("3-login", "Dev", by(tm))
	p1, p2 := w.scrolls.scroll("p0001", "Plano", "3-login"), w.scrolls.scroll("p0002", "Notas", "3-login")
	for _, c := range []struct {
		a, b connections.Ref
	}{{ch(dev), p1}, {p1, p2}} {
		if _, err := w.svc.Store().Add(w.ctx, connections.AddInput{Workspace: workspace, Table: "3-login", A: c.a, B: c.b, Reason: connections.ReasonManual}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.svc.Ended(w.ctx, dev); err != nil {
		t.Fatal(err)
	}
	if cs, err := w.svc.Store().Of(w.ctx, ch(dev)); err != nil || len(cs) != 0 {
		t.Errorf("connections of the ended character: %+v, %v", cs, err)
	}
	if got := w.reasons(p1, p2); got != "manual" {
		t.Errorf("the scrolls: %q, want their connection kept", got)
	}
}

// Part of CA-CONX-02 (stage 9 closes it with the quest): the connections of a
// quest carry its reason and go away when it ends if nothing else keeps them.
func TestTheConnectionsOfAQuestGoAwayWhenItEndsUnlessAnotherReasonKeepsThem(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.gm()
	tm := w.master("3-login", gm)
	dev := w.dev("3-login", "Dev", characters.Creator{Kind: characters.CreatedByEngine})
	rev := w.dev("3-login", "Revisor", characters.Creator{Kind: characters.CreatedByEngine})
	scroll := w.scrolls.scroll("p0001", "Missão: login", "3-login")

	// Dev -> Revisor, with a return to Dev; every step with the scroll.
	if err := w.svc.LinkQuest(w.ctx, "q1", workspace, "3-login",
		[2]connections.Ref{ch(dev), ch(rev)}, [2]connections.Ref{ch(dev), scroll}, [2]connections.Ref{ch(rev), scroll}); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.LinkQuest(w.ctx, "q2", workspace, "3-login", [2]connections.Ref{ch(dev), ch(rev)}); err != nil {
		t.Fatal(err)
	}
	if got := w.reasons(ch(dev), ch(rev)); got != "missao:q1,missao:q2" {
		t.Fatalf("Dev and Revisor: %q", got)
	}
	// The table master connects Dev to the scroll by hand too.
	if _, err := w.svc.Connect(w.ctx, connections.ConnectRequest{Caller: tm, A: "Dev", B: "Missão: login"}); err != nil {
		t.Fatal(err)
	}

	if err := w.svc.EndQuest(w.ctx, "q1"); err != nil {
		t.Fatal(err)
	}
	if got := w.reasons(ch(dev), ch(rev)); got != "missao:q2" {
		t.Errorf("Dev and Revisor after q1 ended: %q, want the reason of q2 to keep it", got)
	}
	if got := w.reasons(ch(dev), scroll); got != "manual" {
		t.Errorf("Dev and the scroll after q1 ended: %q, want only manual", got)
	}
	if got := w.reasons(ch(rev), scroll); got != "" {
		t.Errorf("Revisor and the scroll after q1 ended: %q, want none", got)
	}
	if err := w.svc.EndQuest(w.ctx, "q2"); err != nil {
		t.Fatal(err)
	}
	if got := w.reasons(ch(dev), ch(rev)); got != "" {
		t.Errorf("Dev and Revisor after both ended: %q, want none", got)
	}
}

// Part of CA-CONX-03 (`note read` closes it in internal/app, over the CLI): scrolls P1 and P2 are connected, P1 is connected to Dev: Dev
// reads P2, and a scroll with no path to Dev is refused. A tome stands for all
// its scrolls, the ones that join it later too.
func TestAnAgentSeesTheScrollsConnectedToItAndTheOnesReachedThroughScrolls(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.gm()
	tm := w.master("3-login", gm)
	dev := w.dev("3-login", "Dev", by(tm))
	p1, p2 := w.scrolls.scroll("p1", "P1", "3-login"), w.scrolls.scroll("p2", "P2", "3-login")
	p3, p4 := w.scrolls.scroll("p3", "P3", "3-login"), w.scrolls.scroll("p4", "P4", "3-login")
	connect := func(a, b connections.Ref) {
		t.Helper()
		if _, err := w.svc.Store().Add(w.ctx, connections.AddInput{Workspace: workspace, Table: "3-login", A: a, B: b, Reason: connections.ReasonManual}); err != nil {
			t.Fatal(err)
		}
	}
	connect(p1, p2)
	connect(ch(dev), p1)
	connect(p3, p4) // a chain that does not reach Dev

	can := func(agent characters.Character, scroll string) bool {
		t.Helper()
		ok, err := w.svc.CanSeeScroll(w.ctx, agent.ID, scroll)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	for scroll, want := range map[string]bool{"p1": true, "p2": true, "p3": false, "p4": false} {
		if got := can(dev, scroll); got != want {
			t.Errorf("Dev reads %s: %v, want %v", scroll, got, want)
		}
	}
	if can(tm, "p2") {
		t.Error("the table master reads a scroll nobody connected to it")
	}

	// A tome is connected to P4's neighbour; members that join later count.
	w.scrolls.tome("t1", "Tomo", "3-login", "p3")
	connect(ch(dev), connections.Tome("t1"))
	if !can(dev, "p3") || !can(dev, "p4") {
		t.Error("the tome did not give Dev its scrolls and, through them, the scroll connected to one")
	}
	w.scrolls.scroll("p5", "P5", "3-login")
	if can(dev, "p5") {
		t.Error("a scroll outside the tome is visible")
	}
	w.scrolls.join("t1", "p5")
	if !can(dev, "p5") {
		t.Error("a scroll that joined the tome later is not visible")
	}

	v, err := w.svc.View(w.ctx, dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range v.Scrolls {
		got = append(got, s.Name)
	}
	if !slices.Equal(got, []string{"P1", "P2", "P3", "P4", "P5"}) {
		t.Errorf("scrolls in the view: %v", got)
	}
	if v.Scrolls[0].Name != "P1" || !slices.Equal(v.Scrolls[0].Connections, []string{"Dev", "P2"}) {
		t.Errorf("P1 in the view: %+v", v.Scrolls[0])
	}
}

// The visibility rules: connected characters; the Grand Master sees every
// table master of the workspace; a table master sees the Grand Master and the
// whole party; nothing crosses tables.
func TestWhatEachAgentSeesFollowsTheConnectionsAndTheMastersRules(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.gm()
	tm := w.master("3-login", gm)
	tm2 := w.master("4-api", gm)
	dev := w.dev("3-login", "Dev", by(tm))
	qa := w.dev("3-login", "QA", by(tm))
	odev := w.dev("4-api", "Outro", by(tm2))

	see := func(c characters.Character) []string {
		t.Helper()
		cs, err := w.svc.Visible(w.ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		return names(cs)
	}
	if got := see(dev); !slices.Equal(got, []string{"Mestre"}) {
		t.Errorf("Dev sees %v", got)
	}
	if got := see(tm); !slices.Equal(got, []string{"Dev", "Grão-Mestre", "QA"}) {
		t.Errorf("the table master sees %v", got)
	}
	if got := see(gm); !slices.Equal(got, []string{"Mestre", "Mestre"}) {
		t.Errorf("the Grand Master sees %v", got)
	}
	// A manual connection between Dev and QA lets each see the other.
	if _, err := w.svc.Connect(w.ctx, connections.ConnectRequest{Caller: tm, A: "Dev", B: "QA"}); err != nil {
		t.Fatal(err)
	}
	if got := see(dev); !slices.Equal(got, []string{"Mestre", "QA"}) {
		t.Errorf("Dev after the connection sees %v", got)
	}
	if got := see(qa); !slices.Equal(got, []string{"Dev", "Mestre"}) {
		t.Errorf("QA after the connection sees %v", got)
	}
	if got := see(odev); !slices.Equal(got, []string{"Mestre"}) {
		t.Errorf("the character of the other table sees %v", got)
	}
	// An ended character is no longer seen.
	if err := w.reg.SetStop(w.ctx, qa.ID, w.clk.Now(), "engine:", "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.reg.Transition(w.ctx, qa.ID, characters.Stopped, characters.Change{At: w.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	if got := see(dev); !slices.Equal(got, []string{"Mestre"}) {
		t.Errorf("Dev after QA stopped sees %v", got)
	}
}

// CA-CONX-04: a table master cannot connect an item of another table; the
// rest of the rules of connect.
func TestConnectRefusesWhatTheDocumentRefuses(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.gm()
	tm := w.master("3-login", gm)
	tm2 := w.master("4-api", gm)
	w.dev("3-login", "Dev", by(tm))
	w.dev("4-api", "Gemeo", by(tm2))
	w.dev("4-api", "Dev", by(tm2)) // the same name in another table is another character
	w.scrolls.scroll("p1", "Plano", "3-login")

	connect := func(caller characters.Character, table, a, b string) (connections.ConnectResult, error) {
		return w.svc.Connect(w.ctx, connections.ConnectRequest{Caller: caller, Table: table, A: a, B: b})
	}
	if _, err := connect(tm, "", "Dev", "Gemeo"); !errors.Is(err, connections.ErrDifferentTables) {
		t.Errorf("a character of another table: %v, want different tables", err)
	}
	if _, err := connect(tm, "4-api", "Dev", "Plano"); !errors.Is(err, connections.ErrLevelDenied) {
		t.Errorf("a table master in another table: %v, want level denied", err)
	}
	if _, err := connect(tm, "", "Dev", "Grão-Mestre"); !errors.Is(err, connections.ErrGrandMaster) {
		t.Errorf("the Grand Master: %v, want refused", err)
	}
	if _, err := connect(gm, "3-login", "Dev", "grão-mestre"); !errors.Is(err, connections.ErrGrandMaster) {
		t.Errorf("the Grand Master asking to connect itself: %v, want refused", err)
	}
	if _, err := connect(tm, "", "Dev", "Dev"); !errors.Is(err, connections.ErrSame) {
		t.Errorf("itself: %v, want same item", err)
	}
	if _, err := connect(tm, "", "Dev", "Fantasma"); !errors.Is(err, connections.ErrNotFound) {
		t.Errorf("a missing name: %v, want not found", err)
	}
	if _, err := connect(gm, "", "Dev", "Plano"); !errors.Is(err, connections.ErrTableRequired) {
		t.Errorf("the Grand Master without a table: %v, want table required", err)
	}
	dev := w.dev("3-login", "Dev2", by(tm))
	if _, err := connect(dev, "", "Dev", "Dev2"); !errors.Is(err, connections.ErrLevelDenied) {
		t.Errorf("a common character: %v, want level denied", err)
	}
	if cs, _ := w.svc.Store().OfTable(w.ctx, workspace, "3-login"); len(cs) != 2 {
		t.Errorf("nothing was refused into existence, yet %d connections: %+v", len(cs), cs) // Dev2 and the table master; Dev and the table master
	}
}

// "connect repetido não duplica: só acrescenta o motivo manual", and the
// Grand Master connects in any table of the workspace.
func TestConnectIsRepeatedWithoutDuplicatingAndTheGrandMasterConnectsInAnyTable(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.gm()
	tm := w.master("3-login", gm)
	w.dev("3-login", "Dev", by(tm))
	w.dev("3-login", "QA", by(tm))
	w.scrolls.scroll("p1", "Plano", "3-login")

	first, err := w.svc.Connect(w.ctx, connections.ConnectRequest{Caller: gm, Table: "3-login", A: "Dev", B: "QA"})
	if err != nil || first.AlreadyConnected {
		t.Fatalf("first connect: %+v, %v", first, err)
	}
	again, err := w.svc.Connect(w.ctx, connections.ConnectRequest{Caller: tm, A: "qa", B: "dev"})
	if err != nil || !again.AlreadyConnected {
		t.Fatalf("second connect: %+v, %v; want already connected", again, err)
	}
	if got := again.Connection.ReasonNames(); !slices.Equal(got, []string{"manual"}) {
		t.Errorf("reasons: %v", got)
	}
	// A scroll and a character: the connection a person made for a pair that
	// the engine connected keeps both reasons.
	if _, err := w.svc.Connect(w.ctx, connections.ConnectRequest{Caller: tm, A: "Dev", B: "Mestre"}); err != nil {
		t.Fatal(err)
	}
	devRef := connections.Char(w.mustByName("3-login", "Dev").ID)
	if got := w.reasons(devRef, ch(tm)); got != "criador,manual,mesa" {
		t.Errorf("Dev and the table master: %q", got)
	}
	if _, err := w.svc.Connect(w.ctx, connections.ConnectRequest{Caller: tm, A: "Plano", B: "Dev"}); err != nil {
		t.Fatal(err)
	}
	if got := w.reasons(devRef, connections.Scroll("p1")); got != "manual" {
		t.Errorf("Dev and the scroll: %q", got)
	}
}

func (w *world) mustByName(table, name string) characters.Character {
	w.t.Helper()
	c, err := w.reg.ByName(w.ctx, workspace, table, name)
	if err != nil {
		w.t.Fatal(err)
	}
	return c
}

// Part of CA-CONX-05 (the drag and the reason shown are the canvas, milestone
// 6): the engine refuses to remove an automatic connection and says why; only
// the Owner and the Administrators remove a manual one, and a connection that
// something else keeps stays.
func TestAUserCannotRemoveAnAutomaticConnectionAndSeesWhy(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.gm()
	tm := w.master("3-login", gm)
	dev := w.dev("3-login", "Dev", by(tm))
	admin := connections.User{ID: "bia", Level: characters.LevelAdministrator}

	_, err := w.svc.Disconnect(w.ctx, admin, ch(tm), ch(gm))
	var auto *connections.AutomaticError
	if !errors.As(err, &auto) || !slices.Equal(auto.Reasons, []string{"mestre"}) {
		t.Fatalf("remove Mestre-Grão-Mestre: %v, want the automatic refusal with the reason mestre", err)
	}
	if got := w.reasons(ch(tm), ch(gm)); got != "mestre" {
		t.Errorf("the connection after the refusal: %q", got)
	}
	resp := connections.Refusal(err)
	if resp.Error == nil || resp.Error.Code != "automatic_connection" || resp.ExitCode() != 3 {
		t.Errorf("answer: %+v", resp)
	}

	if _, err := w.svc.Disconnect(w.ctx, connections.User{ID: "joao", Level: characters.LevelApprover}, ch(dev), ch(tm)); !errors.Is(err, connections.ErrLevelDenied) {
		t.Errorf("an Approver: %v, want level denied", err)
	}
	// Manual on top of automatic: removing it leaves the automatic ones.
	if _, err := w.svc.Connect(w.ctx, connections.ConnectRequest{Caller: tm, A: "Dev", B: "Mestre"}); err != nil {
		t.Fatal(err)
	}
	res, err := w.svc.Disconnect(w.ctx, admin, ch(dev), ch(tm))
	if err != nil || res.Removed || !slices.Equal(res.Remaining, []string{"criador", "mesa"}) {
		t.Fatalf("remove the manual reason: %+v, %v", res, err)
	}
	// A purely manual connection goes away.
	qa := w.dev("3-login", "QA", characters.Creator{Kind: characters.CreatedByEngine})
	if _, err := w.svc.Connect(w.ctx, connections.ConnectRequest{Caller: tm, A: "Dev", B: "QA"}); err != nil {
		t.Fatal(err)
	}
	owner := connections.User{ID: "lucas", Level: characters.LevelOwner}
	if res, err := w.svc.Disconnect(w.ctx, owner, ch(dev), ch(qa)); err != nil || !res.Removed {
		t.Fatalf("remove a manual connection: %+v, %v", res, err)
	}
	if _, err := w.svc.Disconnect(w.ctx, owner, ch(dev), ch(qa)); !errors.Is(err, connections.ErrNoConnection) {
		t.Errorf("remove it again: %v, want no connection", err)
	}
}

// Inherit gives the connections of a replaced character, reasons included, to
// the one that takes its place.
func TestInheritMovesTheConnectionsWithTheirReasons(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.gm()
	tm := w.master("3-login", gm)
	old := w.dev("3-login", "Dev", by(tm))
	qa := w.dev("3-login", "QA", by(tm))
	if _, err := w.svc.Connect(w.ctx, connections.ConnectRequest{Caller: tm, A: "Dev", B: "QA"}); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.LinkQuest(w.ctx, "q1", workspace, "3-login", [2]connections.Ref{ch(old), ch(qa)}); err != nil {
		t.Fatal(err)
	}
	if err := w.reg.SetStop(w.ctx, old.ID, w.clk.Now(), "engine:", "replaced"); err != nil {
		t.Fatal(err)
	}
	fresh := w.add(characters.RoleCharacter, "3-login", "Dev 2", by(tm))
	if err := w.svc.Inherit(w.ctx, old, fresh); err != nil {
		t.Fatal(err)
	}
	if got := w.reasons(ch(fresh), ch(qa)); got != "manual,missao:q1" {
		t.Errorf("the new one and QA: %q", got)
	}
	if got := w.reasons(ch(fresh), ch(tm)); got != "criador,mesa" {
		t.Errorf("the new one and the table master: %q", got)
	}
	if cs, _ := w.svc.Store().Of(w.ctx, ch(old)); len(cs) != 0 {
		t.Errorf("the old one kept %+v", cs)
	}
}

// CA-CANVAS-10 (security): a table cannot undo the connection of a scroll that belongs to the
// table of the same name in another campaign.
func TestDisconnectInTableRefusesAScrollOfAnotherCampaignWithTheSameTable(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.gm()
	w.master("42-login", gm)
	dev := w.dev("42-login", "Dev", by(gm))
	mine := w.scrolls.scroll(workspace+"/42-login/notas", "notas", "42-login")
	foreign := w.scrolls.scroll("outra/42-login/notas", "notas", "42-login")
	admin := connections.User{ID: "u1", Level: characters.LevelAdministrator}
	if _, err := w.svc.ConnectByUser(w.ctx, admin, workspace, "42-login", connections.Char(dev.ID), mine); err != nil {
		t.Fatal(err)
	}
	// the engine made this one (a user cannot): it is the connection to protect
	if err := w.svc.Link(w.ctx, workspace, "42-login", connections.Char(dev.ID), foreign, "u2"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.DisconnectInTable(w.ctx, admin, workspace, "42-login", connections.Char(dev.ID), foreign); !errors.Is(err, connections.ErrNotFound) {
		t.Fatalf("disconnecting the scroll of another campaign = %v; want not found", err)
	}
	if w.reasons(connections.Char(dev.ID), foreign) == "" {
		t.Error("the connection of the other campaign is gone")
	}
	if _, err := w.svc.DisconnectInTable(w.ctx, admin, workspace, "42-login", connections.Char(dev.ID), mine); err != nil {
		t.Fatalf("disconnecting its own scroll: %v", err)
	}
}

// CA-CANVAS-10 (security): a user cannot connect a scroll of another campaign whose table has the
// same id; nothing is connected.
func TestConnectByUserRefusesAScrollOfAnotherCampaignWithTheSameTable(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.gm()
	w.master("42-login", gm)
	dev := w.dev("42-login", "Dev", by(gm))
	foreign := w.scrolls.scroll("outra/42-login/notas", "notas", "42-login")
	admin := connections.User{ID: "u1", Level: characters.LevelAdministrator}
	if _, err := w.svc.ConnectByUser(w.ctx, admin, workspace, "42-login", connections.Char(dev.ID), foreign); !errors.Is(err, connections.ErrDifferentTables) {
		t.Fatalf("connecting the scroll of another campaign = %v; want different tables", err)
	}
	if w.reasons(connections.Char(dev.ID), foreign) != "" {
		t.Error("a connection was made")
	}
}
