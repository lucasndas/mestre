package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/store"
	guilddb "github.com/mestre-ia/mestre/internal/store/guild"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// seedRequest puts a request of extra cost in the Guild database, waiting for the
// people the way the chain leaves it (the request, and the question that asks the PIN),
// before the engine starts: the smoke test finds it in the central of approvals and
// answers it. The Guild has no workspace and no agent; the request is the engine's own.
func seedRequest(ctx context.Context, guildDB string) error {
	clk := clock.Real{}
	db, err := guilddb.Open(ctx, guildDB, store.Options{Clock: clk, Synchronous: "OFF"})
	if err != nil {
		return err
	}
	defer db.Close()
	now := clk.Now()
	r := chain.Request{
		Workspace: "loja", Type: classes.RequestExtraCost, State: chain.Open, Level: chain.Level{Kind: chain.LevelPeople},
		About: "mais US$ 20 de uso este mês", Risk: classes.RiskResult{Risk: classes.RiskMedium, Rule: "custo extra", TableVersion: 1},
		CreatedAt: now, Deadline: now.Add(24 * time.Hour),
	}
	r, err = (&chain.Store{DB: db}).Insert(ctx, r)
	if err != nil {
		return err
	}
	text := chain.Text(r, r.Level, chain.AnswersFor(chain.LevelPeople, r))
	return db.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO approval_flow (request, approvals, require, min_level, pin, text, asked_at) VALUES (?, 1, '[]', ?, 1, ?, ?)`,
			r.ID, string(classes.LevelApprover), text, now.UnixMilli())
		if err != nil {
			return fmt.Errorf("the question of the request: %w", err)
		}
		return nil
	})
}
