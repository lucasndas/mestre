package app_test

import (
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
)

// seedGuild leaves in the folder of the Guild database the file migrated
// once per test binary, so Found only finds nothing left to apply
// (internal/testkit/guildtpl).
func seedGuild(t *testing.T, paths app.Paths) {
	t.Helper()
	if err := guildtpl.Seed(filepath.Join(paths.Banco, "mestre.db")); err != nil {
		t.Fatal(err)
	}
}
