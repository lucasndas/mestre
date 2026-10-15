package presets

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

// The Operator of the PC: the one base the Mestre creates and nobody deletes,
// and its default sheet (docs/orquestracao/bases-e-fichas.md#base-especial-operador-do-pc).
// What runs it (the session process, the approval, the deadline) is the
// Operator's own milestone (M7); here are the rules that hold from the first
// day: what the base is, who may use it and why a request fails.
const (
	// OperatorBase is the name of the base.
	OperatorBase = "operador"
	// OperatorSheet is the name of the default sheet.
	OperatorSheet = "Operador"
)

// CodeOperatorClassMissing is the code of a request for the Operator whose
// sheet has no class with `operator: true` yet.
const CodeOperatorClassMissing = "operator_class_missing"

// ErrOperatorClassMissing: the sheet "Operador" is incomplete. The Mestre
// has no ready-made classes, so the Owner creates one with `operator: true`
// on the panel and picks it as the sheet's class.
var ErrOperatorClassMissing = errors.New(CodeOperatorClassMissing)

// OperatorBaseDef is the base `operador` as the Mestre creates it.
func OperatorBaseDef() Base {
	return Base{
		Name:      OperatorBase,
		Command:   "claude.exe",
		Args:      []string{"--chrome"},
		Platforms: []Platform{{OS: Windows}},
		Requires:  map[string]any{"operador": true},
		Ready:     Ready{Mode: ReadyHook},
	}
}

// OperatorSheetDef is the default sheet "Operador": it has no class, because
// the Mestre comes with none.
func OperatorSheetDef() Sheet {
	one := 1
	return Sheet{Name: OperatorSheet, Base: OperatorBase, Agent: AgentClaudeCode, Lifecycle: Ephemeral, MaxCopies: &one}
}

// Incomplete says why a sheet cannot be used yet: the sheet of the Operator
// without the class that has `operator: true`. Empty is complete.
func (s Sheet) Incomplete() string {
	if s.Base == OperatorBase && s.Class == "" {
		return "falta a classe com `operator: true`"
	}
	return ""
}

// checkOperatorEdit: of the base `operador` only the arguments are edited
// (by the Owner; the level is the panel's to ask), so no other base becomes
// an Operator and this one never stops being it.
func checkOperatorEdit(b Base) error {
	want := OperatorBaseDef()
	want.Args = b.Args
	if !equalBase(b, want) {
		return fmt.Errorf("%w: a base `%s` é criada pelo Mestre; só os argumentos se editam", ErrInvalid, OperatorBase)
	}
	return nil
}

func equalBase(a, b Base) bool { return reflect.DeepEqual(canon(a), canon(b)) }

// canon is the base with an empty list or map as absent and the arguments
// out, so two bases compare by what the Owner may not change.
func canon(b Base) Base {
	b = b.clone()
	b.Args = nil
	if len(b.Machines) == 0 {
		b.Machines = nil
	}
	if len(b.Env) == 0 {
		b.Env = nil
	}
	if len(b.Requires) == 0 {
		b.Requires = nil
	}
	for i := range b.Platforms {
		if len(b.Platforms[i].Args) == 0 {
			b.Platforms[i].Args = nil
		}
	}
	return b
}

// SeedOperator creates the base `operador` and the sheet "Operador" when the
// installation has the Operator enabled. It keeps what is there: the Owner's
// arguments, and the class the Owner picked.
func (c *Catalog) SeedOperator(ctx context.Context) error {
	return c.write(ctx, func(tx querier) error {
		if _, ok, err := loadBase(ctx, tx, OperatorBase); err != nil {
			return err
		} else if !ok {
			if err := c.putBase(ctx, tx, OperatorBaseDef(), true); err != nil {
				return err
			}
		}
		if _, ok, err := loadSheet(ctx, tx, OperatorSheet); err != nil {
			return err
		} else if !ok {
			return c.putSheet(ctx, tx, OperatorSheetDef())
		}
		return nil
	})
}

// CheckOperatorRequest is what a request for the Operator checks first
// (`mestre recruit` with this sheet, M7): the sheet is the Operator's and
// its class has `operator: true`. A sheet without the class fails with
// ErrOperatorClassMissing.
func (c *Catalog) CheckOperatorRequest(ctx context.Context, sheet string) error {
	s, ok, err := c.Sheet(ctx, sheet)
	switch {
	case err != nil:
		return err
	case !ok:
		return fmt.Errorf("%w: a ficha `%s` não existe", ErrInvalid, sheet)
	case s.Base != OperatorBase:
		return fmt.Errorf("%w: a ficha `%s` não é da base `%s`", ErrInvalid, sheet, OperatorBase)
	case s.Incomplete() != "":
		return fmt.Errorf("%w: a ficha `%s` %s", ErrOperatorClassMissing, sheet, s.Incomplete())
	}
	if c.Classes == nil {
		return nil
	}
	// The class may have been deleted, or changed, since the sheet was saved.
	cl, err := c.class(ctx, s.Class)
	switch {
	case err != nil:
		return err
	case cl == nil || cl.Permissions == nil || cl.Permissions.Operator == nil || !*cl.Permissions.Operator:
		return fmt.Errorf("%w: a classe `%s` da ficha `%s` não existe ou não tem `operator: true`", ErrOperatorClassMissing, s.Class, sheet)
	}
	return nil
}
