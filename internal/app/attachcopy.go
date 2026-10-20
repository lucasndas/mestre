package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/replication"
	"github.com/lucasndas/mestre/internal/safefile"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/store"
)

// The copy of the attachments into the worktrees of a table
// (docs/telegram/anexos-e-transcricao.md#recebimento-de-anexos): a file that a
// message brought lives in the main clone of the workspace, under
// `.mestre/anexos/`; when the message goes to a character of a table the engine
// puts a copy at the same path in every worktree of the table, on the Citadel
// and on each Tower, and writes the table in the row of the attachment
// (`attachments.tables`). The Towers have the main clone by the replication of
// the files (the row cites the file, so it arrives before the row), and copy it
// into the worktree of the table when the Citadel asks (`table.attach`) and
// again when the replica brings a row that names a table of theirs.

// attachDir is where the attachments are, in the main clone and in a worktree.
const attachDir = ".mestre/anexos/"

// maxAttachCopy is the largest attachment the engine copies: the 50 MB of the
// limit of the Bot API for a document, the most a user or the Grand Master sends.
const maxAttachCopy = 50 << 20

// attachMode is the mode of a copy: nothing an agent receives is executable.
const attachMode fs.FileMode = 0o664

// errAttachPath is a path that is not an attachment of the main clone.
var errAttachPath = errors.New("the path is not an attachment")

// attachRel checks the path of an attachment: relative to the main clone, clean,
// below `.mestre/anexos/`, with no name the systems read differently and no
// control character. Every path that reaches a copy goes through it.
func attachRel(p string) (string, error) {
	switch {
	case !strings.HasPrefix(p, attachDir) || len(p) == len(attachDir),
		p != path.Clean(p) || !fs.ValidPath(p),
		strings.ContainsAny(p, "\\:\x00"),
		strings.IndexFunc(p, unicode.IsControl) >= 0:
		return "", fmt.Errorf("%w: %q", errAttachPath, p)
	}
	return p, nil
}

// attachTowers puts attachments in the worktrees a table has on a Tower.
type attachTowers interface {
	// Ship sends the files to the main clone of the Tower and has it copy them
	// into the worktree of the table; it returns when they are there.
	Ship(ctx context.Context, machine, workspace, table string, rels []string) error
}

// attachmentCopies is the copy of the attachments on one machine: it reads the
// files of the main clone and the rows of the attachments of its own copy of the
// Guild database.
type attachmentCopies struct {
	db      *store.DB
	tables  tables.Store
	layout  workspaces.Layout
	machine string
	towers  attachTowers
	log     *slog.Logger
}

var _ sessionzero.AttachmentSource = (*attachmentCopies)(nil)

// source reads an attachment of the main clone.
func (c *attachmentCopies) source(workspace, rel string) ([]byte, error) {
	data, _, err := safefile.Read(filepath.Join(c.layout.Principal(workspace), filepath.FromSlash(rel)), maxAttachCopy)
	if err != nil {
		return nil, fmt.Errorf("o anexo %s não pôde ser lido: %w", rel, err)
	}
	return data, nil
}

// put writes a copy into a worktree, unless it has the same size already (an
// attachment never changes: its name has its id).
func put(dir, rel string, data []byte) error {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if info, err := safefile.Stat(p); err == nil && info.Exists && info.Size == int64(len(data)) {
		return nil
	}
	return safefile.Write(p, data, safefile.Options{Mode: attachMode})
}

// copyInto copies attachments of the main clone into a worktree of this machine.
func (c *attachmentCopies) copyInto(workspace, dir string, rels []string) error {
	for _, rel := range rels {
		if _, err := attachRel(rel); err != nil {
			return err
		}
		data, err := c.source(workspace, rel)
		if err != nil {
			return err
		}
		if err := put(dir, rel, data); err != nil {
			return fmt.Errorf("o anexo %s não foi copiado: %w", rel, err)
		}
	}
	return nil
}

// deliver makes the attachments exist in every worktree of the table, on this
// machine and on the Towers, and records the table in their rows (first, so a
// Tower that is away gets them when it is back). It returns when every copy is
// done; one that is not (a Tower that does not answer) is an error.
func (c *attachmentCopies) deliver(ctx context.Context, workspace, table string, rels []string) error {
	for _, rel := range rels {
		if _, err := attachRel(rel); err != nil {
			return err
		}
	}
	wts, err := c.tables.Worktrees(ctx, workspace, table)
	if err != nil {
		return err
	}
	if len(wts) == 0 {
		return fmt.Errorf("a mesa %s não tem worktree", table)
	}
	if err := c.mark(ctx, workspace, table, rels); err != nil {
		return err
	}
	for _, w := range wts {
		if w.Machine == c.machine {
			if err := c.copyInto(workspace, w.Dir, rels); err != nil {
				return err
			}
			continue
		}
		if c.towers == nil {
			return fmt.Errorf("a mesa %s tem worktree em %s e este motor não alcança Torres", table, w.Machine)
		}
		if err := c.towers.Ship(ctx, w.Machine, workspace, table, rels); err != nil {
			return fmt.Errorf("os anexos não chegaram à Torre %s: %w", w.Machine, err)
		}
	}
	return nil
}

// mark writes the table in the rows of the attachments (the tables the file was
// copied to). A path with no row is a file that did not come by the channel: it
// is copied all the same and nothing is recorded.
func (c *attachmentCopies) mark(ctx context.Context, workspace, table string, rels []string) error {
	return c.db.WriteDurable(ctx, func(tx *sql.Tx) error {
		for _, rel := range rels {
			var raw string
			err := tx.QueryRowContext(ctx, `SELECT tables FROM attachments WHERE workspace = ? AND path = ?`, workspace, rel).Scan(&raw)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			var list []string
			if raw != "" {
				if err := json.Unmarshal([]byte(raw), &list); err != nil {
					return fmt.Errorf("attachments.tables of %s: %w", rel, err)
				}
			}
			if slices.Contains(list, table) {
				continue
			}
			out, err := json.Marshal(append(list, table))
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE attachments SET tables = ? WHERE workspace = ? AND path = ?`, string(out), workspace, rel); err != nil {
				return err
			}
		}
		return nil
	})
}

// For implements sessionzero.AttachmentSource: the attachments recorded for the
// table, which the session zero copies into `.mestre/anexos/` of its worktree
// (a worktree made after the attachments were forwarded still gets them).
func (c *attachmentCopies) For(ctx context.Context, workspace, table string) ([]sessionzero.File, error) {
	rows, err := c.db.Reader().QueryContext(ctx, `SELECT path FROM attachments WHERE workspace = ? AND EXISTS (SELECT 1 FROM json_each(tables) WHERE value = ?) ORDER BY created_at`, workspace, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var files []sessionzero.File
	for _, p := range paths {
		rel, err := attachRel(p)
		if err != nil {
			c.log.Warn("app: an attachment with a path that is not one", "path", p)
			continue
		}
		data, err := c.source(workspace, rel)
		if errors.Is(err, fs.ErrNotExist) {
			continue // not here yet: the replication brings the file before the row, and the next start finds it
		}
		if err != nil {
			return nil, err
		}
		files = append(files, sessionzero.File{Path: strings.TrimPrefix(rel, attachDir), Data: data})
	}
	return files, nil
}

// catchUp copies, into the worktrees this machine has, the attachments the rows
// record for their tables: what a Tower does when it connects and when the
// replica brings rows. dir gives the worktree of a table (a folder that does not
// exist is a table this machine has no part in).
func (c *attachmentCopies) catchUp(ctx context.Context, dir func(workspace, table string) string) error {
	rows, err := c.db.Reader().QueryContext(ctx, `SELECT workspace, path, tables FROM attachments WHERE tables <> '[]' AND tables <> '' ORDER BY created_at`)
	if err != nil {
		return err
	}
	type key struct{ ws, table string }
	want := map[key][]string{}
	for rows.Next() {
		var ws, p, raw string
		if err := rows.Scan(&ws, &p, &raw); err != nil {
			_ = rows.Close()
			return err
		}
		var list []string
		if json.Unmarshal([]byte(raw), &list) != nil {
			continue
		}
		for _, t := range list {
			k := key{ws, t}
			want[k] = append(want[k], p)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	var errs []error
	for k, rels := range want {
		if !names.MatchString(k.ws) || !names.MatchString(k.table) {
			continue
		}
		d := dir(k.ws, k.table)
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			continue
		}
		var ok []string
		for _, rel := range rels {
			if _, err := attachRel(rel); err == nil {
				ok = append(ok, rel)
			}
		}
		if err := c.copyInto(k.ws, d, ok); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("%s/%s: %w", k.ws, k.table, err))
		}
	}
	return errors.Join(errs...)
}

// messageAttachments is waits.Attachments: the copy of the attachments of a
// message of the inbox into the table of the target of an `ask --from-message`.
type messageAttachments struct{ *attachmentCopies }

// Forward copies the attachments (paths of the main clone) into the table and
// returns the paths of the copies there: the same, below the worktree.
func (m messageAttachments) Forward(ctx context.Context, workspace, table string, paths []string) ([]string, error) {
	if err := m.deliver(ctx, workspace, table, paths); err != nil {
		return nil, err
	}
	return slices.Clone(paths), nil
}

// openAttachments is tables.Attachments: the attachments of the messages that
// open a table. A file the channel kept only in the message (Data without a file
// in the main clone) is written to the main clone first.
type openAttachments struct{ *attachmentCopies }

// Forward copies the attachments (paths relative to `.mestre/anexos/`).
func (o openAttachments) Forward(ctx context.Context, workspace, table string, files []sessionzero.File) error {
	rels := make([]string, 0, len(files))
	for _, f := range files {
		rel, err := attachRel(attachDir + f.Path)
		if err != nil {
			return err
		}
		src := filepath.Join(o.layout.Principal(workspace), filepath.FromSlash(rel))
		if info, err := safefile.Stat(src); (err != nil || !info.Exists) && len(f.Data) > 0 {
			if err := safefile.Write(src, f.Data, safefile.Options{Mode: attachMode}); err != nil {
				return fmt.Errorf("o anexo %s não foi guardado: %w", rel, err)
			}
		}
		rels = append(rels, rel)
	}
	return o.deliver(ctx, workspace, table, rels)
}

// towerAttachments is how the Citadel puts attachments in the worktrees of the
// Towers: the file to the main clone of the Tower by the replication of the
// files, then the order to copy it.
type towerAttachments struct {
	towerClient
	send func(ctx context.Context, machine string, ref replication.FileRef) error
}

// Ship implements attachTowers.
func (t towerAttachments) Ship(ctx context.Context, machine, workspace, table string, rels []string) error {
	for _, rel := range rels {
		if err := t.send(ctx, machine, replication.FileRef{Root: filesWorkspaces, Path: workspace + "/principal/" + rel}); err != nil {
			return err
		}
	}
	_, err := t.call(ctx, machine, towerTableWait, protocol.Outgoing{Type: protocol.TypeTableAttach, Payload: protocol.TableAttach{Workspace: workspace, Table: table, Paths: rels}})
	return err
}

// attachCites says which file a change of the attachments cites, for the
// replication to send it to the Towers before the row: the path is relative to
// the main clone of the workspace.
func attachCites(root string) replication.Cites {
	return func(c store.Change) []replication.FileRef {
		if c.Table != "attachments" || c.Op == store.OpDelete || len(c.New) < 15 {
			return nil
		}
		ws, _ := c.New[1].(string)
		p, _ := c.New[14].(string)
		if ws == "" || !names.MatchString(ws) {
			return nil
		}
		if _, err := attachRel(p); err != nil {
			return nil
		}
		return []replication.FileRef{{Root: root, Path: ws + "/principal/" + p}}
	}
}

// joinCites makes one Cites of several.
func joinCites(all ...replication.Cites) replication.Cites {
	return func(c store.Change) []replication.FileRef {
		var out []replication.FileRef
		for _, f := range all {
			if f != nil {
				out = append(out, f(c)...)
			}
		}
		return out
	}
}

// mirrorCites says which mirror a change of the database cites: a table whose
// row names a commit of the mirror of its workspace (the base, the head of the
// pull request, the merged head) when the row is new or one of them changed.
// The mirror then reaches the Tower before the row does. workspaces is the
// folder of the workspaces; a workspace with no mirror is not cited (the
// sending of a mirror that cannot be opened would hold the replication back).
func mirrorCites(workspaces string) func(c store.Change) []string {
	const base, head, merged = 6, 13, 15 // columns of tables: base_commit, pr_head, merged_head
	return func(c store.Change) []string {
		if c.Table != "tables" || c.Op == store.OpDelete || len(c.New) <= merged {
			return nil
		}
		moved := c.Op == store.OpInsert || len(c.Old) <= merged
		for _, i := range []int{base, head, merged} {
			if !moved && c.Old[i] != c.New[i] {
				moved = true
			}
		}
		ws, _ := c.New[0].(string)
		if !moved || !names.MatchString(ws) {
			return nil
		}
		if fi, err := os.Stat(filepath.Join(workspaces, ws, "espelho.git")); err != nil || !fi.IsDir() {
			return nil
		}
		return []string{ws}
	}
}

// attachSync runs the catch-up of a Tower one at a time and coalesces the calls
// that come while it runs into one more run.
type attachSync struct {
	mu      sync.Mutex
	running bool
	again   bool
}

// run calls f in the background, once at a time; a call during a run asks for
// another when it ends.
func (a *attachSync) run(f func()) {
	a.mu.Lock()
	if a.running {
		a.again = true
		a.mu.Unlock()
		return
	}
	a.running = true
	a.mu.Unlock()
	go func() {
		for {
			f()
			a.mu.Lock()
			if !a.again {
				a.running = false
				a.mu.Unlock()
				return
			}
			a.again = false
			a.mu.Unlock()
		}
	}()
}
