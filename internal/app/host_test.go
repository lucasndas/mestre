package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/verification"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Part of CA-CIDADELA-15: what the Citadel hosts is built over its database,
// and what depends on a stage of milestone 5 refuses with the reason.
func TestTheOrchestrationOfTheCitadelIsBuiltAndWhatWaitsForMilestone5Refuses(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	db, err := guildtpl.Open(t.Context(), filepath.Join(root, "mestre.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e := &Engine{clk: clock.Real{}, log: slog.New(slog.DiscardHandler), id: guild.Identity{MachineID: "m1"},
		paths: Paths{Workspaces: filepath.Join(root, "workspaces"), Maquina: filepath.Join(root, "maquina")},
		deps:  Deps{Masker: logging.NewMasker(), Binary: filepath.Join(root, "mestre")}}
	orc, err := e.hostOrchestration(db, nil)
	if err != nil || orc == nil || orc.Workspaces == nil || orc.Characters == nil || orc.Inbox == nil {
		t.Fatalf("orchestration: %+v, %v", orc, err)
	}
	for _, p := range []Paths{{}, {Workspaces: filepath.Join(root, "w")}, {Maquina: filepath.Join(root, "m")}} {
		if _, err := (&Engine{paths: p, deps: Deps{Masker: logging.NewMasker()}}).hostOrchestration(db, nil); err == nil {
			t.Fatalf("the orchestration was built with the folders %+v", p)
		}
	}

	ctx := context.Background()
	for what, err := range map[string]error{
		"github": func() error { _, err := unavailableGitHub{}.Repository(ctx, workspaces.Repo{}); return err }(),
		"epic":   func() error { _, err := unavailableEpics{}.Epic(ctx, "w", 1); return err }(),
		"close":  unavailableEpics{}.CloseEpic(ctx, "w", 1),
		"pr":     unavailableEpics{}.ClosePR(ctx, "w", 1),
	} {
		if err == nil || !strings.Contains(err.Error(), "milestone 5") {
			t.Errorf("%s: %v; want a refusal that names milestone 5", what, err)
		}
	}
	// The session zero of a character refuses with what a run without the people
	// of the chain lacks (SessionBuilder is the real one).
	if _, err := (unavailableBuilder{}).Build(ctx, characters.BuildInput{}); err == nil || !strings.Contains(err.Error(), "decrees") {
		t.Errorf("builder: %v; want a refusal that names what is missing", err)
	}
}

// The executor of the verification is composed by the host (milestone 5, by
// injected configuration): it reads the commands the workspace confirmed in the
// database of the Guild, and a machine with no terminals (no Config.Terminais)
// confirms nothing, so the done ends unverified and the gate does not approve
// alone.
func TestTheHostComposesTheExecutorOfTheVerificationAndWithoutTerminalsItIsNeverGreen(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	db, err := guildtpl.Open(t.Context(), filepath.Join(root, "mestre.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e := &Engine{clk: clock.Real{}, log: slog.New(slog.DiscardHandler), id: guild.Identity{MachineID: "m1"},
		paths: Paths{Workspaces: filepath.Join(root, "workspaces"), Maquina: filepath.Join(root, "maquina")},
		deps:  Deps{Masker: logging.NewMasker(), Binary: filepath.Join(root, "mestre")}}
	orc, err := e.hostOrchestration(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	ex, ok := orc.Verifier.(*verification.Executor)
	if !ok || ex.Worktrees == nil {
		t.Fatalf("the verifier of the host = %T", orc.Verifier)
	}
	if ok, err := orc.Verifier.Confirmed(t.Context(), "loja"); err != nil || ok {
		t.Fatalf("no command confirmed: %v, %v", ok, err)
	}
	if _, err := (verification.Store{DB: db, Clock: clock.Real{}}).Confirm(t.Context(), "loja", "u1", []byte("version: 1\ncomandos: [\"make\"]\n")); err != nil {
		t.Fatal(err)
	}
	if ok, err := orc.Verifier.Confirmed(t.Context(), "loja"); err != nil || ok {
		t.Fatalf("a machine with no terminals confirmed commands: %v, %v", ok, err)
	}
	if res, err := orc.Verifier.Verify(t.Context(), quests.VerifyRequest{Workspace: "loja", Table: "1", Branch: "b"}); err == nil || res.OK {
		t.Fatalf("a machine with no terminals verified: %+v, %v", res, err)
	}
}
