package guild_test

import (
	"context"
	"errors"
	"net"
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/protocol"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The promoted machine signs the next certificate with the CA of the Guild
// and records the last lsn it had applied; the transactions of the old
// Citadel after that lsn are the ones the change lost, named by table and key
// in the audit, once (docs/guilda/replicacao.md#perda-na-troca).
func TestTheNewEpochRecordsWhereItStartsAndTheLostTransactionsAreAuditedOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clk := clock.Real{}
	gdb := openDB(t, guildTemplate, "mestre.db", clk, guilddb.Open)
	founder := bareNode(t, clk, "a")
	f, err := guild.Create(ctx, guild.CreateConfig{
		GuildDB: gdb, Member: founder.member, Secrets: &memSecrets{}, Clock: clk,
		Name: "a", Hostname: "a", IPs: []net.IP{net.ParseIP("192.168.1.10")}, RecoveryPublicKey: []byte("recovery-public-key"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if r, err := guild.EpochStart(ctx, gdb, 1); err != nil || r.CitadelMachineID != f.MachineID || r.FromLSN != 0 {
		t.Fatalf("the epoch 1: %+v, %v", r, err)
	}
	from, err := gdb.LastLSN(ctx)
	if err != nil {
		t.Fatal(err)
	}

	cert, err := guild.TakeCommand(ctx, gdb, f.CA, clk, guild.Command{Epoch: 2, MachineID: "b", ApprovalID: "p1", ApprovedBy: "dono", FromLSN: from})
	if err != nil {
		t.Fatal(err)
	}
	if err := guild.NewVerifier(f.CA.Certificate(), f.GuildID).Verify(cert); err != nil || cert.Epoch != 2 || cert.CitadelMachineID != "b" || cert.ApprovalID != "p1" {
		t.Fatalf("the certificate of epoch 2: %+v, %v", cert, err)
	}
	if r, err := guild.EpochStart(ctx, gdb, 2); err != nil || r.FromLSN != from || r.ApprovedBy != "dono" {
		t.Fatalf("the epoch 2: %+v, %v", r, err)
	}
	if _, err := guild.TakeCommand(ctx, gdb, f.CA, clk, guild.Command{Epoch: 2, MachineID: "c", FromLSN: from}); !errors.Is(err, guild.ErrStaleCertificate) {
		t.Fatalf("a second epoch 2 = %v, want ErrStaleCertificate", err)
	}

	// The bookkeeping of the protocol is no data of the Guild: left out.
	if _, err := gdb.Writer().ExecContext(ctx,
		`INSERT INTO processed_commands (sender, id, type, processed_at) VALUES ('m', 'x', 'machine.sheet', 0)`); err != nil {
		t.Fatal(err)
	}

	// The taking of the command is the one transaction after from: the
	// guild row, the epoch and the audit, by key.
	total, txs, err := guild.ListDiscarded(ctx, gdb, from, 0)
	if err != nil || total != 1 || len(txs) != 1 {
		t.Fatalf("ListDiscarded = %d, %+v, %v", total, txs, err)
	}
	var tables []string
	for _, c := range txs[0].Changes {
		tables = append(tables, c.Table+"/"+c.Op)
		if len(c.Key) == 0 {
			t.Errorf("the change of %s has no key", c.Table)
		}
		if c.Table == "epochs" && c.Key[0] != int64(2) {
			t.Errorf("the key of the epoch is %v", c.Key)
		}
	}
	slices.Sort(tables)
	if want := []string{"audit_log/insert", "epochs/insert", "guild/update"}; !slices.Equal(tables, want) {
		t.Fatalf("the discarded transaction changed %v, want %v", tables, want)
	}

	p := &protocol.GuildDiscarded{Epoch: 1, FromLSN: from, Total: total, Transactions: txs}
	for range 2 {
		if err := guild.RecordDiscarded(ctx, gdb, clk, "a", p); err != nil {
			t.Fatal(err)
		}
	}
	got, err := guild.Discarded(ctx, gdb)
	if err != nil || len(got) != 1 || got[0].LSN != txs[0].LSN || got[0].Epoch != 1 || got[0].MachineID != "a" {
		t.Fatalf("the audit lists %+v, %v; want the one transaction, once", got, err)
	}
}
