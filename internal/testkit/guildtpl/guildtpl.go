// Package guildtpl opens the Guild database of a test from a copy of a file
// migrated once per test binary. Applying the twenty migrations is a third of
// what a test rig costs under the race detector, and every test rig does it;
// a copy of the migrated file is not (docs/qualidade/testes.md). Production
// code must not import it (depguard in .golangci.yml).
package guildtpl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
)

var (
	once     sync.Once
	template []byte
	tplErr   error
)

// migrated returns the Guild database file as the migrations leave it.
func migrated() ([]byte, error) {
	once.Do(func() {
		dir, err := os.MkdirTemp("", "guildtpl")
		if err != nil {
			tplErr = err
			return
		}
		defer os.RemoveAll(dir)
		path := filepath.Join(dir, "mestre.db")
		db, err := guilddb.Open(context.Background(), path, store.Options{Synchronous: "OFF"})
		if err != nil {
			tplErr = err
			return
		}
		if err := db.Close(); err != nil {
			tplErr = err
			return
		}
		template, tplErr = os.ReadFile(path)
	})
	return template, tplErr
}

// Open is guilddb.Open for a test: when no database exists at path yet (and
// it is not a replica, which gets its schema from the replication), it starts
// from the migrated copy, so Open only finds nothing left to apply.
func Open(ctx context.Context, path string, opts store.Options) (*store.DB, error) {
	if !opts.Replica {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			b, err := migrated()
			if err != nil {
				return nil, fmt.Errorf("guildtpl: %w", err)
			}
			if err := os.WriteFile(path, b, 0o600); err != nil {
				return nil, fmt.Errorf("guildtpl: %w", err)
			}
		}
	}
	return guilddb.Open(ctx, path, opts)
}
