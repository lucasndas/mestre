package app

import (
	"log/slog"
	"testing"

	"github.com/lucasndas/mestre/internal/logging"
)

// Part of CA-PERM-26: the hook engine of `mestre run` judges what is written
// with the masker of the engine, the one the vault registers its secrets in.
func TestTheHookEngineUsesTheMaskerOfTheEngine(t *testing.T) {
	t.Parallel()
	m := logging.NewMasker()
	e := &Engine{deps: Deps{Masker: m}, log: slog.New(slog.DiscardHandler)}
	if got := e.hookEngine(nil).Masker; got != m {
		t.Fatalf("the hook engine has the masker %p; want the one of the engine, %p", got, m)
	}
}
