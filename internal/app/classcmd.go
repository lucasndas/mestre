package app

import (
	"context"
	"errors"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// classCommands are the commands `mestre class …` over the libraries of the
// workspaces, the registry of the characters and the inbox of the Grand
// Master (docs/cli/coordenacao-classes-e-rituais.md). Without libraries the
// commands are not registered.
func classCommands(o OrchestrationOptions, reg characters.Registry, ib *inbox.Service) *classes.Commands {
	if o.ClassLibraries == nil {
		return nil
	}
	return &classes.Commands{
		Scope: func(ctx context.Context, c *localchan.Call) (classes.Caller, error) {
			x, refused := inbox.Caller(ctx, reg, c)
			if refused != nil {
				return classes.Caller{}, errors.New(refused.Error.Message)
			}
			lib := o.ClassLibraries(x.Workspace)
			if lib == nil {
				return classes.Caller{}, errors.New("the workspace has no classes yet")
			}
			who := classes.Caller{Lib: lib, Workspace: x.Workspace, Character: x.ID, Name: x.Name, Author: classes.Author{ID: x.ID}}
			switch x.Role {
			case characters.RoleTableMaster:
				who.Table, who.Author.Level, who.Author.Table = x.Table, classes.LevelTableMaster, x.Table
			case characters.RoleGrandMaster:
				who.Author.Level = classes.LevelGrandMaster
			default:
				who.Table = x.Table
			}
			return who, nil
		},
		InUse: func(ctx context.Context, who classes.Caller, class string) ([]string, error) {
			alive, err := reg.AliveOf(ctx, who.Workspace)
			if err != nil {
				return nil, err
			}
			var names []string
			for _, c := range alive {
				if classes.Slug(c.Class) == class {
					names = append(names, c.Name)
				}
			}
			return names, nil
		},
		Assign: func(ctx context.Context, who classes.Caller, character, class string) error {
			c, err := reg.ByName(ctx, who.Workspace, who.Table, character)
			if err != nil {
				return classes.ErrNotVisible
			}
			return reg.SetClass(ctx, c.ID, class)
		},
		NotifyGrandMaster: inbox.GrandMaster{S: ib}.NotifySystem,
		Tmp: func(ctx context.Context, who classes.Caller) (string, error) {
			if o.ClassTmp == nil {
				return "", errors.New("this machine does not know the MESTRE_TMP of the character")
			}
			return o.ClassTmp(ctx, who.Character)
		},
		Destructive: o.ClassDestructive,
	}
}
