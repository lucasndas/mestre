package app

import (
	"fmt"
	"path/filepath"

	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/replication"
)

// The names of the folders of the Citadel on the wire: the Tower has the same
// ones, each at its own path (docs/guilda/replicacao.md#arquivos-e-espelhos).
const (
	filesWorkspaces = "workspaces"
	filesClasses    = "classes-globais"
	filesTemplates  = "modelos"
	filesOperator   = "operador"
)

// replicationFiles are the folders every Tower keeps a copy of: the global
// classes, the templates, the captures of the Operator (already encrypted, not
// for an excluded machine) and the .mestre/ of the main clone of each workspace
// (the scrolls, the attachments and what is only local in it). A folder with no
// path is left out; nil when there is none. The two sides build the same set.
func (p Paths) replicationFiles() (*replication.Files, error) {
	var roots []replication.FileRoot
	add := func(name, dir string, r replication.FileRoot) error {
		if dir == "" {
			return nil
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("app: the folder %s: %w", name, err)
		}
		r.Name, r.Dir, r.Walk = name, abs, true
		roots = append(roots, r)
		return nil
	}
	for _, f := range []struct {
		name, dir string
		root      replication.FileRoot
	}{
		{filesClasses, p.ClassesGlobais, replication.FileRoot{}},
		{filesTemplates, p.Modelos, replication.FileRoot{}},
		{filesOperator, p.Operador, replication.FileRoot{NotForExcluded: true}},
		{filesWorkspaces, p.Workspaces, replication.FileRoot{Allow: workspaces.SharedFile}},
	} {
		if err := add(f.name, f.dir, f.root); err != nil {
			return nil, err
		}
	}
	if len(roots) == 0 {
		return nil, nil
	}
	return replication.NewFiles(roots...)
}

// replicationCites is what a change of the database cites among those files:
// today the scrolls of the tables (the other records keep their content in the
// database; the attachments come with the bot, M5).
func replicationCites() replication.Cites { return scrolls.Cites(filesWorkspaces) }
