package app

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/replication"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// sendFile sends one file of the folders of the Citadel to a Tower now (the
// replication of the files), for the commands that need the file there.
func (e *Engine) sendFile(ctx context.Context, machine string, ref replication.FileRef) error {
	e.mu.Lock()
	run := e.cit
	e.mu.Unlock()
	if run == nil || run.primary == nil {
		return replication.ErrNoTower
	}
	return run.primary.SendFile(ctx, machine, ref)
}

// towerAttachmentCopies is the copy of the attachments on this Tower, over its
// replica of the Guild database.
func (e *Engine) towerAttachmentCopies(db *store.DB) (*attachmentCopies, error) {
	root, err := filepath.Abs(e.paths.Workspaces)
	if err != nil {
		return nil, err
	}
	return &attachmentCopies{db: db, layout: workspaces.Layout{Root: root}, machine: e.id.MachineID, log: e.log}, nil
}

// towerCatchUp is what the Tower does with the rows the replica brings: the
// attachments that name a table of its own are copied into the worktree of the
// table (the files came before the rows). It runs in the background, one at a
// time, and a failure is only logged: the next batch, or the next connection,
// tries again.
func (e *Engine) towerCatchUp(copies *attachmentCopies, sync *attachSync) func(first, last uint64) {
	return func(uint64, uint64) {
		sync.run(func() {
			ctx, cancel := clock.WithTimeout(context.Background(), e.clk, time.Minute)
			defer cancel()
			if err := copies.catchUp(ctx, e.towerWorktreeDir); err != nil {
				e.log.Warn("app: attachments were not copied into the tables of this Tower", "err", err)
			}
		})
	}
}

// registerTowerAttachments puts in the router of a Tower the handler of
// `table.attach`: the Citadel has sent the files to the main clone of this
// machine and asks for the copy into the worktree of a table.
func (e *Engine) registerTowerAttachments(router *protocol.Router, copies *attachmentCopies) {
	router.HandleFunc(protocol.TypeTableAttach, func(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
		if !e.fromCitadel(m.Peer) {
			return nil, protocol.NewError(protocol.CodeForbidden, "table.attach: only the Citadel gives this command")
		}
		p, ok := m.Payload.(*protocol.TableAttach)
		if !ok {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "table.attach: %T", m.Payload)
		}
		if !names.MatchString(p.Workspace) || !names.MatchString(p.Table) {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "table.attach: the workspace and the table are plain names")
		}
		dir := e.towerWorktreeDir(p.Workspace, p.Table)
		if err := e.towerWorktree(dir); err != nil {
			return nil, protocol.NewError(protocol.CodeForbidden, "table.attach: %v", err)
		}
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			return nil, protocol.NewError(protocol.CodeMissingPrerequisite, "table.attach: this machine has no worktree of the table %s", p.Table)
		}
		if err := copies.copyInto(p.Workspace, dir, p.Paths); err != nil {
			return nil, protocol.NewError(protocol.CodeMissingPrerequisite, "table.attach: %v", err)
		}
		return nil, nil
	})
}
