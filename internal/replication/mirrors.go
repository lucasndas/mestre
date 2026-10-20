package replication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/protocol"
)

// The mirrors (docs/guilda/replicacao.md#arquivos-e-espelhos): every Tower
// keeps a copy of the mirror of each workspace, brought up to date by git
// bundle every time a ref of the mirror on the Citadel moves, with the
// mechanism of the tables (ADR 0013). The workspaces are milestone 4; here is
// the mechanism, with the set of mirrors left to whoever owns them
// (MirrorSource) and the layout on the Tower to MirrorStore.

// MirrorRepo is what the Citadel needs of a mirror; gitx.Repo satisfies it.
type MirrorRepo interface {
	ListRefs(ctx context.Context, prefixes ...string) ([]gitx.Ref, error)
	CreateBundle(ctx context.Context, file string, revs ...string) error
}

// MirrorSource is the set of mirrors of the Citadel.
type MirrorSource interface {
	// IDs lists the mirrors (the identifier of a workspace).
	IDs(ctx context.Context) ([]string, error)
	// Open returns the repository of a mirror.
	Open(ctx context.Context, id string) (MirrorRepo, error)
}

// mirrorRefs are the refs a mirror replicates.
var mirrorRefs = []string{"refs/heads/", "refs/tags/"}

// ReplicaPrefix is where the replica of a mirror keeps what the Citadel
// sent: refs/heads/main arrives as refs/mestre/espelho/heads/main. The
// branches of the replica itself (refs/heads/, the ones the worktrees of the
// tables check out and the commits of the characters of the Tower are on) are
// never written, fetched into or pruned by the replication.
const ReplicaPrefix = "refs/mestre/espelho/"

// ReplicaRef is the name, in the replica, of a ref of the mirror of the
// Citadel.
func ReplicaRef(ref string) string { return ReplicaPrefix + strings.TrimPrefix(ref, "refs/") }

// RefFilter may be implemented by a MirrorSource that keeps refs out of the
// replication: Withheld gives, for a mirror, the function that says whether a
// ref stays on the Citadel. The branches of the tables travel by the bundles of
// the tables (the engine's check of their refs comes first), never by the
// mirror, so a branch that is not trusted does not reach a Tower this way.
type RefFilter interface {
	Withheld(ctx context.Context, id string) (func(ref string) bool, error)
}

var mirrorID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// Mirrors sends the mirrors of the Citadel to the Towers.
type Mirrors struct {
	src MirrorSource
	dir string
	// mu serializes the syncs: two bundles of one mirror to one Tower must not
	// finish in the wrong order and leave the replica behind.
	mu sync.Mutex
}

// NewMirrors returns the sender; dir is where the bundles are written while
// they are sent (a private folder).
func NewMirrors(src MirrorSource, dir string) *Mirrors {
	return &Mirrors{src: src, dir: dir}
}

// mirrorMeta is the meta of a transfer of a mirror: its id and the refs it
// leaves the Tower with.
type mirrorMeta struct {
	Mirror string            `json:"mirror"`
	Refs   map[string]string `json:"refs"`
}

func (m mirrorMeta) object() protocol.Object {
	raw, _ := json.Marshal(m)
	var o protocol.Object
	_ = json.Unmarshal(raw, &o)
	return o
}

func mirrorMetaOf(o protocol.Object) (mirrorMeta, error) {
	raw, err := json.Marshal(o)
	if err != nil {
		return mirrorMeta{}, err
	}
	var m mirrorMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return mirrorMeta{}, err
	}
	if !mirrorID.MatchString(m.Mirror) {
		return mirrorMeta{}, fmt.Errorf("mirror id %q is not valid", m.Mirror)
	}
	return m, nil
}

func (m *Mirrors) syncAll(ctx context.Context, link *protocol.Link, pe *peer) error {
	ids, err := m.src.IDs(ctx)
	if err != nil {
		return fmt.Errorf("replication: listing the mirrors: %w", err)
	}
	// a mirror that cannot be sent does not keep the Tower from the rest
	var errs []error
	for _, id := range ids {
		if err := m.sync(ctx, link, pe, id); err != nil {
			if ctx.Err() != nil {
				return err
			}
			errs = append(errs, fmt.Errorf("mirror %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// sync brings one mirror of one Tower up to date: a bundle of what moved since
// the last time on this connection, or of the whole mirror the first time. A
// Tower that lacks the base of an incremental bundle answers
// missing_prerequisite and gets the whole mirror (protocol.Link.SendBundle).
func (m *Mirrors) sync(ctx context.Context, link *protocol.Link, pe *peer, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pe.mu.Lock()
	prev := pe.tips[id]
	pe.mu.Unlock()
	tr, err := m.Bundle(ctx, id, prev)
	if err != nil {
		return err
	}
	if tr.Tips == nil {
		return nil // nothing to send
	}
	if err := link.SendBundle(ctx, tr.Meta, tr.Source); err != nil {
		return fmt.Errorf("replication: sending mirror %s: %w", id, err)
	}
	pe.mu.Lock()
	if pe.tips == nil {
		pe.tips = map[string]map[string]gitx.Hash{}
	}
	pe.tips[id] = tr.Tips
	pe.mu.Unlock()
	return nil
}

// Transfer is the bundle of a mirror for a Tower.
type Transfer struct {
	// Meta and Source are what protocol.Link.SendBundle takes.
	Meta   protocol.Object
	Source protocol.BundleSource
	// Tips are the refs the Tower has once it applied the transfer; nil when
	// there is nothing to send (no ref, or the Tower has them all).
	Tips map[string]gitx.Hash
}

// Bundle builds the transfer of the mirror id for a Tower that has the refs
// prev of it (nil for none): the refs withheld by the source (RefFilter) are
// left out, and so is the whole transfer when the Tower already has what is
// left.
func (m *Mirrors) Bundle(ctx context.Context, id string, prev map[string]gitx.Hash) (Transfer, error) {
	repo, err := m.src.Open(ctx, id)
	if err != nil {
		return Transfer{}, fmt.Errorf("replication: opening mirror %s: %w", id, err)
	}
	refs, err := repo.ListRefs(ctx, mirrorRefs...)
	if err != nil {
		return Transfer{}, fmt.Errorf("replication: mirror %s: %w", id, err)
	}
	if f, ok := m.src.(RefFilter); ok {
		withheld, err := f.Withheld(ctx, id)
		if err != nil {
			return Transfer{}, fmt.Errorf("replication: mirror %s: %w", id, err)
		}
		refs = slices.DeleteFunc(refs, func(r gitx.Ref) bool { return withheld != nil && withheld(r.Name) })
	}
	if len(refs) == 0 {
		return Transfer{}, nil
	}
	cur := make(map[string]gitx.Hash, len(refs))
	names := make([]string, 0, len(refs))
	wire := make(map[string]string, len(refs))
	for _, r := range refs {
		cur[r.Name] = r.Hash
		names = append(names, r.Name)
		wire[r.Name] = string(r.Hash)
	}
	sort.Strings(names)
	if sameTips(prev, cur) {
		return Transfer{}, nil
	}

	var incremental []string
	complete := prev != nil
	for _, n := range names {
		switch old, ok := prev[n]; {
		case ok && old == cur[n]:
		case ok:
			incremental = append(incremental, string(old)+".."+n)
		default:
			complete = false
		}
	}
	open := func(revs []string) protocol.OpenSource {
		return func(ctx context.Context) (protocol.Source, func(), error) {
			f, err := os.CreateTemp(m.dir, "mirror-*.bundle")
			if err != nil {
				return protocol.Source{}, nil, err
			}
			name := f.Name()
			_ = f.Close()
			cleanup := func() { _ = os.Remove(name) }
			// git refuses to overwrite nothing: it writes the file itself.
			_ = os.Remove(name)
			if err := repo.CreateBundle(ctx, name, revs...); err != nil {
				cleanup()
				return protocol.Source{}, nil, err
			}
			rf, err := os.Open(name)
			if err != nil {
				cleanup()
				return protocol.Source{}, nil, err
			}
			fi, err := rf.Stat()
			if err != nil {
				_ = rf.Close()
				cleanup()
				return protocol.Source{}, nil, err
			}
			return protocol.Source{ReaderAt: rf, Size: fi.Size()}, func() { _ = rf.Close(); cleanup() }, nil
		}
	}
	src := protocol.BundleSource{Full: open(names)}
	if complete && len(incremental) > 0 {
		// Only the refs that moved; the rest the Tower has from before.
		src.Incremental = open(incremental)
	}
	return Transfer{Meta: mirrorMeta{Mirror: id, Refs: wire}.object(), Source: src, Tips: cur}, nil
}

func sameTips(a, b map[string]gitx.Hash) bool {
	if a == nil || len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// MirrorStore is where a Tower keeps its copy of the mirrors: a bare
// repository per mirror, at <Root>/<id>/espelho.git, the same layout as the
// Citadel's (Root is <dados>/workspaces and id the slug of the workspace;
// docs/orquestracao/workspaces.md), so the worktrees of the tables of the
// Tower come out of it. What the Citadel sends is kept under ReplicaPrefix.
type MirrorStore struct {
	Root string
	Git  gitx.Git
}

// Open opens the replica of a mirror, making it if the Tower has none yet.
func (s MirrorStore) Open(ctx context.Context, id string) (gitx.Repo, error) { return s.open(ctx, id) }

func (s MirrorStore) open(ctx context.Context, id string) (gitx.Repo, error) {
	if !mirrorID.MatchString(id) {
		return nil, fmt.Errorf("mirror id %q is not valid", id)
	}
	dir := filepath.Join(s.Root, id, "espelho.git")
	repo, err := s.Git.Open(ctx, dir)
	if err == nil {
		return repo, nil
	}
	if !errors.Is(err, gitx.ErrNotARepo) {
		return nil, err
	}
	return s.Git.Init(ctx, dir, gitx.InitOptions{Bare: true})
}

// mirrorReceiver is the Finalizer of kind git-bundle for the mirrors: it
// verifies the bundle in the repository of the mirror and fetches its refs.
type mirrorReceiver struct {
	store MirrorStore
	protocol.BundleReceiver
}

var (
	_ protocol.Finalizer = (*mirrorReceiver)(nil)
	_ protocol.Skipper   = (*mirrorReceiver)(nil)
)

// MirrorFinalizer is the Finalizer of the transfers of kind git-bundle of the
// mirrors on a Tower (the replica uses it; the tests of the workspaces and
// the tables call it to apply what Mirrors.Bundle builds).
func MirrorFinalizer(s MirrorStore) protocol.Finalizer { return newMirrorReceiver(s) }

func newMirrorReceiver(s MirrorStore) *mirrorReceiver {
	return &mirrorReceiver{
		store: s,
		BundleReceiver: protocol.BundleReceiver{
			Repo: func(ctx context.Context, meta protocol.Object) (protocol.BundleRepo, error) {
				m, err := mirrorMetaOf(meta)
				if err != nil {
					return nil, protocol.NewError(protocol.CodeInvalidPayload, "%v", err)
				}
				return s.open(ctx, m.Mirror)
			},
			Apply: func(ctx context.Context, repo protocol.BundleRepo, meta protocol.Object, file string) error {
				// "+": the mirror follows the Citadel, whatever it did to its refs.
				// Into the namespace of the replica, never into refs/heads/: the
				// branches of the tables are checked out there, and git refuses to
				// fetch into a checked-out branch (the whole fetch would fail).
				if err := repo.(gitx.Repo).FetchBundle(ctx, file, "+refs/heads/*:"+ReplicaPrefix+"heads/*", "+refs/tags/*:"+ReplicaPrefix+"tags/*"); err != nil {
					return err
				}
				return pruneMirror(ctx, repo.(gitx.Repo), meta)
			},
		},
	}
}

// pruneMirror deletes the refs the Citadel no longer has: the meta of a bundle
// lists all the refs of the mirror, not only the ones the bundle carries. A
// mirror left with no ref at all sends no bundle, so its refs stay. Only the
// namespace of the replica is pruned: the branches of the Tower, and the
// commits of its characters, are not touched.
func pruneMirror(ctx context.Context, repo gitx.Repo, meta protocol.Object) error {
	m, err := mirrorMetaOf(meta)
	if err != nil {
		return err
	}
	if len(m.Refs) == 0 {
		return nil
	}
	kept := make(map[string]bool, len(m.Refs))
	for name := range m.Refs {
		kept[ReplicaRef(name)] = true
	}
	have, err := repo.ListRefs(ctx, ReplicaPrefix)
	if err != nil {
		return err
	}
	for _, ref := range have {
		if kept[ref.Name] {
			continue
		}
		if err := repo.DeleteRef(ctx, ref.Name); err != nil {
			return fmt.Errorf("replication: pruning %s from the mirror: %w", ref.Name, err)
		}
	}
	return nil
}

// Have says whether the mirror already has the refs the Citadel announces, and
// no others; then no bundle is sent.
func (r *mirrorReceiver) Have(ctx context.Context, b protocol.XferBegin) bool {
	m, err := mirrorMetaOf(b.Meta)
	if err != nil || len(m.Refs) == 0 {
		return false
	}
	repo, err := r.store.open(ctx, m.Mirror)
	if err != nil {
		return false
	}
	refs, err := repo.ListRefs(ctx, ReplicaPrefix)
	if err != nil || len(refs) != len(m.Refs) {
		return false
	}
	have := make(map[string]string, len(refs))
	for _, ref := range refs {
		have[ref.Name] = string(ref.Hash)
	}
	for name, hash := range m.Refs {
		if have[ReplicaRef(name)] != hash {
			return false
		}
	}
	return true
}
