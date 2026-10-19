package scrolls_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"modernc.org/sqlite"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
)

// downTables is a Guild where the machine "m2" does not answer.
type downTables struct{ fakeTables }

func (d downTables) Worktree(ctx context.Context, ws, tb, machine string) (string, error) {
	if machine == "m2" {
		return "", &characters.MachineUnreachableError{Machine: machine}
	}
	return d.fakeTables.Worktree(ctx, ws, tb, machine)
}

// A Tower that does not answer is skipped: the copies of the other machines
// are still made.
// CA-PERG-09
func TestATowerThatDoesNotAnswerDoesNotStopTheCopiesOfTheOthers(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOptions{
		copies: scrolls.LocalCopies{Machine: "m1"},
		targets: func(w *world) scrolls.Targets {
			return scrolls.CharacterTargets{Registry: w.reg, Tables: downTables{fakeTables{root: filepath.Join(w.root, "wt")}}}
		},
	})
	dev := w.dev("Dev")
	far := characters.Character{ID: "far1", Name: "Longe", Kind: characters.KindCharacter, Role: characters.RoleCharacter, Workspace: workspace, Table: table,
		Machine: "m2", Sheet: "Dev Claude", Base: "claude-code", Class: "Dev", State: characters.Idle, Lifecycle: characters.Persistent,
		CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, CreatedAt: w.clk.Now()}
	if err := w.reg.Insert(w.ctx, far); err != nil {
		t.Fatal(err)
	}
	n := w.create(dev, "# Plano\numa")
	if b, err := os.ReadFile(filepath.Join(w.root, "wt", "m1", n.Table, filepath.FromSlash(scrolls.Rel(n.Table, n.ID)))); err != nil || string(b) != "# Plano\numa" {
		t.Fatalf("the copy of m1 = %q, %v", b, err)
	}
}

// A copy goes out with the lock of the service released: a Tower is reached
// over the network, and a slow one must not stop the other scrolls.
type reentrantCopies struct {
	w    *world
	once sync.Once
	read chan struct{}
}

func (r *reentrantCopies) Put(_ context.Context, _ scrolls.Target, _ string, _ []byte) error {
	r.once.Do(func() {
		go func() {
			_, _, _ = r.w.read(user(characters.LevelOwner), "Plano")
			close(r.read)
		}()
		// With the lock held this never returns, and the test ends by the
		// timeout of `go test`; there is no clock to count on here.
		<-r.read
	})
	return nil
}

func (r *reentrantCopies) Remove(context.Context, scrolls.Target, string) error { return nil }

func TestTheCopiesGoOutWithTheLockOfTheServiceReleased(t *testing.T) {
	t.Parallel()
	rc := &reentrantCopies{read: make(chan struct{})}
	w := newWorld(t, worldOptions{
		copies: rc,
		targets: func(w *world) scrolls.Targets {
			rc.w = w
			return scrolls.CharacterTargets{Registry: w.reg, Tables: fakeTables{root: filepath.Join(w.root, "wt")}}
		},
	})
	dev := w.dev("Dev")
	w.create(dev, "# Plano\numa") // the copy reads the service while it is sent
	select {
	case <-rc.read:
	default:
		t.Fatal("the read of the copy did not end")
	}
}

// fileFirst keeps, for each test, the guard of the order: the row of the index
// may be written only when the file it cites is already there, with the size
// the row says. The trigger names its test by a token.
var fileFirst sync.Map // token -> func(ws, table, id string) (size int64, ok bool)

func init() {
	sqlite.MustRegisterScalarFunction("test_scroll_file_ready", 5, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		f, ok := fileFirst.Load(args[0].(string))
		if !ok {
			return int64(1), nil
		}
		size, there := f.(func(ws, table, id string) (int64, bool))(args[1].(string), args[2].(string), args[3].(string))
		if there && size == args[4].(int64) {
			return int64(1), nil
		}
		return int64(0), nil
	})
}

func TestTheFileOfAScrollIsWrittenBeforeItsRowInTheIndex(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOptions{})
	token := w.dbPath
	raw, err := sql.Open("sqlite", w.dbPath) // the schema of the replicated database is closed to its own writer
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for _, ev := range []string{"INSERT", "UPDATE"} {
		if _, err := raw.Exec(`CREATE TRIGGER file_first_` + ev + ` BEFORE ` + ev + ` ON notes
			WHEN test_scroll_file_ready('` + token + `', NEW.workspace, NEW.table_id, NEW.id, NEW.size) = 0
			BEGIN SELECT RAISE(ABORT, 'the row came before the file'); END`); err != nil {
			t.Fatal(err)
		}
	}
	fileFirst.Store(token, func(ws, tb, id string) (int64, bool) {
		st, err := os.Stat(w.svc.File(ws, tb, id))
		if err != nil {
			return 0, false
		}
		return st.Size(), true
	})
	t.Cleanup(func() { fileFirst.Delete(token) })

	dev := w.dev("Dev")
	n := w.create(dev, "# Plano\nversão 1") // fails the test if the row came first
	if _, err := w.write(dev, n.Name, "# Plano\nversão 2, maior", nil); err != nil {
		t.Fatalf("the change: %v", err)
	}
}
