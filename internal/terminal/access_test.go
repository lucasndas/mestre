package terminal

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/store/machine"
)

// fakeAccess records the grants of the local channel.
type fakeAccess struct {
	granted    []string
	revoked    []string
	only       [][]string
	grantErr   error
	revokeErr  error
	grantCalls int
}

func (f *fakeAccess) Grant(sid string) error {
	f.grantCalls++
	if f.grantErr != nil {
		return f.grantErr
	}
	if !slices.Contains(f.granted, sid) {
		f.granted = append(f.granted, sid)
	}
	return nil
}

func (f *fakeAccess) Revoke(sid string) error {
	f.revoked = append(f.revoked, sid)
	f.granted = slices.DeleteFunc(f.granted, func(s string) bool { return s == sid })
	return f.revokeErr
}

func (f *fakeAccess) Only(keep []string) error {
	f.only = append(f.only, keep)
	f.granted = slices.DeleteFunc(f.granted, func(s string) bool { return !slices.Contains(keep, s) })
	return nil
}

func accessManager(t *testing.T, fa *fakeAccess) *Manager {
	t.Helper()
	ctx := context.Background()
	db, err := machine.Open(ctx, t.TempDir()+"/maquina.db", store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dir := t.TempDir()
	m, err := NewManager(ManagerConfig{
		DB: db, MachineID: "m1", SocketDir: dir + "/s", LogDir: dir + "/l", Spawner: &DirectSpawner{},
		PTYBinary: func(string) (string, error) { return "", nil }, PTYVersion: "1.0.0", Access: fa,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func row(t *testing.T, m *Manager, id string) Record {
	t.Helper()
	rec := Record{ID: id, CharacterID: "c", MachineID: "m1", Dir: "/d", Status: StatusStarting, SocketPath: "/s/" + id + ".sock",
		Secret: []byte("x"), PTYVersion: "1.0.0"}
	if err := m.reg.insert(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// CA-CODEX-WIN-02
//
// Only the text of a logon SID (S-1-5-5-x-y) is accepted: the answer of the
// launcher never becomes an ACE for another SID.
func TestOnlyALogonSIDIsAccepted(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"S-1-5-5-0-123456", "S-1-5-5-4294967295-4294967295"} {
		if err := ValidLogonSID(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "S-1-1-0", "S-1-5-18", "S-1-5-32-544", "S-1-5-21-1-2-3-1001", "S-1-5-5-1", "S-1-5-5-1-2-3", "S-1-5-5-a-b",
		"S-1-5-5-1-2 ", " S-1-5-5-1-2", "S-1-5-5-1-2\n", "s-1-5-5-1-2", "S-1-5-5--1-2", "S-1-5-5-12345678901-1",
		"S-1-5-5-1-2)(A;;GA;;;WD", "(A;;FA;;;S-1-5-5-1-2)", "Everyone",
	} {
		if err := ValidLogonSID(bad); err == nil {
			t.Errorf("%q accepted as a logon SID", bad)
		}
	}
}

// CA-CODEX-WIN-02
//
// The grant is made and recorded in the row; a SID that does not validate,
// or a grant that fails, ends the start with an error (fail closed), with
// nothing granted for a bad SID and the SID in the row for a failed grant,
// so the end of the start revokes it.
func TestTheGrantOfATerminalFailsClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fa := &fakeAccess{}
	m := accessManager(t, fa)
	const sid = "S-1-5-5-0-1001"

	rec := row(t, m, "t1")
	if err := m.grantAccess(ctx, &rec, sid); err != nil {
		t.Fatal(err)
	}
	got, err := m.reg.get(ctx, "t1")
	if err != nil || got.LogonSID != sid || !slices.Equal(fa.granted, []string{sid}) {
		t.Fatalf("row = %+v, %v; granted = %v", got, err, fa.granted)
	}

	bad := row(t, m, "t2")
	for _, s := range []string{"", "S-1-1-0", "S-1-5-18"} {
		if err := m.grantAccess(ctx, &bad, s); err == nil {
			t.Errorf("SID %q granted", s)
		}
	}
	if fa.grantCalls != 1 || bad.LogonSID != "" {
		t.Errorf("a bad SID reached the access: %d calls, rec %q", fa.grantCalls, bad.LogonSID)
	}

	fa.grantErr = errors.New("access denied")
	failing := row(t, m, "t3")
	if err := m.grantAccess(ctx, &failing, "S-1-5-5-0-1002"); err == nil || failing.LogonSID != "S-1-5-5-0-1002" {
		t.Errorf("failed grant: err %v, rec %q (the SID must be in the record to be revoked)", err, failing.LogonSID)
	}

	// Without an Access (Linux) nothing is asked.
	none := &Manager{}
	if err := none.grantAccess(ctx, &bad, "anything"); err != nil {
		t.Errorf("no Access configured: %v", err)
	}
}

// CA-CODEX-WIN-02
//
// The grant leaves with the terminal (revokeAccess), a failure to revoke
// does not stop the end, and a restarted engine puts the grant back for the
// live terminals only and sweeps every other (syncAccess).
func TestTheGrantLeavesWithTheTerminalAndTheEngineAtStartKeepsTheLiveOnes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fa := &fakeAccess{}
	m := accessManager(t, fa)
	const live, dead, none = "S-1-5-5-0-1", "S-1-5-5-0-2", "S-1-5-5-0-3"
	for id, sid := range map[string]string{"live": live, "dead": dead} {
		rec := row(t, m, id)
		if err := m.grantAccess(ctx, &rec, sid); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.reg.setStatus(ctx, "dead", StatusInterrupted, m.clk.Now()); err != nil {
		t.Fatal(err)
	}
	fa.granted = append(fa.granted, none) // left by a dead engine, in no row
	rows, err := m.reg.list(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fa.granted = nil // the socket was made anew by the new engine
	m.syncAccess(rows)
	if !slices.Equal(fa.granted, []string{live}) {
		t.Errorf("after the start: granted %v, want only %s", fa.granted, live)
	}
	if len(fa.only) != 1 || !slices.Equal(fa.only[0], []string{live}) {
		t.Errorf("Only called with %v", fa.only)
	}

	fa.revokeErr = errors.New("busy")
	m.revokeAccess("live", live) // must not panic or return anything
	m.revokeAccess("live", "")   // a terminal without a SID asks nothing
	if !slices.Equal(fa.revoked, []string{live}) {
		t.Errorf("revoked %v", fa.revoked)
	}
}

// CA-CODEX-WIN-02
//
// The engine sweeps the grants before it makes the socket and puts them back
// after it exists: SyncAccess, not Reconcile, repairs a socket made anew.
func TestTheEngineSweepsBeforeTheSocketAndPutsTheGrantsBackAfter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fa := &fakeAccess{}
	m := accessManager(t, fa)
	rec := row(t, m, "live")
	if err := m.grantAccess(ctx, &rec, "S-1-5-5-0-7"); err != nil {
		t.Fatal(err)
	}
	if err := m.SweepAccess(); err != nil || len(fa.granted) != 0 {
		t.Fatalf("after the sweep: granted %v, %v", fa.granted, err)
	}
	if err := m.SyncAccess(ctx); err != nil || !slices.Equal(fa.granted, []string{"S-1-5-5-0-7"}) {
		t.Fatalf("after the sync: granted %v, %v", fa.granted, err)
	}
	// Without an Access (Linux) both are no-ops.
	none := &Manager{}
	if none.SweepAccess() != nil || none.SyncAccess(ctx) != nil {
		t.Error("no Access configured: not a no-op")
	}
}

// Prune forgets the rows of a prefix that are not live, and only those.
func TestPruneForgetsOnlyTheRowsOfThePrefixThatAreNotLive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := accessManager(t, &fakeAccess{})
	for _, id := range []string{"verifica-stopped", "verifica-gone", "verifica-live", "char-stopped"} {
		row(t, m, id)
	}
	for id, st := range map[string]string{"verifica-stopped": StatusStopped, "verifica-gone": StatusInterrupted, "char-stopped": StatusStopped} {
		if err := m.reg.setStatus(ctx, id, st, m.clk.Now()); err != nil {
			t.Fatal(err)
		}
	}
	n, err := m.Prune(ctx, "verifica-")
	if err != nil || n != 2 {
		t.Fatalf("Prune = %d, %v; want 2", n, err)
	}
	rows, _ := m.Records(ctx)
	var left []string
	for _, r := range rows {
		left = append(left, r.ID)
	}
	slices.Sort(left)
	if !slices.Equal(left, []string{"char-stopped", "verifica-live"}) {
		t.Errorf("rows left: %v", left)
	}
}
