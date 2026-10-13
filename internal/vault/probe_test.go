package vault

// Temporary: timing probe for the Windows CI; removed before merge.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/pkg/clock"
)

func TestZZTimingProbe(t *testing.T) {
	if os.Getenv("MESTRE_PROBE") == "" && runtime.GOOS != "windows" {
		t.Skip("probe")
	}
	ctx := context.Background()
	c := clock.Real{}
	var out []string
	lap := func(name string, f func()) {
		start := c.Now()
		f()
		out = append(out, fmt.Sprintf("%s=%v", name, clock.Since(c, start).Round(100*time.Microsecond)))
	}
	var dir string
	lap("tempdir", func() { dir = t.TempDir() })
	var db *store.DB
	lap("open-fresh", func() {
		var err error
		db, err = guild.Open(ctx, filepath.Join(dir, "mestre.db"), store.Options{})
		if err != nil {
			t.Fatal(err)
		}
	})
	lap("close-fresh", func() { db.Close() })
	lap("reopen", func() {
		var err error
		db, err = guild.Open(ctx, filepath.Join(dir, "mestre.db"), store.Options{})
		if err != nil {
			t.Fatal(err)
		}
	})
	lap("insert", func() { _, _ = db.Writer().Exec(`DELETE FROM secrets`) })
	lap("close-2", func() { db.Close() })
	raw, _ := os.ReadFile(filepath.Join(dir, "mestre.db"))
	lap("copy-file", func() { _ = os.WriteFile(filepath.Join(dir, "copy.db"), raw, 0o600) })
	lap("open-copy", func() {
		var err error
		db, err = guild.Open(ctx, filepath.Join(dir, "copy.db"), store.Options{})
		if err != nil {
			t.Fatal(err)
		}
	})
	lap("write-durable", func() { _ = db.WriteDurable(ctx, func(tx *sql.Tx) error { return nil }) })
	lap("write-atomic", func() { _ = writeFileAtomic(filepath.Join(dir, "env.bin"), []byte("x")) })
	lap("write-plain", func() { _ = os.WriteFile(filepath.Join(dir, "plain.bin"), []byte("x"), 0o600) })
	rk, _ := NewRecoveryKey()
	cfg := Config{DB: db, Keys: &memKeys{}, EnvelopePath: filepath.Join(dir, "cofre-envelope.bin"), RecoveryPublicKey: rk.PublicKey(), Masker: logging.NewMasker(), Clock: c}
	var v *Vault
	lap("create", func() { v, _ = Create(ctx, cfg) })
	lap("put7", func() {
		for _, s := range samples {
			_ = v.Put(ctx, s.name, s.typ, []byte(s.value), s.site)
		}
	})
	lap("rotate", func() { _ = v.Rotate(ctx) })
	lap("open-vault", func() { _, _ = Open(ctx, cfg) })
	lap("close-3", func() { db.Close() })
	t.Errorf("PROBE %s", strings.Join(out, " "))
}
