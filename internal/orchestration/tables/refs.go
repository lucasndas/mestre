package tables

import (
	"context"
	"errors"
	"fmt"

	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
)

// RefAlert is a ref of the mirror that moved without the engine
// (docs/orquestracao/mesas.md#worktree-por-máquina-e-sincronização, "refs
// alheias não são confiáveis"): a Codex character reaches the writing of every
// ref of the mirror (docs/classes/reforco-nativo.md), so the engine compares
// the branch of each table, and the refs of the Towers, with the last commit
// it registered itself (table_worktrees) before it sends or synchronises
// anything.
type RefAlert struct {
	Workspace, Table string
	Ref              string
	// Registered is what the engine registered, Current what the ref has now
	// ("" for a ref that disappeared).
	Registered, Current string
	Reason              string
}

func (a RefAlert) String() string {
	return fmt.Sprintf("%s da mesa %s: %s (registrado %s, agora %s)", a.Ref, a.Table, a.Reason, gitx.Hash(a.Registered).Short(), gitx.Hash(a.Current).Short())
}

func untrustedKey(workspace, table string) string { return workspace + "/" + table }

// Untrusted says whether a ref of the table moved without the engine and
// nobody trusted it again. The alerts are in the database (a restart or a
// new Citadel does not forget them); when the database cannot be read the
// table is not trusted.
func (s *Service) Untrusted(workspace, table string) bool {
	s.mu.Lock()
	_, ok := s.untrusted[untrustedKey(workspace, table)]
	s.mu.Unlock()
	if ok {
		return true
	}
	has, err := s.store.HasAlert(context.Background(), workspace, table)
	return err != nil || has
}

// CheckRefs checks the branches of the tables of the workspace, and the refs
// the Towers sent, against what the engine registered. A ref that moved
// without the engine is an alert to the table master and to the
// Administrators, and the table is not trusted: nothing of it is sent to a
// Tower or to GitHub, and no bundle is applied to it, until TrustRef. A branch
// that only moved ahead, in a table where a character is alive to commit, is
// registered as the new tip.
func (s *Service) CheckRefs(ctx context.Context, workspace string) ([]RefAlert, error) {
	ts, err := s.store.List(ctx, workspace)
	if err != nil {
		return nil, err
	}
	var out []RefAlert
	for _, t := range ts {
		if t.BranchDeleted {
			continue
		}
		as, err := s.checkLocked(ctx, t)
		if err != nil {
			return out, err
		}
		out = append(out, as...)
	}
	return out, nil
}

// checkLocked checks one table under its lock: a check that runs while a
// character commits or a bundle is applied would take the move in between for
// a ref that moved without the engine.
func (s *Service) checkLocked(ctx context.Context, t Table) ([]RefAlert, error) {
	defer s.lock(t.Workspace, t.ID)()
	cur, err := s.store.Get(ctx, t.Workspace, t.ID) // under the lock
	if err != nil {
		if errors.Is(err, ErrNoTable) {
			return nil, nil
		}
		return nil, err
	}
	return s.checkTable(ctx, cur)
}

// checkTable checks the refs of one table.
func (s *Service) checkTable(ctx context.Context, t Table) ([]RefAlert, error) {
	wts, err := s.store.Worktrees(ctx, t.Workspace, t.ID)
	if err != nil || len(wts) == 0 {
		return nil, err
	}
	mirror, err := s.o.Workspaces.OpenMirror(ctx, t.Workspace)
	if err != nil {
		return nil, err
	}
	var alerts []RefAlert
	for _, wt := range wts {
		if wt.Machine == s.o.Machine {
			a, err := s.checkBranch(ctx, mirror, t, wt)
			if err != nil {
				return alerts, err
			}
			alerts = append(alerts, a...)
			continue
		}
		ref := TowerRef(wt.Machine, t.Branch)
		cur, err := mirror.ResolveRef(ctx, ref)
		switch {
		case errors.Is(err, gitx.ErrRefNotFound):
		case err != nil:
			return alerts, err
		case wt.RefCommit != "" && string(cur) != wt.RefCommit:
			alerts = append(alerts, s.alert(ctx, t, RefAlert{Ref: ref, Registered: wt.RefCommit, Current: string(cur), Reason: "andou sem passar pelo motor"}))
		}
	}
	return alerts, nil
}

func (s *Service) checkBranch(ctx context.Context, mirror gitx.Repo, t Table, wt Worktree) ([]RefAlert, error) {
	ref := "refs/heads/" + t.Branch
	cur, err := mirror.ResolveRef(ctx, ref)
	if errors.Is(err, gitx.ErrRefNotFound) {
		return []RefAlert{s.alert(ctx, t, RefAlert{Ref: ref, Registered: wt.LastCommit, Reason: "a branch desapareceu do espelho"})}, nil
	}
	if err != nil {
		return nil, err
	}
	if string(cur) == wt.LastCommit {
		return nil, nil // an alert, if there is one, stays until TrustRef
	}
	ahead, err := mirror.IsAncestor(ctx, wt.LastCommit, string(cur))
	if err != nil {
		return nil, err
	}
	if !ahead {
		return []RefAlert{s.alert(ctx, t, RefAlert{Ref: ref, Registered: wt.LastCommit, Current: string(cur), Reason: "não é continuação do que o motor registrou"})}, nil
	}
	alive, err := s.hasLiving(ctx, t)
	if err != nil {
		return nil, err
	}
	if !alive {
		return []RefAlert{s.alert(ctx, t, RefAlert{Ref: ref, Registered: wt.LastCommit, Current: string(cur), Reason: "avançou, mas nenhum personagem da mesa está vivo para ter feito isso"})}, nil
	}
	if foreign, err := s.foreignCommits(ctx, mirror, t, wt.LastCommit, string(cur)); err != nil {
		return nil, err
	} else if foreign != "" {
		return []RefAlert{s.alert(ctx, t, RefAlert{Ref: ref, Registered: wt.LastCommit, Current: string(cur), Reason: "recebeu commits que são da mesa " + foreign})}, nil
	}
	if s.Untrusted(t.Workspace, t.ID) {
		return nil, nil // moved ahead after an alert: still not trusted
	}
	return nil, s.store.SetLastCommit(ctx, t.Workspace, t.ID, s.o.Machine, string(cur), s.clk.Now())
}

// foreignCommits looks for the commits a branch received (from..to) in the
// other tables of the workspace: a table never receives the work of another,
// so a branch moved ahead onto commits that are in the branch of another table
// was moved by someone who is not its characters. It answers with that table
// ("" for none). The engine registers the commits of each table when its
// characters give `done` and at every sweep; between two registrations the
// commits of a table that were just made, and are already in a branch moved
// onto them, make both tables suspect, and an Administrator looks.
func (s *Service) foreignCommits(ctx context.Context, mirror gitx.Repo, t Table, from, to string) (string, error) {
	mine, err := mirror.Log(ctx, from+".."+to)
	if err != nil || len(mine) == 0 {
		return "", err
	}
	in := make(map[gitx.Hash]bool, len(mine))
	for _, c := range mine {
		in[c.Hash] = true
	}
	others, err := s.store.List(ctx, t.Workspace)
	if err != nil {
		return "", err
	}
	for _, o := range others {
		if o.ID == t.ID || o.BranchDeleted || o.BaseCommit == "" {
			continue
		}
		// The branch of the other table and what its Towers sent.
		revs := []string{"refs/heads/" + o.Branch}
		wts, err := s.store.Worktrees(ctx, o.Workspace, o.ID)
		if err != nil {
			return "", err
		}
		for _, wt := range wts {
			if wt.Machine != s.o.Machine {
				revs = append(revs, TowerRef(wt.Machine, o.Branch))
			}
		}
		for _, rev := range revs {
			cs, err := mirror.Log(ctx, o.BaseCommit+".."+rev)
			if err != nil {
				continue // the ref is gone or unreadable: its own check says so
			}
			for _, c := range cs {
				if in[c.Hash] {
					return o.ID, nil
				}
			}
		}
	}
	return "", nil
}

// hasLivingOn says whether a character of the table is alive on a machine.
func (s *Service) hasLivingOn(ctx context.Context, t Table, machine string) (bool, error) {
	cs, err := s.o.Registry.AliveOf(ctx, t.Workspace)
	if err != nil {
		return false, err
	}
	for _, c := range cs {
		if c.Table == t.ID && c.Machine == machine {
			return true, nil
		}
	}
	return false, nil
}

// hasLiving says whether a character of the table is alive.
func (s *Service) hasLiving(ctx context.Context, t Table) (bool, error) {
	cs, err := s.o.Registry.AliveOf(ctx, t.Workspace)
	if err != nil {
		return false, err
	}
	for _, c := range cs {
		if c.Table == t.ID {
			return true, nil
		}
	}
	return false, nil
}

// alert records the alert once per commit the ref moved to, tells the table
// master and the Administrators, and marks the table as not trusted.
func (s *Service) alert(ctx context.Context, t Table, a RefAlert) RefAlert {
	a.Workspace, a.Table = t.Workspace, t.ID
	key := untrustedKey(t.Workspace, t.ID)
	// The mark is in the database, per ref; the one in memory only holds the
	// table untrusted if the database cannot be written.
	s.mu.Lock()
	s.untrusted[key] = a.Current + "|" + a.Ref
	s.mu.Unlock()
	fresh, err := s.store.SetAlert(ctx, t.Workspace, t.ID, a.Ref, a.Current, s.clk.Now())
	if err != nil {
		s.log.Warn("tables: the alert of a ref was not recorded", "table", t.ID, "ref", a.Ref, "err", err)
		fresh = true
	} else {
		s.mu.Lock()
		delete(s.untrusted, key)
		s.mu.Unlock()
	}
	if !fresh {
		return a
	}
	text := "Alerta: a ref " + a.String() + ". Nada desta mesa é enviado nem sincronizado até um Administrador confiar de novo na ref."
	s.tellMaster(ctx, t, text)
	if err := s.o.Alerts.Operational(ctx, workspaces.Alert{Workspace: t.Workspace, Kind: workspaces.AlertRefMoved, Detail: text, Since: s.clk.Now()}); err != nil {
		s.log.Warn("tables: the Administrators were not told of a ref that moved", "table", t.ID, "err", err)
	}
	return a
}

// TrustRef trusts the branch of the table again: an Administrator looked at
// what moved it. The tip it has now becomes the registered one.
func (s *Service) TrustRef(ctx context.Context, workspace, id string) error {
	defer s.lock(workspace, id)()
	t, err := s.store.Get(ctx, workspace, id)
	if err != nil {
		return err
	}
	mirror, err := s.o.Workspaces.OpenMirror(ctx, workspace)
	if err != nil {
		return err
	}
	cur, err := mirror.ResolveRef(ctx, "refs/heads/"+t.Branch)
	if err != nil {
		return err
	}
	if err := s.store.SetLastCommit(ctx, workspace, id, s.o.Machine, string(cur), s.clk.Now()); err != nil {
		return err
	}
	wts, err := s.store.Worktrees(ctx, workspace, id)
	if err != nil {
		return err
	}
	for _, wt := range wts {
		if wt.Machine == s.o.Machine {
			continue
		}
		if tip, err := mirror.ResolveRef(ctx, TowerRef(wt.Machine, t.Branch)); err == nil {
			if err := s.store.SetRefCommit(ctx, workspace, id, wt.Machine, string(tip), s.clk.Now()); err != nil {
				return err
			}
		}
	}
	if err := s.store.ClearAlerts(ctx, workspace, id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.untrusted, untrustedKey(workspace, id))
	s.mu.Unlock()
	return nil
}

// guard refuses to send or synchronise a table whose refs are not trusted,
// after a fresh check.
func (s *Service) guard(ctx context.Context, t Table) error {
	if _, err := s.checkTable(ctx, t); err != nil {
		return err
	}
	if s.Untrusted(t.Workspace, t.ID) {
		return fmt.Errorf("%w: a mesa %s", ErrRefMoved, t.ID)
	}
	return nil
}

// Refs of the bundles.
const (
	// CitadelRefPrefix holds, on a Tower, the tip of the branch the Citadel
	// sent: refs/mestre/cidadela/<branch>.
	CitadelRefPrefix = "refs/mestre/cidadela/"
	// TowerRefPrefix holds, on the Citadel, what a Tower sent:
	// refs/mestre/torres/<tower>/<branch>.
	TowerRefPrefix = "refs/mestre/torres/"
)

// CitadelRef is the ref, on a Tower, of the tip of the Citadel.
func CitadelRef(branch string) string { return CitadelRefPrefix + branch }

// TowerRef is the ref, on the Citadel, of the tip a Tower sent.
func TowerRef(machine, branch string) string { return TowerRefPrefix + machine + "/" + branch }
