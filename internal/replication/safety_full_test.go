//go:build full

package replication_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/replication"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/vault"
)

// Cases of the replication that the fast suite leaves to the full one: each
// costs a Citadel, a Tower and their files, which on a Windows runner is where
// the 30 s of the fast suite go.

// Part of CA-REPL-04
// What an excluded machine does not get, it does not keep: the folders marked
// so are emptied with the vault, and the replica stays the same otherwise.
func TestVaultDropAlsoErasesTheFoldersThatAreNotForAnExcludedMachine(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, rigOpts{vault: true, files: true})
		ctx := context.Background()
		for dir, name := range map[string]string{r.filesDir: "class.md", r.capturesDir: "shot.png.enc"} {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o640); err != nil {
				t.Fatal(err)
			}
		}
		tw := r.addTower("t1", true)
		cp := &vault.Copier{Keys: tw.keys, EnvelopePath: tw.env}
		exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
		shot, class := filepath.Join(tw.captures, "shot.png.enc"), filepath.Join(tw.files, "class.md")
		eventually(t, "both folders", func() bool { return exists(shot) && exists(class) && tw.lsn() == r.lsn() })

		r.roster.set("t1", true)
		r.primary.ExclusionChanged(guild.Machine{ID: "t1", Excluded: true})
		eventually(t, "the captures to be erased", func() bool { return !exists(shot) && !cp.Holds(ctx) })
		if !exists(class) {
			t.Error("the folder that an excluded machine gets was erased")
		}
		// And what the Citadel sends next does not bring them back.
		if err := os.WriteFile(filepath.Join(r.capturesDir, "later.png.enc"), []byte("later"), 0o640); err != nil {
			t.Fatal(err)
		}
		r.primary.FileChanged("captures", "later.png.enc")
		settle()
		advance(time.Second)
		if exists(filepath.Join(tw.captures, "later.png.enc")) {
			t.Error("a capture reached an excluded machine")
		}
	})
}

// CA-REPL-08
// A file never leaves its folder, on the Citadel that sends it or on the Tower
// that takes it, through a link in the middle of the path; and a path that the
// replication refuses does not stop the replication of the rest.
func TestAFileNeverLeavesItsFolderThroughALinkInTheMiddleOfThePath(t *testing.T) {
	t.Parallel()
	fileEscapeScenario(t, os.Symlink)
}

// fileEscapeScenario is the test above for a kind of link: link(target, name)
// makes name a link to the folder target (a symbolic link; on Windows also a
// junction, which Go does not take for a symbolic link).
func fileEscapeScenario(t *testing.T, link func(target, name string) error) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		cites := func(c store.Change) []replication.FileRef {
			if c.Table == "machines" && c.Op == store.OpInsert && strings.HasSuffix(c.New[1].(string), ".md") {
				return []replication.FileRef{{Root: "classes", Path: c.New[1].(string)}}
			}
			return nil
		}
		r := newRig(t, rigOpts{files: true, cites: cites})
		write := func(p, body string) {
			t.Helper()
			if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
				t.Fatal(err)
			}
		}
		// The Citadel has a folder that is really a link to somewhere else.
		secretDir := filepath.Join(r.dir, "private")
		write(filepath.Join(secretDir, "secret.md"), "do not send")
		write(filepath.Join(r.filesDir, "ok.md"), "ok")
		if err := link(secretDir, filepath.Join(r.filesDir, "leak")); err != nil {
			t.Skipf("this kind of link is not available here: %v", err)
		}
		// The Tower has a folder that is really a link to somewhere else.
		tw := r.addTower("t1", true)
		outside := filepath.Join(r.dir, "outside")
		if err := os.MkdirAll(outside, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(tw.files, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := link(outside, filepath.Join(tw.files, "sub")); err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(r.filesDir, "sub", "evil.md"), "evil")
		r.exec(`INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, joined_at) VALUES ('m1', 'leak/secret.md', '', 0, 0, 'active', '{}', 0)`)
		r.exec(`INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, joined_at) VALUES ('m2', 'sub/evil.md', '', 0, 0, 'active', '{}', 0)`)
		r.exec(`INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, joined_at) VALUES ('m3', 'ok.md', '', 0, 0, 'active', '{}', 0)`)
		last := r.lsn()
		r.primary.FileChanged("classes", "leak/secret.md")
		r.primary.FileChanged("classes", "sub/evil.md")

		// The rest goes on: the records apply and the plain file arrives.
		eventually(t, "the records", func() bool { return tw.lsn() == last })
		eventually(t, "ok.md", func() bool {
			b, _ := os.ReadFile(filepath.Join(tw.files, "ok.md"))
			return string(b) == "ok"
		})
		advance(time.Minute)
		if got := tw.snapshots.Load(); got != 1 {
			t.Errorf("snapshots = %d: the refused paths made the replication start over", got)
		}
		// Nothing outside the folder, and nothing of the Citadel's private folder anywhere.
		if entries, _ := os.ReadDir(outside); len(entries) != 0 {
			t.Errorf("a file went through the link on the Tower: %v", entries)
		}
		var found []string
		_ = filepath.WalkDir(tw.files, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && d.Type().IsRegular() {
				if b, _ := os.ReadFile(p); strings.Contains(string(b), "do not send") {
					found = append(found, p)
				}
			}
			return nil
		})
		if len(found) != 0 {
			t.Errorf("the private file of the Citadel reached the Tower: %v", found)
		}
		equal(t, r, tw)
	})
}
