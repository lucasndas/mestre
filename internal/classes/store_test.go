package classes_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	machinedb "github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The machine and the Guild databases are migrated once, by TestMain, outside
// the time of every test; a test starts from a copy (docs/qualidade/testes.md).
var machineTemplate, guildTemplate []byte

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "classes-template")
	if err == nil {
		path := filepath.Join(dir, "maquina.db")
		var db *store.DB
		if db, err = machinedb.Open(context.Background(), path, store.Options{Synchronous: "OFF"}); err == nil {
			if err = db.Close(); err == nil {
				machineTemplate, err = os.ReadFile(path)
			}
		}
		if err == nil {
			path = filepath.Join(dir, "mestre.db")
			if db, err = guilddb.Open(context.Background(), path, store.Options{Synchronous: "OFF"}); err == nil {
				if err = db.Close(); err == nil {
					guildTemplate, err = os.ReadFile(path)
				}
			}
		}
		os.RemoveAll(dir)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "classes: template:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func newStore(t *testing.T) *classes.Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "maquina.db")
	if err := os.WriteFile(path, machineTemplate, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := machinedb.Open(context.Background(), path, store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &classes.Store{DB: db, Clock: clock.NewFake(now), Agents: filepath.Join(dir, "agentes")}
}

func TestStoreKeepsTheNewestRulesAndTheirSnapshot(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	r := rules(t, qaYAML)
	if err := s.Save(ctx, r); err != nil {
		t.Fatal(err)
	}
	newer := rules(t, anyYAML)
	newer.GeneratedAt = now.Add(time.Second)
	if err := s.Save(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, r); !errors.Is(err, classes.ErrStale) {
		t.Fatalf("older document: %v; want ErrStale", err)
	}
	got, err := s.Rules(ctx, agentID)
	if err != nil || !got.GeneratedAt.Equal(newer.GeneratedAt) || !got.Operator {
		t.Fatalf("Rules = %+v, %v; want the newer document", got, err)
	}
	snap, err := classes.LoadSnapshot(s.Agents, agentID)
	if err != nil || !snap.GeneratedAt.Equal(newer.GeneratedAt) {
		t.Fatalf("snapshot = %+v, %v", snap, err)
	}
	info, err := os.Stat(filepath.Join(s.Agents, agentID, classes.SnapshotName))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o640 && os.PathSeparator == '/' {
		t.Fatalf("policy.json mode %o; want 640", mode)
	}
	if _, err := s.Rules(ctx, "01JQNINGUEM"); err == nil {
		t.Fatal("rules of an unknown character")
	}
	if _, err := classes.LoadSnapshot(s.Agents, "../x"); err == nil {
		t.Fatal("a snapshot path out of agentes/ was read")
	}
}

func TestStoreUsesAnExceptionOnceUntilItLeavesTheDocument(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	ex := classes.Decree{ID: 3, Kind: classes.DecreeException, Level: classes.LevelApprover, Effect: classes.Effect{Kind: classes.EffectPermission},
		Action: &classes.Action{Kind: providers.KindShell, Command: "npm install x"}}
	if err := s.Save(ctx, rules(t, qaYAML, ex)); err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{true, false} {
		if used, err := s.Use(ctx, agentID, 3); err != nil || used != want {
			t.Fatalf("use %d = %v, %v; want %v", i, used, err, want)
		}
	}
	// A new version that still lists it keeps it used.
	again := rules(t, qaYAML, ex)
	again.GeneratedAt = now.Add(time.Second)
	if err := s.Save(ctx, again); err != nil {
		t.Fatal(err)
	}
	if used, _ := s.Use(ctx, agentID, 3); used {
		t.Fatal("the exception was used twice")
	}
	// Decide with the store as the ledger.
	m := newMachine()
	env := m.env()
	env.Exceptions = s
	d := decide(env, again, m.shell("npm install x"))
	if d.Verdict != classes.Ask {
		t.Fatalf("a used exception allowed again: %+v", d)
	}
	// Gone from the list: the record goes too.
	gone := rules(t, qaYAML)
	gone.GeneratedAt = now.Add(2 * time.Second)
	if err := s.Save(ctx, gone); err != nil {
		t.Fatal(err)
	}
	if used, _ := s.Use(ctx, agentID, 3); !used {
		t.Fatal("the record of an exception out of the document was kept")
	}
}

func TestAgentRulesMessage(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	ctx := context.Background()
	citadel := protocol.Peer{Role: protocol.RoleMachine, ID: "01JQCIDADELA"}
	h := s.Handler(func(p protocol.Peer) bool { return p == citadel })
	out, err := classes.Message(rules(t, qaYAML))
	if err != nil || out.Type != protocol.TypeAgentRules {
		t.Fatalf("Message = %+v, %v", out, err)
	}
	obj := out.Payload.(protocol.Object)
	if _, err := h(ctx, &protocol.Message{Payload: &obj, Peer: protocol.Peer{Role: protocol.RoleMachine, ID: "01JQTORRE"}}); !isCode(err, protocol.CodeForbidden) {
		t.Fatalf("from a Tower: %v; want forbidden", err)
	}
	if _, err := h(ctx, &protocol.Message{Payload: &obj, Peer: citadel}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rules(ctx, agentID); err != nil {
		t.Fatalf("the rules were not kept: %v", err)
	}
	bad := protocol.Object{"version": []byte("1"), "agent_id": []byte(`"../x"`)}
	if _, err := h(ctx, &protocol.Message{Payload: &bad, Peer: citadel}); !isCode(err, protocol.CodeInvalidPayload) {
		t.Fatalf("invalid document: %v; want invalid_payload", err)
	}
	if _, err := h(ctx, &protocol.Message{Payload: &obj, Peer: citadel}); err != nil {
		t.Fatalf("the same version again: %v", err)
	}
}

func isCode(err error, code string) bool {
	var pe *protocol.Error
	return errors.As(err, &pe) && pe.Code == code
}
