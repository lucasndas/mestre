package chain_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/pkg/clock"
)

func policyDB(t *testing.T, path string) *chain.PolicyDB {
	t.Helper()
	db, err := guildtpl.Open(t.Context(), path, store.Options{Synchronous: "OFF", Clock: clock.Real{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &chain.PolicyDB{DB: db}
}

// CA-APROV-POL-22 (the last valid policy is in the database: a restart with the file replaced,
// missing or untrusted keeps it, and the default content does not come back)
func TestTheLastValidPolicyHoldsAfterARestartWhenTheFileIsGone(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "mestre.db")
	db := policyDB(t, dbPath)
	r := newPolicyRig(t)
	r.file.Store = db
	r.write("versao: 1\nexpiracao: 6h\ncadeia:\n  prazo_agente: 3m\n")
	_, v := r.chain()
	if v == "default" {
		t.Fatal("the file did not hold")
	}
	db.Flush() // the write is on its own goroutine: wait for it, no sleep

	// "restart": a new PolicyFile over the same database; the folder was replaced (no file)
	if err := os.Remove(r.path); err != nil {
		t.Fatal(err)
	}
	again := newPolicyRig(t)
	again.file.Store = &chain.PolicyDB{DB: db.DB}
	got, gotV := again.chain()
	if gotV != v || got.PeopleDeadline != 6*time.Hour || got.AgentDeadline != 3*time.Minute {
		t.Fatalf("missing file after a restart: %+v %q; want the stored version %q", got, gotV, v)
	}

	// an untrusted or invalid file fails closed to the stored version too, and the Owner is told
	again2 := newPolicyRig(t)
	again2.file.Store = &chain.PolicyDB{DB: db.DB}
	again2.file.Read = func(string) ([]byte, error) { return nil, chain.ErrInvalidPolicy } // stands for ErrUntrusted
	got, gotV = again2.chain()
	if gotV != v || got.PeopleDeadline != 6*time.Hour {
		t.Fatalf("unreadable file after a restart: %+v %q; want the stored version", got, gotV)
	}
	if len(again2.alerts) != 1 {
		t.Fatalf("alerts = %v, want the Owner told once", again2.alerts)
	}
	again3 := newPolicyRig(t)
	again3.file.Store = &chain.PolicyDB{DB: db.DB}
	again3.write("versao: 1\nexpiracao: depressa\n")
	if _, gotV = again3.chain(); gotV != v {
		t.Fatalf("invalid file after a restart: %q, want %q", gotV, v)
	}
}

// CA-APROV-POL-22 (without any stored version the default content holds, and a new valid file replaces the stored one)
func TestTheStoredPolicyIsReplacedByTheNextValidFileAndDefaultHoldsWithoutOne(t *testing.T) {
	t.Parallel()
	db := policyDB(t, filepath.Join(t.TempDir(), "mestre.db"))
	r := newPolicyRig(t)
	r.file.Store = db
	if _, v := r.chain(); v != "default" {
		t.Fatalf("nothing stored and no file: %q, want default", v)
	}
	r.write("versao: 1\nexpiracao: 6h\n")
	r.chain()
	r.write("versao: 1\nexpiracao: 8h\n")
	_, v2 := r.chain()
	db.Flush()
	if err := os.Remove(r.path); err != nil {
		t.Fatal(err)
	}
	again := newPolicyRig(t)
	again.file.Store = &chain.PolicyDB{DB: db.DB}
	if got, gotV := again.chain(); gotV != v2 || got.PeopleDeadline != 8*time.Hour {
		t.Fatalf("restart: %+v %q; want the last valid version %q", got, gotV, v2)
	}
}
