//go:build zz

package guildtpl

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/store"
)

func TestZZ(t *testing.T) {
	ctx := context.Background()
	b, _ := migrated()
	var tw, to, tc, tr, ts time.Duration
	N := 40
	for i := 0; i < N; i++ {
		dir, _ := os.MkdirTemp("", "zz")
		p := filepath.Join(dir, "m.db")
		t0 := time.Now()
		os.WriteFile(p, b, 0o600)
		t1 := time.Now()
		db, err := Open(ctx, p, store.Options{Synchronous: "OFF"})
		if err != nil {
			t.Fatal(err)
		}
		t2 := time.Now()
		db.Close()
		t3 := time.Now()
		os.RemoveAll(dir)
		t4 := time.Now()
		small := t.TempDir()
		for j := 0; j < 20; j++ {
			os.WriteFile(filepath.Join(small, "f"+string(rune('a'+j))), []byte("x"), 0o600)
		}
		t5 := time.Now()
		tw += t1.Sub(t0)
		to += t2.Sub(t1)
		tc += t3.Sub(t2)
		tr += t4.Sub(t3)
		ts += t5.Sub(t4)
	}
	d := time.Duration(N)
	t.Logf("ZZ write1.3MB %v open %v close %v removeall %v 20smallfiles %v", tw/d, to/d, tc/d, tr/d, ts/d)
}
