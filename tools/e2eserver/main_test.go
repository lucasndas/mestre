package main

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/store"
	guilddb "github.com/mestre-ia/mestre/internal/store/guild"
	"github.com/mestre-ia/mestre/pkg/clock"
)

func TestTheReadyLineRoundTripsAndNothingElseParses(t *testing.T) {
	t.Parallel()
	url, code, ok := parseReady(readyLine("https://127.0.0.1:7443", "ABCD2345EFGH") + "\n")
	if !ok || url != "https://127.0.0.1:7443" || code != "ABCD2345EFGH" {
		t.Fatalf("parseReady = %q %q %v", url, code, ok)
	}
	for _, line := range []string{"", "E2E_READY", "E2E_READY url= setup_code=x", "E2E_READY url=https://x setup_code=", `{"level":"INFO"}`} {
		if _, _, ok := parseReady(line); ok {
			t.Errorf("%q parses as a ready line", line)
		}
	}
}

func TestABindThatIsNotAnIPIsRefusedBeforeAnythingStarts(t *testing.T) {
	t.Parallel()
	err := runWith(context.Background(), io.Discard, io.Discard, nil, runOptions{Dir: t.TempDir(), Bind: "mestre.local"})
	if err == nil || !strings.Contains(err.Error(), `-bind "mestre.local" is not an IP`) {
		t.Fatalf("runWith = %v", err)
	}
}

func TestAFreePortIsTakenOnTheIPOfTheBind(t *testing.T) {
	t.Parallel()
	port, err := freePort("127.0.0.1")
	if err != nil || port < portLow || port >= portLow+portSpan {
		t.Fatalf("freePort = %d, %v", port, err)
	}
	if _, err := freePort("192.0.2.1"); err == nil {
		t.Fatal("a port on an IP of no interface of the machine was handed out")
	}
}

// The rich seed leaves the administration with the states a fresh Guild never has: the Apps ready,
// waiting and with the link expired, the proposals of class (one with a problem), the second
// machine active and a request to join.
func TestTheRichSeedLeavesTheAdministrationWithItsStates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, err := guilddb.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Clock: clock.Real{}, Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := clock.Real{}.Now()
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, joined_at)
		VALUES ('e2e-torre', 'notebook-ana', 'torre', 1, 0, 'offline', '{}', ?)`, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := seedAdmin(ctx, db, now); err != nil {
		t.Fatal(err)
	}
	apps, err := (&github.Registry{DB: db, Clock: clock.Real{}}).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]classes.AppState{}
	for _, a := range apps {
		states[a.Name] = a.State
	}
	if len(states) != 4 || states["lucas-mestre-qa"] != classes.AppAwaitingCreation || states["lucas-mestre-revisor"] != classes.AppExpired || states["lucas-mestre-mestre"] != classes.AppReady {
		t.Errorf("the Apps = %v", states)
	}
	pending, err := (classes.ClassDB{DB: db, Clock: clock.Real{}}).Pending(ctx, "loja")
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending proposals = %v, %v", pending, err)
	}
	var machine string
	if err := db.Reader().QueryRowContext(ctx, `SELECT state FROM machines WHERE machine_id = 'e2e-torre'`).Scan(&machine); err != nil || machine != "active" {
		t.Errorf("the second machine is %q, %v", machine, err)
	}
	var joins int
	if err := db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM join_requests WHERE state = 'pending'`).Scan(&joins); err != nil || joins != 1 {
		t.Errorf("requests to join = %d, %v", joins, err)
	}
}
