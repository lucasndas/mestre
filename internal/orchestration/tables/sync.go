package tables

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/privdir"
	"github.com/lucasndas/mestre/internal/protocol"
)

// The branches of the tables travel between the Citadel and the Towers as git
// bundles over the connection of the protocol (docs/decisoes/0013 and
// docs/orquestracao/mesas.md#worktree-por-máquina-e-sincronização). Only
// commits travel; integration is always a fast-forward, and the engine never
// merges by itself: what does not fast-forward stays as it is and the table
// master hears of it.

// kindTable is the value of meta.kind of a bundle of a table; the bundles of
// the mirrors (internal/replication) carry a `mirror` instead.
const kindTable = "table"

// BundleMeta is the meta of the transfer of a bundle of a table.
type BundleMeta struct {
	Kind      string `json:"kind"`
	Workspace string `json:"workspace"`
	Table     string `json:"table"`
	Branch    string `json:"branch"`
	// Tip is the commit the sender has at the tip of the branch.
	Tip string `json:"tip"`
	// Base is the prerequisite of an incremental bundle ("": the whole
	// branch).
	Base string `json:"base,omitempty"`
}

// Object is the meta as the protocol carries it.
func (m BundleMeta) Object() protocol.Object {
	m.Kind = kindTable
	raw, _ := json.Marshal(m)
	var o protocol.Object
	_ = json.Unmarshal(raw, &o)
	return o
}

// IsTableBundle says whether the meta of a transfer is the one of a bundle of
// a table, not of a mirror.
func IsTableBundle(o protocol.Object) bool {
	raw, ok := o["kind"]
	if !ok {
		return false
	}
	var k string
	return json.Unmarshal(raw, &k) == nil && k == kindTable
}

// MetaOf reads the meta of a bundle of a table.
func MetaOf(o protocol.Object) (BundleMeta, error) {
	raw, err := json.Marshal(o)
	if err != nil {
		return BundleMeta{}, err
	}
	var m BundleMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return BundleMeta{}, err
	}
	switch {
	case m.Kind != kindTable:
		return BundleMeta{}, errors.New("not the bundle of a table")
	case m.Workspace == "" || m.Table == "" || !validBranch(m.Branch) || m.Tip == "":
		return BundleMeta{}, errors.New("the meta of the bundle lacks the workspace, the table, the branch or the tip")
	}
	return m, nil
}

// Dispatch is the Finalizer of the transfers of kind git-bundle on a
// connection: the bundles of the tables go to table, the ones of the mirrors
// (internal/replication) to mirrors. Both implement protocol.Skipper when the
// receiver does.
func Dispatch(table, mirrors protocol.Finalizer) protocol.Finalizer {
	return dispatcher{table: table, mirrors: mirrors}
}

type dispatcher struct{ table, mirrors protocol.Finalizer }

func (d dispatcher) pick(b protocol.XferBegin) protocol.Finalizer {
	if IsTableBundle(b.Meta) {
		return d.table
	}
	return d.mirrors
}

func (d dispatcher) Finalize(ctx context.Context, b protocol.XferBegin, file string) error {
	return d.pick(b).Finalize(ctx, b, file)
}

// Admit forwards the question to the receiver that would finalize the transfer.
func (d dispatcher) Admit(ctx context.Context, b protocol.XferBegin) error {
	if a, ok := d.pick(b).(protocol.Admitter); ok {
		return a.Admit(ctx, b)
	}
	return nil
}

func (d dispatcher) Have(ctx context.Context, b protocol.XferBegin) bool {
	if sk, ok := d.pick(b).(protocol.Skipper); ok {
		return sk.Have(ctx, b)
	}
	return false
}

// maxBundleAttempts is how many times a bundle whose hash did not match is
// sent again before the table master is told.
const maxBundleAttempts = 3

// SendBundleWithRetry sends the bundle again when the receiver found its hash
// invalid, up to three times; after that it gives the error back and the
// caller tells the table master (docs/orquestracao/mesas.md#regras-e-erros).
func SendBundleWithRetry(ctx context.Context, send func(context.Context) error) error {
	var err error
	for range maxBundleAttempts {
		if err = send(ctx); err == nil {
			return nil
		}
		var pe *protocol.Error
		if !errors.As(err, &pe) || pe.Code != protocol.CodeHashMismatch {
			return err
		}
	}
	return fmt.Errorf("the bundle was refused %d times: %w", maxBundleAttempts, err)
}

// bundleSources builds the sources of the bundles of a branch: the
// incremental one from base and the whole branch. The files are written in dir
// and removed when the source is released.
func bundleSources(repo gitx.Repo, dir, branch, base string) protocol.BundleSource {
	open := func(rev string) protocol.OpenSource {
		return func(ctx context.Context) (protocol.Source, func(), error) {
			f, err := os.CreateTemp(dir, "bundle-*.pack")
			if err != nil {
				return protocol.Source{}, nil, err
			}
			name := f.Name()
			_ = f.Close()
			cleanup := func() { _ = os.Remove(name) }
			if err := repo.CreateBundle(ctx, name, rev); err != nil {
				cleanup()
				return protocol.Source{}, nil, err
			}
			rf, err := os.Open(name)
			if err != nil {
				cleanup()
				return protocol.Source{}, nil, err
			}
			st, err := rf.Stat()
			if err != nil {
				_ = rf.Close()
				cleanup()
				return protocol.Source{}, nil, err
			}
			return protocol.Source{ReaderAt: rf, Size: st.Size()}, func() { _ = rf.Close(); cleanup() }, nil
		}
	}
	src := protocol.BundleSource{Full: open(branch)}
	if base != "" {
		src.Incremental = open(base + ".." + branch)
	}
	return src
}

// privateTemp is the folder the bundles are written in while they are sent: the
// one given, or a folder of its own in the temporary one, private to the
// account of the engine (a bundle holds the code of the project).
func privateTemp(dir string) (string, error) {
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "mestre-bundles")
	}
	return dir, privdir.Ensure(dir)
}

// ensureTower makes the worktree of the table on a Tower, with a bundle of
// its branch, and records it.
func (s *Service) ensureTower(ctx context.Context, workspace, table, machine string) (string, error) {
	t, err := s.store.Get(ctx, workspace, table)
	if err != nil {
		return "", err
	}
	if !t.State.Accepts() {
		return "", fmt.Errorf("%w: a mesa %s está %s", ErrBadTransition, table, t.State)
	}
	if err := s.SyncTower(ctx, workspace, table, machine); err != nil {
		return "", err
	}
	if s.o.Peers == nil {
		return "", fmt.Errorf("%w: %s", ErrMachineUnreachable, machine)
	}
	dir, err := s.o.Peers.WorktreeDir(ctx, machine, workspace, table)
	if err != nil {
		return "", err
	}
	w, err := s.store.Worktree(ctx, workspace, table, machine)
	if err != nil {
		return "", err
	}
	w.Dir, w.UpdatedAt = dir, s.clk.Now()
	if err := s.store.PutWorktree(ctx, w); err != nil {
		return "", err
	}
	return dir, nil
}

// SyncTower brings the worktree of the table on a Tower to the tip of the
// Citadel: before each step that runs there, and when the Tower gets its
// first character of the table (docs/orquestracao/mesas.md). It sends an
// incremental bundle (the whole branch when the Tower has nothing yet) and
// answers when the Tower brought its worktree to it. A table whose refs moved
// without the engine is not sent.
func (s *Service) SyncTower(ctx context.Context, workspace, table, machine string) error {
	defer s.lock(workspace, table)()
	t, err := s.store.Get(ctx, workspace, table)
	if err != nil {
		return err
	}
	if s.o.Peers == nil || !s.o.Peers.Connected(machine) {
		return fmt.Errorf("%w: %s", ErrMachineUnreachable, machine)
	}
	if !t.State.Accepts() {
		return fmt.Errorf("%w: a mesa %s está %s", ErrBadTransition, table, t.State)
	}
	if err := s.guard(ctx, t); err != nil {
		return err
	}
	mirror, err := s.o.Workspaces.OpenMirror(ctx, workspace)
	if err != nil {
		return err
	}
	tip, err := mirror.ResolveRef(ctx, "refs/heads/"+t.Branch)
	if err != nil {
		return err
	}
	wt, werr := s.store.Worktree(ctx, workspace, table, machine)
	known := werr == nil
	if werr != nil && !errors.Is(werr, ErrNoWorktree) {
		return werr
	}
	if known && wt.LastCommit == string(tip) {
		return nil // already there
	}
	base := ""
	if known && wt.LastCommit != "" {
		if ok, err := mirror.IsAncestor(ctx, wt.LastCommit, string(tip)); err == nil && ok {
			base = wt.LastCommit
		}
	}
	meta := BundleMeta{Workspace: workspace, Table: table, Branch: t.Branch, Tip: string(tip), Base: base}
	src := bundleSources(mirror, s.tmp, t.Branch, base)
	err = SendBundleWithRetry(ctx, func(ctx context.Context) error {
		return s.o.Peers.SendBundle(ctx, machine, meta.Object(), src)
	})
	if err != nil {
		s.tellMaster(ctx, t, fmt.Sprintf("A sincronização da branch %s com a Torre %s falhou: %v", t.Branch, machine, err))
		return fmt.Errorf("sync %s to %s: %w", table, machine, err)
	}
	now := s.clk.Now()
	if !known {
		wt = Worktree{Workspace: workspace, Table: table, Machine: machine, CreatedAt: now}
	}
	wt.LastCommit, wt.UpdatedAt = string(tip), now
	return s.store.PutWorktree(ctx, wt)
}

// UnreachableTowers lists the Towers with a worktree of the table that have no
// connection now: the answer of `mestre pr` carries the warning that the
// branch was sent from the Citadel without them (docs/orquestracao/mesas.md).
func (s *Service) UnreachableTowers(ctx context.Context, workspace, table string) ([]string, error) {
	wts, err := s.store.Worktrees(ctx, workspace, table)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, wt := range wts {
		if wt.Machine != s.o.Machine && (s.o.Peers == nil || !s.o.Peers.Connected(wt.Machine)) {
			out = append(out, wt.Machine)
		}
	}
	return out, nil
}

// Receiver is the Finalizer of the bundles a Tower sends to the Citadel: the
// bundle arrives as refs/mestre/torres/<tower>/<branch>; if the branch of the
// Citadel only needs to move ahead, the engine runs `git merge --ff-only`
// inside the worktree of the table on the Citadel; if it diverges, or the
// worktree has changes that conflict, nothing changes and the table master
// hears of it. Each transfer is idempotent by the SHA-256 of the bundle.
type Receiver struct {
	s       *Service
	machine string
}

var (
	_ protocol.Finalizer = (*Receiver)(nil)
	_ protocol.Skipper   = (*Receiver)(nil)
)

// ReceiverFor is the receiver of the bundles of one Tower, to put in the
// finalizers of its connection (the machine is the one the connection
// authenticated, never what the meta says).
func (s *Service) ReceiverFor(machine string) *Receiver { return &Receiver{s: s, machine: machine} }

// Have says whether this very bundle was already applied.
func (r *Receiver) Have(ctx context.Context, b protocol.XferBegin) bool {
	_, ok, err := r.s.store.Transferred(ctx, b.SHA256, r.machine, ToCitadel)
	return err == nil && ok
}

// MaxBundle is the largest bundle of a table a Tower may send.
const MaxBundle = 2 << 30

// Admit refuses, at xfer.begin, the bundle of a table the Citadel does not have
// or does not take that branch for, or one that is too big: nothing is stored
// for a Tower that sends what could never be applied.
func (r *Receiver) Admit(ctx context.Context, b protocol.XferBegin) error {
	m, err := MetaOf(b.Meta)
	if err != nil {
		return protocol.NewError(protocol.CodeInvalidPayload, "%v", err)
	}
	if b.Size > MaxBundle {
		return protocol.NewError(protocol.CodeTooLarge, "a bundle of a table is at most %d bytes", int64(MaxBundle))
	}
	t, err := r.s.store.Get(ctx, m.Workspace, m.Table)
	if err != nil {
		return protocol.NewError(protocol.CodeForbidden, "the table %s is not known here", m.Table)
	}
	if t.Branch != m.Branch || Over(t.State) {
		return protocol.NewError(protocol.CodeForbidden, "the table %s does not take this branch", m.Table)
	}
	return nil
}

// Finalize implements protocol.Finalizer.
func (r *Receiver) Finalize(ctx context.Context, b protocol.XferBegin, file string) error {
	m, err := MetaOf(b.Meta)
	if err != nil {
		return protocol.NewError(protocol.CodeInvalidPayload, "%v", err)
	}
	return protocol.BundleReceiver{
		Repo: func(ctx context.Context, _ protocol.Object) (protocol.BundleRepo, error) {
			t, err := r.s.store.Get(ctx, m.Workspace, m.Table)
			if err != nil {
				return nil, protocol.NewError(protocol.CodeInvalidPayload, "%v", err)
			}
			if t.Branch != m.Branch || Over(t.State) {
				return nil, protocol.NewError(protocol.CodeInvalidPayload, "the table %s does not take this branch", m.Table)
			}
			return r.s.o.Workspaces.OpenMirror(ctx, m.Workspace)
		},
		Apply: func(ctx context.Context, repo protocol.BundleRepo, _ protocol.Object, f string) error {
			return r.apply(ctx, repo.(gitx.Repo), b, m, f)
		},
	}.Finalize(ctx, b, file)
}

// apply imports the bundle and, when it is a straight advance, fast-forwards
// the worktree of the Citadel.
func (r *Receiver) apply(ctx context.Context, mirror gitx.Repo, b protocol.XferBegin, m BundleMeta, file string) error {
	s := r.s
	defer s.lock(m.Workspace, m.Table)()
	t, err := s.store.Get(ctx, m.Workspace, m.Table)
	if err != nil {
		return err
	}
	ref := TowerRef(r.machine, t.Branch)
	// The refs are checked first: a branch that moved on the Citadel without
	// the engine is not touched by a bundle.
	if _, err := s.checkTable(ctx, t); err != nil {
		return err
	}
	registeredRef := ""
	if prev, err := s.store.Worktree(ctx, t.Workspace, t.ID, r.machine); err == nil {
		registeredRef = prev.RefCommit
	}
	record := func(outcome string, tip gitx.Hash) error {
		return s.store.PutTransfer(ctx, Transfer{SHA256: b.SHA256, Workspace: t.Workspace, Table: t.ID, Machine: r.machine, Direction: ToCitadel,
			Tip: string(tip), Outcome: outcome, At: s.clk.Now()})
	}
	if err := mirror.FetchBundle(ctx, file, "+refs/heads/"+t.Branch+":"+ref); err != nil {
		return protocol.NewError(protocol.CodeInternal, "importing the bundle: %v", err)
	}
	tower, err := mirror.ResolveRef(ctx, ref)
	if err != nil {
		return err
	}
	// What the Tower has, the engine registers: this is a ref it wrote.
	if err := s.store.SetLastCommit(ctx, t.Workspace, t.ID, r.machine, string(tower), s.clk.Now()); err != nil {
		return err
	}
	if err := s.store.SetRefCommit(ctx, t.Workspace, t.ID, r.machine, string(tower), s.clk.Now()); err != nil {
		return err
	}
	if s.Untrusted(t.Workspace, t.ID) {
		return nil // blocked: not recorded, so the same bundle is looked at again after TrustRef
	}
	local, err := mirror.ResolveRef(ctx, "refs/heads/"+t.Branch)
	if err != nil {
		return err
	}
	if local == tower {
		return record("same", tower)
	}
	if behind, err := mirror.IsAncestor(ctx, string(tower), string(local)); err == nil && behind {
		return record("behind", tower) // the Tower is behind: it gets the Citadel's tip at the next sync
	}
	if ahead, err := mirror.IsAncestor(ctx, string(local), string(tower)); err != nil {
		return err
	} else if !ahead {
		s.tellMaster(ctx, t, s.divergence(ctx, mirror, t, r.machine, local, tower))
		return record("diverged", tower)
	}
	// The same checks of a branch that moved ahead on the Citadel: a bundle
	// is a move of the branch too, and it comes from a machine where agents
	// write to the replica (docs/orquestracao/mesas.md).
	if why, err := s.continuationProblem(ctx, mirror, t, r.machine, string(local), string(tower)); err != nil {
		return err
	} else if why != "" {
		s.alert(ctx, t, RefAlert{Ref: ref, Registered: registeredRef, Current: string(tower), Reason: why})
		return nil // blocked: nothing is applied and nothing is recorded
	}
	wt, err := s.store.Worktree(ctx, t.Workspace, t.ID, s.o.Machine)
	if err != nil {
		return err
	}
	if err := s.fastForward(ctx, t, wt, ref); err != nil {
		if errors.Is(err, errWrongBranch) {
			s.tellMaster(ctx, t, fmt.Sprintf("A Torre %s enviou commits para a branch %s, mas o worktree da Cidadela não está nessa branch (%v); nada mudou.", r.machine, t.Branch, err))
			return record("conflict", tower)
		}
		if errors.Is(err, gitx.ErrLocalChanges) || errors.Is(err, gitx.ErrNotFastForward) {
			s.tellMaster(ctx, t, fmt.Sprintf("A Torre %s enviou commits para a branch %s, mas o worktree da Cidadela tem alterações que conflitam com eles; nada mudou. "+
				"Faça o commit ou desfaça essas alterações e peça a sincronização de novo.", r.machine, t.Branch))
			return record("conflict", tower)
		}
		return err
	}
	if err := s.store.SetLastCommit(ctx, t.Workspace, t.ID, s.o.Machine, string(tower), s.clk.Now()); err != nil {
		return err
	}
	return record("applied", tower)
}

// fastForward moves the worktree of the table on the Citadel to ref, inside
// the session zero's Sync so the files of the Mestre do not meet the move.
func (s *Service) fastForward(ctx context.Context, t Table, wt Worktree, ref string) error {
	repo, err := s.worktreeRepo(ctx, wt)
	if err != nil {
		return err
	}
	tr, err := s.tableRequest(ctx, t, gitx.WorktreeRecord{Dir: wt.Dir, GitDir: wt.GitDir, Common: wt.Common})
	if err != nil {
		return err
	}
	tr.Repo = repo
	return s.o.Files.Sync(ctx, tr, func(ctx context.Context) error {
		// MergeFFOnly moves whatever branch the worktree has checked out:
		// an agent that switched it must not have the branch of the table
		// merged onto the other one.
		if err := checkHead(ctx, repo, t.Branch); err != nil {
			return err
		}
		_, err := repo.MergeFFOnly(ctx, ref)
		return err
	})
}

// errWrongBranch is a worktree that has another branch checked out than the
// one of its table.
var errWrongBranch = errors.New("the worktree is not on the branch of its table")

// checkHead refuses a worktree whose HEAD is not the branch of the table.
func checkHead(ctx context.Context, repo gitx.Repo, branch string) error {
	head, err := repo.HeadBranch(ctx)
	if err != nil {
		return err
	}
	if head != branch {
		if head == "" {
			head = "HEAD solto"
		}
		return fmt.Errorf("%w: está em %s, a branch é %s", errWrongBranch, head, branch)
	}
	return nil
}

// continuationProblem says why the commits from..to cannot be taken as a
// continuation of the branch of the table that a character of the machine
// made ("" when they can): nobody of the table is alive there to have made
// them, or they are commits of another table (also the ones its Towers sent).
func (s *Service) continuationProblem(ctx context.Context, mirror gitx.Repo, t Table, machine, from, to string) (string, error) {
	alive, err := s.hasLivingOn(ctx, t, machine)
	if err != nil {
		return "", err
	}
	if !alive {
		return "avançou, mas nenhum personagem da mesa está vivo na Torre " + machine + " para ter feito isso", nil
	}
	foreign, err := s.foreignCommits(ctx, mirror, t, from, to)
	if err != nil {
		return "", err
	}
	if foreign != "" {
		return "recebeu commits que são da mesa " + foreign, nil
	}
	return "", nil
}

// divergence is the notice to the table master: the commits of each side that
// the other lacks.
func (s *Service) divergence(ctx context.Context, mirror gitx.Repo, t Table, machine string, local, tower gitx.Hash) string {
	list := func(rng string) string {
		cs, err := mirror.Log(ctx, rng)
		if err != nil || len(cs) == 0 {
			return "(nenhum)"
		}
		var lines []string
		for _, c := range cs {
			lines = append(lines, "  "+c.Hash.Short()+" "+c.Subject())
		}
		return "\n" + strings.Join(lines, "\n")
	}
	return fmt.Sprintf("Divergência na branch %s: a Torre %s enviou commits que não seguem a branch da Cidadela, que continua como estava.\n"+
		"Commits só da Cidadela:%s\nCommits só da Torre %s:%s\n"+
		"A Torre recebe a ponta da Cidadela em %s no próximo envio; um personagem dela resolve com `git rebase` ou `git merge` dessa ref e sinaliza `done`.",
		t.Branch, machine, list(string(tower)+".."+string(local)), machine, list(string(local)+".."+string(tower)), CitadelRef(t.Branch))
}
