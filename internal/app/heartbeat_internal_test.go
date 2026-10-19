package app

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
)

type diskProbe struct{ free uint64 }

func (d diskProbe) Info(context.Context) (machinesheet.SystemInfo, error) {
	return machinesheet.SystemInfo{}, nil
}

func (d diskProbe) GUI(context.Context) (bool, error)     { return false, nil }
func (d diskProbe) IPs(context.Context) ([]string, error) { return nil, nil }

func (d diskProbe) Resources(context.Context) (machinesheet.Resources, error) {
	return machinesheet.Resources{DiskFreeBytes: d.free}, nil
}

type fixedPresence bool

func (f fixedPresence) Connected(string) bool { return bool(f) }

// The heartbeat of a Tower carries the free disk the probes of the sheet read.
func TestTheHeartbeatOfATowerCarriesTheFreeDiskAndTheReplicaLag(t *testing.T) {
	t.Parallel()
	lag := 1.5
	hb := towerHeartbeat(t.Context(), diskProbe{free: 42 << 30}, &lag)
	if hb.DiskFreeBytes != 42<<30 || hb.ReplicaLagS == nil || *hb.ReplicaLagS != 1.5 {
		t.Errorf("heartbeat = %+v", hb)
	}
}

// A Tower that missed its heartbeat is not online even with its connection
// open; the next heartbeat brings it back (docs/guilda/ficha-da-maquina.md).
func TestATowerThatMissedItsHeartbeatIsNotOnlineUntilTheNextOne(t *testing.T) {
	t.Parallel()
	b := newHeartbeats()
	p := heartbeatPresence{fixedPresence(true), b}
	if !p.Connected("torre") {
		t.Fatal("a connected Tower with no news is offline")
	}
	b.Lost(protocol.Peer{ID: "torre"})
	if p.Connected("torre") {
		t.Fatal("the Tower is online after missing its heartbeat")
	}
	if _, err := b.Handle(t.Context(), &protocol.Message{Peer: protocol.Peer{ID: "torre"}, Payload: &protocol.MachineHeartbeat{DiskFreeBytes: 7}}); err != nil {
		t.Fatal(err)
	}
	if !p.Connected("torre") {
		t.Error("the Tower is still offline after a heartbeat")
	}
	if hb, ok := b.Last("torre"); !ok || hb.DiskFreeBytes != 7 {
		t.Errorf("last = %+v, %v", hb, ok)
	}
	if (heartbeatPresence{fixedPresence(false), b}).Connected("torre") {
		t.Error("a Tower with no connection is online")
	}
}

// The answer of the discovery tells how many machines are active and the
// SHA-256 of the CA (docs/conectividade/descoberta.md).
func TestTheAnswerOfTheDiscoveryCountsTheActiveMachinesAndNamesTheCA(t *testing.T) {
	t.Parallel()
	db, err := guildtpl.Open(t.Context(), filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for i, state := range []string{"active", "active", "removed", "left"} {
		if _, err := db.Writer().ExecContext(t.Context(), `INSERT INTO machines (machine_id, name, state, names, joined_at) VALUES (?, ?, ?, '[]', 0)`,
			string(rune('a'+i)), "m", state); err != nil {
			t.Fatal(err)
		}
	}
	ca := &x509.Certificate{Raw: []byte("der of the ca")}
	n, fp := guildFacts(t.Context(), db, ca)
	sum := sha256.Sum256(ca.Raw)
	if n != 2 || fp != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Errorf("facts = %d, %q", n, fp)
	}
}

// A new Guild takes the time zone of the system of the machine that made it.
func TestTheTimezoneOfANewGuildIsTheOneOfTheSystem(t *testing.T) {
	t.Setenv("TZ", "America/Sao_Paulo")
	if _, err := time.LoadLocation("America/Sao_Paulo"); err != nil {
		t.Skip("no tzdata")
	}
	if got := systemTimezone(); got != "America/Sao_Paulo" {
		t.Errorf("zone = %q", got)
	}
	t.Setenv("TZ", "Nowhere/Land")
	if got := systemTimezone(); got == "Nowhere/Land" || got == "" {
		t.Errorf("an unknown zone was taken: %q", got)
	}
}
