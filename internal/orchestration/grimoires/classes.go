package grimoires

import (
	"context"
	"errors"
	"fmt"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
)

// LibraryClasses is the Classes of the Libraries of the workspaces
// (classes.Library): the approved version is what is read, and a copy is saved
// as a class of the table through Library.Save, which checks the package, the
// ceiling and the name before anything is written.
type LibraryClasses struct {
	// For gives the Library of a workspace.
	For func(ctx context.Context, workspace string) (*classes.Library, error)
}

var _ Classes = LibraryClasses{}

// Find implements Classes.
func (l LibraryClasses) Find(ctx context.Context, workspace, table, name string) (ClassInfo, bool, error) {
	lib, err := l.For(ctx, workspace)
	if err != nil {
		return ClassInfo{}, false, err
	}
	c, err := lib.Approved(ctx, name, table)
	switch {
	case errors.Is(err, classes.ErrNoApproved):
		return ClassInfo{}, false, nil
	case err != nil:
		return ClassInfo{}, false, err
	}
	scope := c.Scope
	if name == classes.ReservedOrchestrator {
		scope = classes.ClassGlobal // built into the binary
	}
	return ClassInfo{Name: c.Name, Scope: scope, App: c.App.String(), Files: c.Files}, true, nil
}

// Excess implements Classes: the package is read as a class of a table and its
// permissions are held against the ceiling of the workspace. A package that is
// not valid is listed as the excess too, since it cannot be copied.
func (l LibraryClasses) Excess(ctx context.Context, workspace string, files classes.Files) ([]string, error) {
	lib, err := l.For(ctx, workspace)
	if err != nil {
		return nil, err
	}
	ex := classes.Examine(files, classes.ExamineOptions{Scope: classes.ClassTable, Skill: func(string) bool { return true }})
	if !ex.OK() {
		var out []string
		for _, p := range ex.Problems {
			out = append(out, "pacote inválido: "+p.String())
		}
		return out, nil
	}
	if ex.Class.Permissions == nil {
		return nil, nil
	}
	ceil := classes.DefaultCeiling()
	if lib.Ceiling != nil {
		if ceil, err = lib.Ceiling.Current(ctx); err != nil {
			return nil, err
		}
	}
	var out []string
	for _, e := range ceil.Excess(ex.Class.Permissions) {
		out = append(out, e.String())
	}
	return out, nil
}

// Install implements Classes: the Administrator or the Owner who restores the
// grimoire saves the class in the scope of the table, where they apply it by
// themselves.
func (l LibraryClasses) Install(ctx context.Context, workspace, table string, c ClassInfo, by Actor) error {
	lib, err := l.For(ctx, workspace)
	if err != nil {
		return err
	}
	level := classes.LevelAdmin
	if by.Level == characters.LevelOwner {
		level = classes.LevelOwner
	}
	res, err := lib.Save(ctx, classes.SaveRequest{Name: c.Name, Scope: classes.ClassTable, Table: table, App: c.App, Files: c.Files,
		By: classes.Author{Level: level, ID: by.ID}})
	if err != nil {
		return err
	}
	if !res.Applied {
		return fmt.Errorf("the class %s was left as a proposal", c.Name)
	}
	return nil
}
