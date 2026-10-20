package sessionzero

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

// told is the Users of the test: it keeps what the Administrators were told.
type told struct {
	mu    sync.Mutex
	kinds []string
	texts []string
}

func (u *told) AskOwnerToInstall(context.Context, string, []InstallLink) error { return nil }
func (u *told) AskVersioning(context.Context, string) error                    { return nil }
func (u *told) Notify(_ context.Context, _ string, n Notice) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.kinds, u.texts = append(u.kinds, n.Kind), append(u.texts, n.Text)
	return nil
}

func (u *told) count(kind string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	n := 0
	for _, k := range u.kinds {
		if k == kind {
			n++
		}
	}
	return n
}

// verificationRig is the session zero of a workspace with the main clone in a folder.
func verificationRig(t *testing.T) (*Service, *told, string) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(t.Context(), filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	u := &told{}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".mestre", "regras"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Service{o: Options{Users: u, Clock: clk}, store: Store{DB: db}}, u, dir
}

// CA-SZ-WS-08
func TestTheAnalysisProposesTheVerificationCommandsAndNothingHoldsBeforeAnAdministratorConfirms(t *testing.T) {
	t.Parallel()
	s, u, dir := verificationRig(t)
	ctx := t.Context()
	file := filepath.Join(dir, ".mestre", "regras", "verificacao.yaml")

	// a Go project analysed by the Grand Master: the proposal is in the file
	if err := s.proposeVerification(ctx, "meu-app", dir, []string{"go build ./...", "go test ./..."}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("the proposal is not in .mestre/regras/verificacao.yaml: %v", err)
	}
	v, err := ParseVerification(b)
	if err != nil || len(v.Commands) != 2 || v.Commands[0] != "go build ./..." || v.Commands[1] != "go test ./..." {
		t.Fatalf("the proposal = %+v, %v; want go build ./... and go test ./...", v, err)
	}
	if u.count("verification_proposed") != 1 {
		t.Errorf("the Administrators were told %d times that a proposal waits; want once", u.count("verification_proposed"))
	}
	// ... and it does not hold: nothing is confirmed
	if _, ok, err := s.verificationCommands(ctx, "meu-app", dir); err != nil || ok {
		t.Fatalf("the proposal holds before the confirmation: %v, %v", ok, err)
	}
	// running the stage again does not change what is there
	if err := s.proposeVerification(ctx, "meu-app", dir, []string{"make"}); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(file); string(again) != string(b) {
		t.Errorf("a new run of the analysis rewrote the proposal:\n%s", again)
	}

	// who is not an Administrator confirms nothing; a file that is not the one that was seen neither
	hash := VerificationHash(b)
	for _, by := range []classes.Author{{Level: classes.LevelApprover, ID: "ze"}, {Level: classes.LevelGrandMaster, ID: "gm"}} {
		if err := s.confirmVerification(ctx, "meu-app", dir, hash, by); !errors.Is(err, ErrVerificationNeedsAdministrator) {
			t.Errorf("%s confirmed: %v", by.Level, err)
		}
	}
	admin := classes.Author{Level: classes.LevelAdmin, ID: "ana"}
	if err := s.confirmVerification(ctx, "meu-app", dir, "outro", admin); !errors.Is(err, ErrVerificationChanged) {
		t.Errorf("a confirmation of a version nobody saw: %v", err)
	}
	if err := s.confirmVerification(ctx, "meu-app", dir, hash, admin); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.verificationCommands(ctx, "meu-app", dir)
	if err != nil || !ok || len(got.Commands) != 2 || got.Timeout != DefaultVerificationTimeout {
		t.Fatalf("after the confirmation: %+v, %v, %v", got, ok, err)
	}

	// an edit made outside the panel: the last confirmed version holds and the Administrators are told, once
	edited := "version: 1\ncomandos: [\"true\"]\n"
	if err := os.WriteFile(file, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got, ok, err = s.verificationCommands(ctx, "meu-app", dir)
		if err != nil || !ok || len(got.Commands) != 2 || got.Commands[1] != "go test ./..." {
			t.Fatalf("after an edit outside the panel: %+v, %v, %v; want the last confirmed version", got, ok, err)
		}
	}
	if u.count("verification_edited") != 1 || !strings.Contains(u.texts[len(u.texts)-1], "última versão confirmada") {
		t.Errorf("the Administrators were told %d times of the edit (%q); want once", u.count("verification_edited"), u.texts)
	}
	// the edit holds only when an Administrator confirms it
	if err := s.confirmVerification(ctx, "meu-app", dir, VerificationHash([]byte(edited)), admin); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.verificationCommands(ctx, "meu-app", dir); len(got.Commands) != 1 || got.Commands[0] != "true" {
		t.Errorf("after the confirmation of the edit: %+v", got)
	}
}

func TestAVerificationFileThatIsNotValidIsRefused(t *testing.T) {
	t.Parallel()
	for name, bad := range map[string]string{
		"no version":       "comandos: [\"go test ./...\"]\n",
		"no commands":      "version: 1\ncomandos: []\n",
		"unknown field":    "version: 1\ncomandos: [\"a\"]\nextra: 1\n",
		"a multi-line one": "version: 1\ncomandos: [\"a\\nb\"]\n",
		"a bad timeout":    "version: 1\ncomandos: [\"a\"]\ntimeout: soon\n",
		"two documents":    "version: 1\ncomandos: [\"a\"]\n---\nversion: 1\n",
	} {
		if _, err := ParseVerification([]byte(bad)); !errors.Is(err, ErrInvalidVerification) {
			t.Errorf("%s: %v; want ErrInvalidVerification", name, err)
		}
	}
	if v, err := ParseVerification([]byte("version: 1\ncomandos: [\"go build ./...\"]\ntimeout: 10m\n")); err != nil || v.Timeout != 10*time.Minute {
		t.Errorf("a valid file: %+v, %v", v, err)
	}
}
