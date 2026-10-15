package tables_test

import (
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

// character puts a character of the table, alive, on the Tower: the one that
// commits there and gives `done`.
func (tw *tower) character(tbl tables.Table) characters.Character {
	tw.w.t.Helper()
	c := characters.Character{ID: "tc-" + tw.id + "-" + tbl.ID, Name: "Dev " + tw.id, Kind: characters.KindCharacter, Role: characters.RoleCharacter, Workspace: tbl.Workspace,
		Table: tbl.ID, Machine: tw.id, Sheet: "Dev", Base: "claude-code", State: characters.Idle, TerminalID: "tt-" + tw.id + "-" + tbl.ID, Lifecycle: characters.Persistent,
		CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, CreatedAt: tw.w.clk.Now(), StartedAt: tw.w.clk.Now(), LastSignalAt: tw.w.clk.Now()}
	if err := tw.w.reg.Insert(tw.w.ctx, c); err != nil {
		tw.w.t.Fatal(err)
	}
	return c
}
