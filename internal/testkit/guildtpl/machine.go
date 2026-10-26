package guildtpl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/mestre-ia/mestre/internal/store"
	machinedb "github.com/mestre-ia/mestre/internal/store/machine"
)

var (
	mOnce     sync.Once
	mTemplate []byte
	mTplErr   error
)

// migratedMachine returns the machine database file as the migrations leave it.
func migratedMachine() ([]byte, error) {
	mOnce.Do(func() {
		dir, err := os.MkdirTemp("", "machinetpl")
		if err != nil {
			mTplErr = err
			return
		}
		defer os.RemoveAll(dir)
		path := filepath.Join(dir, "maquina.db")
		db, err := machinedb.Open(context.Background(), path, store.Options{Synchronous: "OFF"})
		if err != nil {
			mTplErr = err
			return
		}
		if err := db.Close(); err != nil {
			mTplErr = err
			return
		}
		mTemplate, mTplErr = os.ReadFile(path)
	})
	return mTemplate, mTplErr
}

// OpenMachine is machinedb.Open for a test: when no database exists at path
// yet, it starts from the migrated copy, so Open only finds nothing left to
// apply. One reader unless opts says otherwise.
func OpenMachine(ctx context.Context, path string, opts store.Options) (*store.DB, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		b, err := migratedMachine()
		if err != nil {
			return nil, fmt.Errorf("guildtpl: %w", err)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return nil, fmt.Errorf("guildtpl: %w", err)
		}
	}
	if opts.ReadConns <= 0 {
		opts.ReadConns = 1
	}
	if opts.StmtCache == 0 {
		opts.StmtCache = StmtCache
	}
	return machinedb.Open(ctx, path, opts)
}
