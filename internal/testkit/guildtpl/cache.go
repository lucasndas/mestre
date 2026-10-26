package guildtpl

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"

	"github.com/mestre-ia/mestre/internal/privdir"
	"github.com/mestre-ia/mestre/internal/store"
)

// formatVersion changes when what the template holds changes without a new
// migration (a pragma Open sets at creation, for one), so no old file is reused.
const formatVersion = "1"

// shared returns the migrated database file of set, building it with build
// only when no test binary has left it in the temporary folder yet. Forty
// packages use a template, and each used to migrate its own: on the Windows of
// the CI that was seconds of CPU each, all at once, a good part of the fast
// suite (docs/qualidade/testes.md). The name carries a hash of every migration
// and of the SQLite driver, so a changed schema never meets an old file; the
// file is published by a rename, so a reader never sees half of it, and a
// failure to read or to publish only means this binary migrates for itself.
func shared(kind string, set store.Set, build func() ([]byte, error)) ([]byte, error) {
	path := cachePath(kind, set)
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		return b, nil
	}
	b, err := build()
	if err != nil {
		return nil, err
	}
	publish(path, b)
	return b, nil
}

func cachePath(kind string, set store.Set) string {
	h := sha256.New()
	fmt.Fprintln(h, formatVersion, kind)
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "modernc.org/sqlite" {
				fmt.Fprintln(h, d.Path, d.Version)
			}
		}
	}
	for _, m := range set.Migrations() {
		fmt.Fprintln(h, m.Version, m.Name, strconv.Itoa(len(m.SQL)))
		h.Write([]byte(m.SQL))
	}
	return filepath.Join(os.TempDir(), "mestre-dbtpl", kind+"-"+hex.EncodeToString(h.Sum(nil))[:24]+".db")
}

// publish writes b at path atomically; every error is ignored on purpose.
func publish(path string, b []byte) {
	dir := filepath.Dir(path)
	if privdir.Ensure(dir) != nil {
		return
	}
	f, err := os.CreateTemp(dir, "new-*")
	if err != nil {
		return
	}
	_, werr := f.Write(b)
	cerr := f.Close()
	if werr != nil || cerr != nil || os.Rename(f.Name(), path) != nil {
		_ = os.Remove(f.Name())
	}
}
