//go:build spike

package dbcapture

import (
	"database/sql"
	"testing"
)

// Plan B limits: a trigger fires again on the replica (its effects already
// arrive as changes of depth 1), and AUTOINCREMENT keeps sqlite_sequence.
func TestPlanBTriggersAndAutoincrement(t *testing.T) {
	lp := newLogPair(t)
	ctx := lp.ctx
	if err := lp.p.Migrate(ctx, 2, `CREATE TABLE seq (id INTEGER PRIMARY KEY AUTOINCREMENT, v TEXT);
CREATE TABLE audit (id INTEGER PRIMARY KEY, what TEXT);
CREATE TRIGGER accounts_audit AFTER UPDATE ON accounts BEGIN INSERT INTO audit(what) VALUES('upd ' || new.id); END;`); err != nil {
		t.Fatal(err)
	}
	lp.write(func(tx *sql.Tx) error {
		for _, q := range []string{
			`INSERT INTO accounts(id, owner, balance, updated_at) VALUES(1, 'x', 0, 0)`,
			`INSERT INTO seq(v) VALUES('a'), ('b'), ('c')`,
			`DELETE FROM seq WHERE v = 'c'`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		return nil
	})
	var seqP, seqR int
	lp.p.DB.Reader().QueryRow(`SELECT seq FROM sqlite_sequence WHERE name = 'seq'`).Scan(&seqP)
	lp.r.DB.Reader().QueryRow(`SELECT seq FROM sqlite_sequence WHERE name = 'seq'`).Scan(&seqR)
	t.Logf("AUTOINCREMENT after deleting the last row: sqlite_sequence primary %d, replica %d", seqP, seqR)

	lp.applyOn = false
	lp.write(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE accounts SET balance = 5 WHERE id = 1`)
		return err
	})
	lp.applyOn = true
	rec := lp.shipped[len(lp.shipped)-1]
	for _, c := range rec.Changes {
		t.Logf("record %d change: table %s op %d", rec.LSN, c.Table, c.Op)
	}
	err := lp.r.Apply(ctx, rec)
	var nP, nR int
	lp.p.DB.Reader().QueryRow(`SELECT count(*) FROM audit`).Scan(&nP)
	lp.r.DB.Reader().QueryRow(`SELECT count(*) FROM audit`).Scan(&nR)
	t.Logf("trigger: apply error %v; audit rows primary %d, replica %d", err, nP, nR)
}
