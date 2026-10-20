package app

import (
	"log/slog"
	"path/filepath"

	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/store"
)

// AttachTowers lets the tests of the other package of this folder put a double
// where the Citadel reaches the Towers.
type AttachTowers = attachTowers

// NewAttachmentsForTest is the copy of the attachments over a database, for the
// worlds of the tests: the one of the messages (`ask --from-message`) and the one
// of the tables that open with a message.
func NewAttachmentsForTest(db *store.DB, workspacesDir, machine string, towers AttachTowers) (waits.Attachments, tables.Attachments) {
	root, err := filepath.Abs(workspacesDir)
	if err != nil {
		root = workspacesDir
	}
	c := &attachmentCopies{db: db, tables: tables.Store{DB: db}, layout: workspaces.Layout{Root: root}, machine: machine, towers: towers, log: slog.New(slog.DiscardHandler)}
	return messageAttachments{c}, openAttachments{c}
}
