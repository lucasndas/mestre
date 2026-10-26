package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/internal/sessionzero"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// shipped is the Citadel reaching a Tower: it records what it was asked to ship.
type shipped struct {
	mu   sync.Mutex
	got  []string
	err  error
	made func(machine string)
	// onShip is what the Tower does with the order: the paths it was asked for.
	onShip func(table string, rels []string)
}

func (s *shipped) Ship(_ context.Context, machine, workspace, table string, rels []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	for _, r := range rels {
		s.got = append(s.got, machine+" "+workspace+"/"+table+" "+r)
	}
	if s.made != nil {
		s.made(machine)
	}
	if s.onShip != nil {
		s.onShip(table, rels)
	}
	return nil
}

// attachRig is a workspace with one table that has a worktree on the Citadel and
// another on a Tower.
type attachRig struct {
	db       *store.DB
	root     string
	copies   *attachmentCopies
	citadel  string // the worktree of the Citadel
	towerDir string // the worktree of the Tower, on the same disk
	ship     *shipped
}

const (
	attWS    = "meu-app"
	attTable = "3-login"
	attRel   = ".mestre/anexos/2026/10/a1b2-print.png"
)

func newAttachRig(t *testing.T) *attachRig {
	t.Helper()
	ctx := t.Context()
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	db, err := guildtpl.Open(ctx, filepath.Join(root, "mestre.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES (?, 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, attWS); err != nil {
		t.Fatal(err)
	}
	r := &attachRig{db: db, root: filepath.Join(root, "workspaces"), ship: &shipped{}}
	r.citadel = filepath.Join(r.root, attWS, "mesas", attTable)
	r.towerDir = filepath.Join(root, "torre", attWS, "mesas", attTable)
	for _, d := range []string{r.citadel, r.towerDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	now := clock.Real{}.Now()
	st := tables.Store{DB: db}
	if err := st.Insert(ctx, tables.Table{Workspace: attWS, ID: attTable, Epic: 3, EpicTitle: "Login", Branch: "mestre/3-login", BaseBranch: "main", BaseCommit: "abc",
		State: tables.StateWorking, OpenedBy: "engine", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for machine, dir := range map[string]string{"cidadela": r.citadel, "torre": r.towerDir} {
		if err := st.PutWorktree(ctx, tables.Worktree{Workspace: attWS, Table: attTable, Machine: machine, Dir: dir, LastCommit: "abc", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	r.copies = &attachmentCopies{db: db, tables: st, layout: workspaces.Layout{Root: r.root}, machine: "cidadela", towers: r.ship, log: slog.New(slog.DiscardHandler)}
	return r
}

// keep puts an attachment in the main clone and its row in the database.
func (r *attachRig) keep(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(r.root, attWS, "principal", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Writer().ExecContext(t.Context(), `INSERT INTO attachments (id, workspace, user_id, channel, name_original, mime_detected, size, sha256, path, created_at)
		VALUES (?, ?, 'u1', 'telegram', 'print.png', 'image/png', ?, ?, ?, 1)`, rel, attWS, len(content), fmt.Sprintf("%x", sha256.Sum256([]byte(content))), rel); err != nil {
		t.Fatal(err)
	}
}

// towerCopies makes the fake Tower do what the real one does when the Citadel
// asks (table.attach): copy the attachments of the order into its own worktree.
func (r *attachRig) towerCopies(t *testing.T) {
	t.Helper()
	r.ship.onShip = func(_ string, rels []string) {
		if err := r.copies.copyInto(t.Context(), attWS, r.towerDir, rels); err != nil {
			t.Errorf("the Tower did not copy: %v", err)
		}
	}
}

func (r *attachRig) recorded(t *testing.T, rel string) []string {
	t.Helper()
	var raw string
	if err := r.db.Reader().QueryRowContext(t.Context(), `SELECT tables FROM attachments WHERE path = ?`, rel).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatal(err)
	}
	return list
}

// CA-ANEXO-07: a message that cites an attachment is delivered to the master of
// the table only after the file is in every worktree of the table: the one of
// the Citadel is written and the one of the Tower is asked for, and the row of
// the attachment says to which table it went.
func TestTheAttachmentIsInEveryWorktreeOfTheTableBeforeTheMessageIsDelivered(t *testing.T) {
	t.Parallel()
	r := newAttachRig(t)
	r.towerCopies(t)
	r.keep(t, attRel, "PNG")
	copies, err := messageAttachments{r.copies}.Forward(t.Context(), attWS, attTable, []string{attRel})
	if err != nil || !slices.Equal(copies, []string{attRel}) {
		t.Fatalf("Forward = %v, %v", copies, err)
	}
	if b, err := os.ReadFile(filepath.Join(r.citadel, filepath.FromSlash(attRel))); err != nil || string(b) != "PNG" {
		t.Errorf("the worktree of the Citadel: %q, %v", b, err)
	}
	if want := []string{"torre " + attWS + "/" + attTable + " " + attRel}; !slices.Equal(r.ship.got, want) {
		t.Errorf("the Tower was asked for %v, want %v", r.ship.got, want)
	}
	// and the file is in the worktree of the Tower by the time Forward returns
	if b, err := os.ReadFile(filepath.Join(r.towerDir, filepath.FromSlash(attRel))); err != nil || string(b) != "PNG" {
		t.Errorf("the worktree of the Tower: %q, %v", b, err)
	}
	if got := r.recorded(t, attRel); !slices.Equal(got, []string{attTable}) {
		t.Errorf("the tables of the attachment = %v", got)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(filepath.Join(r.citadel, filepath.FromSlash(attRel))); err != nil || fi.Mode().Perm()&0o111 != 0 {
			t.Errorf("a copy of an attachment is not executable: %v, %v", fi, err)
		}
	}
	// a Tower that does not answer fails the delivery (the message waits), and the
	// row keeps the table so the Tower gets the file when it is back
	r2 := newAttachRig(t)
	r2.keep(t, attRel, "PNG")
	r2.ship.err = errors.New("the Tower is away")
	if _, err := (messageAttachments{r2.copies}).Forward(t.Context(), attWS, attTable, []string{attRel}); err == nil {
		t.Error("a Tower that is away did not fail the copy")
	}
	if got := r2.recorded(t, attRel); !slices.Equal(got, []string{attTable}) {
		t.Errorf("the tables of the attachment after the failure = %v", got)
	}
	// what is not an attachment of the main clone is not copied
	for _, bad := range []string{"../x", ".mestre/anexos/../../x", "README.md", ".mestre/anexos/", ".mestre/anexos/a\\b", ".mestre/anexos//a", ".mestre/anexos/a:b", ".mestre/anexos/a\x01"} {
		if _, err := (messageAttachments{r.copies}).Forward(t.Context(), attWS, attTable, []string{bad}); err == nil {
			t.Errorf("%q was copied", bad)
		}
	}
	// a table with no worktree has nowhere to put it
	if _, err := (messageAttachments{r.copies}).Forward(t.Context(), attWS, "9-nada", []string{attRel}); err == nil {
		t.Error("a table with no worktree took an attachment")
	}
}

// CA-ANEXO-07: the table that opens with a message gets the attachments of the
// message; a file the channel kept only in the message is put in the main clone.
func TestTheTableThatOpensWithAMessageGetsItsAttachments(t *testing.T) {
	t.Parallel()
	r := newAttachRig(t)
	r.towerCopies(t)
	r.keep(t, attRel, "PNG")
	err := openAttachments{r.copies}.Forward(t.Context(), attWS, attTable, []sessionzero.File{
		{Path: "2026/10/a1b2-print.png"}, {Path: "2026/10/c3d4-nota.txt", Data: []byte("nota")}})
	if err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{attRel: "PNG", ".mestre/anexos/2026/10/c3d4-nota.txt": "nota"} {
		for _, dir := range []string{r.citadel, r.towerDir, filepath.Join(r.root, attWS, "principal")} {
			if b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))); err != nil || string(b) != want {
				t.Errorf("%s in %s: %q, %v", rel, dir, b, err)
			}
		}
	}
	if len(r.ship.got) != 2 {
		t.Errorf("the Tower was asked for %v", r.ship.got)
	}
	if err := (openAttachments{r.copies}).Forward(t.Context(), attWS, attTable, []sessionzero.File{{Path: "../../etc/passwd", Data: []byte("x")}}); err == nil {
		t.Error("a path out of the folder of the attachments was kept")
	}
	// the session zero of a worktree made later finds them by the rows
	got, err := r.copies.For(t.Context(), attWS, attTable)
	if err != nil || len(got) != 1 || got[0].Path != "2026/10/a1b2-print.png" || string(got[0].Data) != "PNG" {
		t.Errorf("For = %+v, %v", got, err)
	}
	if got, _ := r.copies.For(t.Context(), attWS, "9-nada"); len(got) != 0 {
		t.Errorf("another table got %+v", got)
	}
}

// What a Tower does with the rows the replica brings: the attachments that name a
// table of its own are copied into its worktree, once; a table it has no
// worktree of gets nothing, and a link planted in the worktree takes nothing out.
func TestTheTowerCopiesTheAttachmentsOfItsTablesFromTheRows(t *testing.T) {
	t.Parallel()
	r := newAttachRig(t)
	r.keep(t, attRel, "PNG")
	if err := r.copies.mark(t.Context(), attWS, attTable, []string{attRel}); err != nil {
		t.Fatal(err)
	}
	if err := r.copies.mark(t.Context(), attWS, "9-sem-worktree", []string{attRel}); err != nil {
		t.Fatal(err)
	}
	dir := func(ws, table string) string { return filepath.Join(filepath.Dir(r.towerDir), table) }
	if err := r.copies.catchUp(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(r.towerDir, filepath.FromSlash(attRel))); err != nil || string(b) != "PNG" {
		t.Errorf("the worktree of the Tower: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(r.towerDir), "9-sem-worktree")); err == nil {
		t.Error("a worktree was made for a table the Tower has no part in")
	}
	if err := r.copies.catchUp(t.Context(), dir); err != nil { // idempotent
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	outside := t.TempDir()
	r.keep(t, ".mestre/anexos/2026/10/e5f6-x.pdf", "PDF")
	if err := r.copies.mark(t.Context(), attWS, attTable, []string{".mestre/anexos/2026/10/e5f6-x.pdf"}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(r.towerDir, ".mestre")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.towerDir, ".mestre")); err != nil {
		t.Skip("no links here:", err)
	}
	if err := r.copies.catchUp(t.Context(), dir); err == nil {
		t.Error("the copy went through a link planted in the worktree")
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Errorf("a file reached the folder the link points to: %v", ents)
	}
}

// The rows that cite a file or a mirror: the file or the mirror reaches the Tower
// before the row; the others cite nothing.
func TestTheRowsThatCiteAnAttachmentOrAMirror(t *testing.T) {
	t.Parallel()
	row := func(path string) []any {
		v := make([]any, 16)
		v[1], v[14] = attWS, path
		return v
	}
	c := attachCites("workspaces")
	if got := c(store.Change{Table: "attachments", Op: store.OpInsert, New: row(attRel)}); len(got) != 1 || got[0].Root != "workspaces" || got[0].Path != attWS+"/principal/"+attRel {
		t.Errorf("an attachment cites %+v", got)
	}
	for _, ch := range []store.Change{
		{Table: "attachments", Op: store.OpInsert, New: row("../../x")},
		{Table: "attachments", Op: store.OpInsert, New: row("README.md")},
		{Table: "attachments", Op: store.OpDelete, Old: row(attRel)},
		{Table: "notes", Op: store.OpInsert, New: row(attRel)},
	} {
		if got := c(ch); len(got) != 0 {
			t.Errorf("%+v cites %+v", ch, got)
		}
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, attWS, "espelho.git"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := mirrorCites(dir)
	tbl := func(base, head, merged string) []any {
		v := make([]any, 18)
		v[0], v[6], v[13], v[15] = attWS, base, head, merged
		return v
	}
	if got := m(store.Change{Table: "tables", Op: store.OpInsert, New: tbl("a", "", "")}); !slices.Equal(got, []string{attWS}) {
		t.Errorf("a new table cites %v", got)
	}
	if got := m(store.Change{Table: "tables", Op: store.OpUpdate, Old: tbl("a", "", ""), New: tbl("a", "", "c")}); !slices.Equal(got, []string{attWS}) {
		t.Errorf("a merged head that moved cites %v", got)
	}
	if got := m(store.Change{Table: "tables", Op: store.OpUpdate, Old: tbl("a", "", ""), New: tbl("a", "b", "")}); len(got) != 0 {
		t.Errorf("the head of a pull request cites %v", got)
	}
	if got := m(store.Change{Table: "tables", Op: store.OpUpdate, Old: tbl("a", "b", ""), New: tbl("a", "b", "")}); len(got) != 0 {
		t.Errorf("a row whose commits did not change cites %v", got)
	}
	other := tbl("a", "", "")
	other[0] = "sem-espelho"
	for _, ch := range []store.Change{
		{Table: "tables", Op: store.OpInsert, New: other},
		{Table: "tables", Op: store.OpDelete, Old: tbl("a", "", "")},
		{Table: "quests", Op: store.OpInsert, New: tbl("a", "", "")},
	} {
		if got := m(ch); len(got) != 0 {
			t.Errorf("%+v cites %v", ch, got)
		}
	}
}
