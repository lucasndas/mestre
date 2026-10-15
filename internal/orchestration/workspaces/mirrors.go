package workspaces

import (
	"context"
	"fmt"

	"github.com/lucasndas/mestre/internal/replication"
)

// Mirrors is the set of mirrors of the Citadel for the replication to send to
// the Towers (replication.MirrorSource): the identifier of a mirror is the
// slug of its workspace, and only workspaces whose mirror exists count.
type Mirrors struct{ s *Service }

var (
	_ replication.MirrorSource = Mirrors{}
	_ replication.RefFilter    = Mirrors{}
)

// RefFilter says, for a workspace, which refs of its mirror stay on the
// Citadel and are not replicated to the Towers.
type RefFilter func(ctx context.Context, workspace string) (func(ref string) bool, error)

// WithholdRefs sets the filter of the refs of the mirrors the replication
// does not send: the tables set it so that the branches of the tables, which
// travel by the bundles of the tables after the check of their refs, do not
// travel by the mirror.
func (s *Service) WithholdRefs(f RefFilter) {
	s.mu.Lock()
	s.withhold = f
	s.mu.Unlock()
}

// Withheld implements replication.RefFilter.
func (m Mirrors) Withheld(ctx context.Context, id string) (func(ref string) bool, error) {
	m.s.mu.Lock()
	f := m.s.withhold
	m.s.mu.Unlock()
	if f == nil {
		return nil, nil
	}
	return f(ctx, id)
}

// Mirrors returns the mirrors of the workspaces.
func (s *Service) Mirrors() Mirrors { return Mirrors{s: s} }

// IDs lists the slugs of the workspaces that have a mirror.
func (m Mirrors) IDs(ctx context.Context) ([]string, error) {
	list, err := m.s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, w := range list {
		if w.MirrorReady() {
			ids = append(ids, w.Slug)
		}
	}
	return ids, nil
}

// Open opens the mirror of a workspace. The mirror is a repository without a
// working tree, so git opens it by its own folder and nothing an agent
// plants inside is followed (docs/arquitetura/seguranca.md#git-do-motor).
func (m Mirrors) Open(ctx context.Context, id string) (replication.MirrorRepo, error) {
	w, err := m.s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !w.MirrorReady() {
		return nil, fmt.Errorf("workspace %s: the mirror does not exist yet (stage %s)", id, w.Stage)
	}
	return m.s.o.Git.Open(ctx, m.s.layout.Mirror(id))
}
