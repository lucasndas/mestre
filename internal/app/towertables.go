package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/replication"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The worktrees of the tables on the Towers (docs/orquestracao/mesas.md): the
// Citadel sends the tip of the branch of a table as a git bundle, asks where
// the Tower keeps the worktree, how it is, to remove it and to send back its
// commits after a `done`; the Tower makes the worktree and runs the session
// zero of the table in it (tables.Tower). Every answer travels in the ack.

const towerTableWait = 2 * time.Minute

// towerTables is how the Citadel of a period reaches the half of the tables of
// each Tower: tables.Peers for the tables and quests.Towers for the `done`.
type towerTables struct{ towerClient }

var _ tables.Peers = towerTables{}

// Connected implements tables.Peers.
func (t towerTables) Connected(machine string) bool {
	_, err := t.link(machine)
	return err == nil
}

// SendBundle implements tables.Peers.
func (t towerTables) SendBundle(ctx context.Context, machine string, meta protocol.Object, src protocol.BundleSource) error {
	l, err := t.link(machine)
	if err != nil {
		return err
	}
	return l.SendBundle(ctx, meta, src)
}

func (t towerTables) where(ctx context.Context, machine, workspace, table string) (protocol.TableWhere, error) {
	raw, err := t.call(ctx, machine, towerTableWait, protocol.Outgoing{Type: protocol.TypeTableWhere, Payload: protocol.TableRef{Workspace: workspace, Table: table}})
	if err != nil {
		return protocol.TableWhere{}, err
	}
	var w protocol.TableWhere
	if err := json.Unmarshal(raw, &w); err != nil || w.Dir == "" {
		return protocol.TableWhere{}, fmt.Errorf("%s answered table.where with %s", machine, raw)
	}
	return w, nil
}

// WorktreeDir implements tables.Peers.
func (t towerTables) WorktreeDir(ctx context.Context, machine, workspace, table string) (string, error) {
	w, err := t.where(ctx, machine, workspace, table)
	return w.Dir, err
}

// Inspect implements tables.Peers.
func (t towerTables) Inspect(ctx context.Context, machine, workspace, table string) (tables.Inspection, error) {
	w, err := t.where(ctx, machine, workspace, table)
	if err != nil {
		return tables.Inspection{}, err
	}
	if !w.Exists {
		return tables.Inspection{}, fmt.Errorf("%w: %s", tables.ErrNoWorktree, table)
	}
	return tables.Inspection{Tip: w.Tip, Dirty: w.Dirty}, nil
}

// RemoveWorktree implements tables.Peers.
func (t towerTables) RemoveWorktree(ctx context.Context, machine, workspace, table string) error {
	_, err := t.call(ctx, machine, towerTableWait, protocol.Outgoing{Type: protocol.TypeTableRemove, Payload: protocol.TableRef{Workspace: workspace, Table: table}})
	return err
}

// Done implements quests.Towers: the Tower sends the commits of the table that
// the Citadel does not have, and says what it could not. The ack says the
// Tower began; the end comes as table.sent, which the Tower cannot send from
// inside the handler: its connection has to be free to carry the transfer.
func (t towerTables) Done(ctx context.Context, machine, workspace, table string) (tables.DoneReport, error) {
	id := t.ids.NewString()
	ch, release := t.rep.expect(id, machine)
	defer release()
	if _, err := t.call(ctx, machine, towerCallWait, protocol.Outgoing{ID: id, Type: protocol.TypeTableDone, Payload: protocol.TableRef{Workspace: workspace, Table: table}}); err != nil {
		return tables.DoneReport{}, err
	}
	tm := t.clk.NewTimer(towerTableWait)
	defer tm.Stop()
	select {
	case v := <-ch:
		d, _ := v.(protocol.TableSent)
		if d.Workspace != workspace || d.Table != table {
			return tables.DoneReport{}, fmt.Errorf("%s answered for the table %s/%s, not %s/%s", machine, d.Workspace, d.Table, workspace, table)
		}
		if d.Error != "" {
			return tables.DoneReport{}, fmt.Errorf("%s: %s", machine, d.Error)
		}
		return tables.DoneReport{Sent: d.Sent, Tip: d.Tip, Warnings: d.Warnings}, nil
	case <-tm.C():
		return tables.DoneReport{}, fmt.Errorf("%s did not finish sending the table %s in time", machine, table)
	case <-ctx.Done():
		return tables.DoneReport{}, ctx.Err()
	}
}

// citadelBundles are the bundles the Citadel takes from a Tower: those of the
// tables, by the receiver of the machine the connection authenticated. The
// mirrors travel the other way only.
type citadelBundles struct {
	svc *atomic.Pointer[tables.Service]
}

var _ protocol.PeerFinalizer = citadelBundles{}

func (citadelBundles) Finalize(context.Context, protocol.XferBegin, string) error {
	return protocol.NewError(protocol.CodeUnavailable, "the bundles of a Tower are received for its machine")
}

// FinalizerFor implements protocol.PeerFinalizer.
func (c citadelBundles) FinalizerFor(machine string) protocol.Finalizer {
	svc := c.svc.Load()
	if svc == nil {
		return refusing{protocol.NewError(protocol.CodeUnavailable, "this Citadel hosts no tables")}
	}
	return tables.Dispatch(svc.ReceiverFor(machine), refusing{protocol.NewError(protocol.CodeForbidden, "a Tower sends no mirror")})
}

// refusing is a Finalizer that refuses with an error.
type refusing struct{ err *protocol.Error }

func (r refusing) Finalize(context.Context, protocol.XferBegin, string) error { return r.err }

// lateTower is the half of the tables of a Tower, which the connection can
// use only once the period made it.
type lateTower struct {
	p atomic.Pointer[tables.Tower]
	// sending keeps one sending of commits at a time: a done and the resend of
	// a connection that came back do not send the same bundle twice.
	sending sync.Mutex
}

// Done is Tower.Done, one at a time.
func (l *lateTower) Done(ctx context.Context, t *tables.Tower, workspace, table string) (tables.DoneReport, error) {
	l.sending.Lock()
	defer l.sending.Unlock()
	return t.Done(ctx, workspace, table)
}

// Resend is Tower.Resend, one at a time.
func (l *lateTower) Resend(ctx context.Context) error {
	t := l.p.Load()
	if t == nil {
		return nil
	}
	l.sending.Lock()
	defer l.sending.Unlock()
	return t.Resend(ctx)
}

func (l *lateTower) Finalize(ctx context.Context, b protocol.XferBegin, file string) error {
	t := l.p.Load()
	if t == nil {
		return protocol.NewError(protocol.CodeUnavailable, "the tables of this machine are not ready")
	}
	// the names of the meta make the path of the worktree: plain names, as in the commands
	if m, err := tables.MetaOf(b.Meta); err != nil || !names.MatchString(m.Workspace) || !names.MatchString(m.Table) {
		return protocol.NewError(protocol.CodeInvalidPayload, "the workspace and the table of a bundle are plain names")
	}
	return t.Finalize(ctx, b, file)
}

// towerFinalizers are the receivers of the transfers of a Tower: the
// replication's, with the bundles of git told apart between the tables of the
// Citadel and the mirrors.
func towerFinalizers(base map[string]protocol.Finalizer, tower *lateTower) map[string]protocol.Finalizer {
	mirrors, ok := base[protocol.KindGitBundle]
	if !ok {
		mirrors = refusing{protocol.NewError(protocol.CodeUnavailable, "this machine keeps no mirrors")}
	}
	base[protocol.KindGitBundle] = tables.Dispatch(tower, mirrors)
	return base
}

// names are the names of a workspace and of a table in the path of a worktree:
// nothing that leaves the folder.
var names = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,100}$`)

// towerWorktreeDir is where this machine keeps the worktree of a table.
func (e *Engine) towerWorktreeDir(workspace, table string) string {
	return filepath.Join(e.paths.Workspaces, workspace, "mesas", table)
}

// newTowerTables makes the half of the tables of this Tower over its link to
// the Citadel.
func (e *Engine) newTowerTables(link *protocol.Link) (*tables.Tower, error) {
	if e.paths.Workspaces == "" {
		return nil, errors.New("this machine keeps no folder of workspaces")
	}
	masker := e.deps.Masker
	if masker == nil {
		masker = logging.NewMasker()
	}
	git, err := gitx.New(gitx.Options{Clock: e.clk, Masker: masker, Suite: os.Getenv("MESTRE_TEST_SUITE")})
	if err != nil {
		return nil, err
	}
	binary := e.deps.Binary
	if binary == "" {
		if binary, err = os.Executable(); err != nil {
			return nil, err
		}
	}
	files, err := sessionzero.NewTable(sessionzero.TableOptions{Worktree: sessionzero.Worktree{Providers: towerProviders(), Binary: binary}})
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(e.paths.Workspaces)
	if err != nil {
		return nil, err
	}
	mirrors := replication.MirrorStore{Root: root, Git: git}
	return tables.NewTower(tables.TowerOptions{
		DB: e.mdb, Git: git, Clock: e.clk, Mirror: mirrors.Open, Files: files, Log: e.log,
		WorktreeDir: func(workspace, table string) string { return e.towerWorktreeDir(workspace, table) },
		Send: func(ctx context.Context, meta protocol.Object, src protocol.BundleSource) error {
			return link.SendBundle(ctx, meta, src)
		},
		Tell: func(ctx context.Context, workspace, table, text string) {
			if _, err := link.Send(ctx, protocol.Outgoing{Type: protocol.TypeTableNotice, Payload: protocol.TableNotice{Workspace: workspace, Table: table, Text: text}}); err != nil {
				e.log.Warn("app: the master of the table was not told", "table", table, "err", err)
			}
		},
		TempDir: filepath.Join(e.paths.Maquina, "tmp"),
	})
}

// registerTowerTables puts in the router of a Tower the handlers of the
// commands of the Citadel for the worktrees of the tables.
func (e *Engine) registerTowerTables(router *protocol.Router, half *lateTower) {
	guard := func(typ string, f func(ctx context.Context, t *tables.Tower, ref protocol.TableRef, m *protocol.Message) (*protocol.Reply, error)) {
		router.HandleFunc(typ, func(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
			if !e.fromCitadel(m.Peer) {
				return nil, protocol.NewError(protocol.CodeForbidden, "%s: only the Citadel gives this command", typ)
			}
			ref, ok := m.Payload.(*protocol.TableRef)
			if !ok || !names.MatchString(ref.Workspace) || !names.MatchString(ref.Table) {
				return nil, protocol.NewError(protocol.CodeInvalidPayload, "%s: the workspace and the table are plain names", typ)
			}
			t := half.p.Load()
			if t == nil {
				return nil, protocol.NewError(protocol.CodeUnavailable, "%s: the tables of this machine are not ready", typ)
			}
			return f(ctx, t, *ref, m)
		})
	}
	fail := func(op string, err error) error {
		return protocol.NewError(protocol.CodeMissingPrerequisite, "%s: %v", op, err)
	}
	guard(protocol.TypeTableWhere, func(ctx context.Context, t *tables.Tower, ref protocol.TableRef, _ *protocol.Message) (*protocol.Reply, error) {
		w := protocol.TableWhere{Dir: e.towerWorktreeDir(ref.Workspace, ref.Table)}
		in, err := t.Inspect(ctx, ref.Workspace, ref.Table)
		switch {
		case errors.Is(err, tables.ErrNoWorktree):
		case err != nil:
			return nil, fail("table.where", err)
		default:
			w.Exists, w.Tip, w.Dirty = true, in.Tip, in.Dirty
		}
		return &protocol.Reply{Type: protocol.TypeTableWhere, Payload: w, InAck: true}, nil
	})
	guard(protocol.TypeTableRemove, func(ctx context.Context, t *tables.Tower, ref protocol.TableRef, _ *protocol.Message) (*protocol.Reply, error) {
		if err := t.RemoveWorktree(ctx, ref.Workspace, ref.Table); err != nil {
			return nil, fail("table.remove", err)
		}
		return nil, nil
	})
	guard(protocol.TypeTableDone, func(ctx context.Context, t *tables.Tower, ref protocol.TableRef, m *protocol.Message) (*protocol.Reply, error) {
		link := m.Link
		id := m.Env.ID
		// The ack goes now and the sending runs on its own: the transfer needs
		// this connection free to carry xfer.ready and xfer.done.
		go func() {
			ctx, cancel := clock.WithTimeout(context.Background(), e.clk, towerTableWait-10*time.Second)
			defer cancel()
			out := protocol.TableSent{Workspace: ref.Workspace, Table: ref.Table}
			rep, err := half.Done(ctx, t, ref.Workspace, ref.Table)
			if err != nil {
				out.Error = err.Error()
			} else {
				out.Sent, out.Tip, out.Warnings = rep.Sent, rep.Tip, rep.Warnings
			}
			// the end is told even when the sending used the time it had
			sctx, scancel := clock.WithTimeout(context.Background(), e.clk, 10*time.Second)
			defer scancel()
			if _, err := link.Send(sctx, protocol.Outgoing{Type: protocol.TypeTableSent, Payload: out, ReplyTo: id}); err != nil {
				e.log.Warn("app: the end of a table.done was not sent", "table", ref.Table, "err", err)
			}
		}()
		return nil, nil
	})
}

// towerProviders are the adapters of the providers the session zero of the
// tables of a Tower writes the files of.
func towerProviders() *providers.Registry { return providers.NewRegistry(providers.Options{}) }

// handleTableNotice is table.notice on the Citadel: a Tower tells the master of
// a table something about its worktree, and only a Tower where the table has
// a living character may.
func (e *Engine) handleTableNotice(ctx context.Context, orc *Orchestration, m *protocol.Message) (*protocol.Reply, error) {
	p, ok := m.Payload.(*protocol.TableNotice)
	if !ok {
		return nil, protocol.NewError(protocol.CodeInvalidPayload, "table.notice: %T", m.Payload)
	}
	all, err := orc.Registry.OnMachine(ctx, m.Peer.ID)
	if err != nil {
		return nil, protocol.NewError(protocol.CodeInternal, "table.notice: %v", err)
	}
	for _, c := range all {
		if c.Workspace == p.Workspace && c.Table == p.Table && c.State.Alive() {
			err := orc.Inbox.Notify(ctx, characters.Notice{Kind: characters.NoticeSystem, To: characters.Recipient{Kind: characters.ToTableMaster},
				Workspace: p.Workspace, Table: p.Table, Text: p.Text})
			if err != nil {
				return nil, protocol.NewError(protocol.CodeInternal, "table.notice: %v", err)
			}
			return nil, nil
		}
	}
	return nil, protocol.NewError(protocol.CodeForbidden, "table.notice: %s has no character in the table %s", m.Peer.ID, p.Table)
}
