//go:build full

package app

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/internal/testkit/orcfakes"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// chainIntegration gives a Citadel the people of the chain of approval (the users
// and the decrees, without GitHub and without the bot) so that it builds the
// libraries of classes, the effective rules and the hooks of the classes as
// `mestre run` does, with the session zero of a character of the doubles: what
// the test of the characters on real terminals needs.
type chainIntegration struct{}

func (chainIntegration) Open(_ context.Context, env IntegrationEnv) (IntegrationRun, error) {
	us, err := users.New(users.Options{DB: env.DB, Clock: env.Clock})
	if err != nil {
		return nil, err
	}
	return chainRun{users: us}, nil
}

type chainRun struct{ users *users.Service }

type noDecrees struct{}

func (noDecrees) Decrees(context.Context, characters.Character) ([]classes.Decree, error) {
	return nil, nil
}

func (chainRun) Extend(o *OrchestrationOptions) { o.Builder = orcfakes.Builder{} }
func (chainRun) Attach(*Orchestration, *localchan.LocalForwarder) []ExternalFunc {
	return nil
}
func (chainRun) BindFence(*guild.Fence)               {}
func (chainRun) FenceChanged(guild.FenceState)        {}
func (chainRun) Close()                               {}
func (chainRun) OwnerAlert(context.Context, i18n.Msg) {}
func (chainRun) CharacterDecrees() CharacterDecrees   { return noDecrees{} }
func (r chainRun) Users() *users.Service              { return r.users }

// approveClassOn approves a class of the workspace in the Guild database of the
// Citadel, the way the library of the period reads it. files is the package.
func approveClassOn(t *testing.T, a *inproc, workspace, name string, files ...classes.File) {
	t.Helper()
	pkg, err := classes.NewFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	db := orcDB(t, a)
	lib := newClassBooks(db, clock.Real{}, a.paths.ClassesGlobais, workspaces.Layout{Root: a.paths.Workspaces}).For(workspace)
	rec := classes.ClassRecord{Key: classes.ClassKey{Scope: classes.ClassWorkspace, Name: name}, Hash: pkg.Hash(), Files: pkg, ApprovedAt: clock.Real{}.Now(),
		By: classes.Author{Level: classes.LevelAdmin, ID: "u1"}}
	if err := lib.Records.Approve(t.Context(), workspace, rec, 0); err != nil {
		t.Fatal(err)
	}
}

// devClass is the package of a class of the full suite: it does what the fake
// agent scripts ask, and its hooks are the given ones (hooks.yaml).
func devClass(name, hooks string) []classes.File {
	files := []classes.File{
		{Path: "CLASS.md", Data: []byte(fmt.Sprintf("---\nname: %s\ndescription: Classe de teste\napp: novo\n---\nFaça o que a mesa pede.\n", name))},
		{Path: "permissions.yaml", Data: []byte("version: 1\nfiles:\n  read: [\"**\"]\n  write: [\"**\"]\ncommands:\n  default: allow\ngit:\n  commit: true\n")},
	}
	if hooks != "" {
		files = append(files, classes.File{Path: "hooks/hooks.yaml", Data: []byte(hooks)})
	}
	return files
}

var _ = filepath.Join
