//go:build full

package app

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

// A secret that the vault opens through the engine is hidden in every line
// the engine logs afterwards (the line before it was opened shows it, so the
// masker is what hides it): the vault and the logger share the masker.
func TestASecretOpenedThroughTheEngineIsMaskedInItsLog(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	r.found(a, clock.Real{})
	a.lock = &losingLock{Lock: r.lock.For(a.id)}

	masker := logging.NewMasker()
	var out syncBuffer
	owner := &testOwner{e: &atomic.Pointer[Engine]{}}
	e, err := New(a.cfg, a.paths, Deps{
		Clock: clock.Real{}, Network: r.net.Host(a.name), Lock: a.lock, Approver: owner, Promotions: owner,
		Sheet: a.sheet.Sources(), RenewEvery: 20 * time.Millisecond,
		Masker: masker, Logger: logging.New(&out, masker, logging.Options{Level: slog.LevelDebug}),
	})
	if err != nil {
		t.Fatal(err)
	}
	owner.e.Store(e)
	a.e = e
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	r.waitFor("a to act", a.acting)

	// a plain secret, which no pattern of the masker would catch by itself
	const value = "zebra-quokka-0042"
	run := e.cit
	if err := run.vault.Put(t.Context(), "provider_key", vault.TypeProviderAPIKey, []byte(value), nil); err != nil {
		t.Fatal(err)
	}
	e.log.Info("before", "detail", "the value was "+value)
	if err := run.vault.Use(t.Context(), "provider_key", "test", func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	e.log.Info("opened", "detail", "the value was "+value)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var before, after string
	for _, l := range lines {
		switch {
		case strings.Contains(l, `"msg":"before"`):
			before = l
		case strings.Contains(l, `"msg":"opened"`):
			after = l
		}
	}
	if !strings.Contains(before, value) {
		t.Fatalf("the line before the vault opened it was masked already: %q", before)
	}
	if after == "" || strings.Contains(after, value) {
		t.Errorf("the line after the vault opened the secret through the engine: %q", after)
	}
}
