package guild_test

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/store"
	guilddb "github.com/mestre-ia/mestre/internal/store/guild"
	"github.com/mestre-ia/mestre/pkg/clock"
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
	// Only the Citadel of the epoch 1 reports what it lost, and from where
	// the epoch 2 started: another machine, or another lsn, is refused.
	handle := guild.DiscardedHandler(func() *store.DB { return gdb }, clk)
	for _, bad := range []struct {
		peer protocol.Peer
		p    *protocol.GuildDiscarded
	}{
		{protocol.Peer{Role: protocol.RoleMachine, ID: "b"}, p},
		{protocol.Peer{Role: protocol.RoleGuardian, ID: f.MachineID}, p},
		{protocol.Peer{Role: protocol.RoleMachine, ID: f.MachineID}, &protocol.GuildDiscarded{Epoch: 1, FromLSN: from + 1, Total: total, Transactions: txs}},
		{protocol.Peer{Role: protocol.RoleMachine, ID: "b"}, &protocol.GuildDiscarded{Epoch: 2, FromLSN: from, Total: total, Transactions: txs}},
	} {
		if _, err := handle(ctx, &protocol.Message{Payload: bad.p, Peer: bad.peer}); err == nil {
			t.Errorf("guild.discarded of epoch %d from %v (from %d) was taken", bad.p.Epoch, bad.peer, bad.p.FromLSN)
		}
	}
	if got, err := guild.Discarded(ctx, gdb); err != nil || len(got) != 0 {
		t.Fatalf("a refused report entered the audit: %+v, %v", got, err)
	}
	for range 2 {
		if _, err := handle(ctx, &protocol.Message{Payload: p, Peer: protocol.Peer{Role: protocol.RoleMachine, ID: f.MachineID}}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := guild.Discarded(ctx, gdb)
	if err != nil || len(got) != 1 || got[0].LSN != txs[0].LSN || got[0].Epoch != 1 || got[0].MachineID != f.MachineID {
		t.Fatalf("the audit lists %+v, %v; want the one transaction, once", got, err)
	}

	// the external actions the fence of the old Citadel dropped enter the
	// audit too, once
	p.Dropped = []string{"github.comment", "telegram.send"}
	for range 2 {
		if _, err := handle(ctx, &protocol.Message{Payload: p, Peer: protocol.Peer{Role: protocol.RoleMachine, ID: f.MachineID}}); err != nil {
			t.Fatal(err)
		}
	}
	var dropped []string
	rows, err := gdb.Reader().QueryContext(ctx, `SELECT detail FROM audit_log WHERE action = ?`, guild.DroppedAction)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		dropped = append(dropped, d)
	}
	if len(dropped) != 1 || !strings.Contains(dropped[0], "github.comment") || !strings.Contains(dropped[0], "telegram.send") {
		t.Errorf("the audit of the dropped actions: %v", dropped)
	}

	// a refused certificate of epoch enters the audit once per epoch,
	// Citadel and reason
	bad := protocol.Certificate{GuildID: f.GuildID, Epoch: 9, CitadelMachineID: "x"}
	for range 3 {
		if err := guild.RecordRefused(ctx, gdb, clk, f.MachineID, bad, guild.ErrWrongGuild); err != nil {
			t.Fatal(err)
		}
	}
	var refused int
	if err := gdb.Reader().QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE action = ?`, guild.RefusedAction).Scan(&refused); err != nil || refused != 1 {
		t.Errorf("the audit has %d refused certificates (%v), want 1", refused, err)
	}
}
