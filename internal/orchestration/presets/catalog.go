package presets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// ClassLookup finds the approved class a sheet names. The classes of a
// workspace or a table are the ones of that place, so the real lookup is the
// Library of the workspace; a sheet is global, and the class it names is
// resolved again, by workspace, when a character is born.
type ClassLookup interface {
	Class(ctx context.Context, name string) (*classes.Class, bool, error)
}

// LibraryLookup is the ClassLookup over a Library: the approved class of
// that name in the global and workspace scopes.
type LibraryLookup struct{ Library *classes.Library }

// Class implements ClassLookup.
func (l LibraryLookup) Class(ctx context.Context, name string) (*classes.Class, bool, error) {
	c, err := l.Library.Approved(ctx, name, "")
	switch {
	case errors.Is(err, classes.ErrNoApproved):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	return c, true, nil
}

// Users tells which live characters were born from a sheet. The character
// registry (M4) implements it; nil means none.
type Users interface {
	SheetUsers(ctx context.Context, sheet string) ([]string, error)
}

// MachineSheets are the sheets of the machines of the Guild
// (machinesheet.Registry).
type MachineSheets interface {
	Sheets(ctx context.Context) ([]machinesheet.Sheet, error)
}

// Catalog keeps the bases and the sheets in the Guild database, on the
// Citadel (a Tower reads the replica). Nothing here knows who is asking: the
// panel checks the level before it calls, and agents have no way in
// ([bases e fichas](docs/orquestracao/bases-e-fichas.md)).
type Catalog struct {
	DB    *store.DB
	Clock clock.Clock
	// Classes, Users and Machines may be nil: a sheet is then checked
	// without its class, deleting is not blocked by living characters, and
	// there is no machine choice.
	Classes  ClassLookup
	Users    Users
	Machines MachineSheets
}

// ErrBuiltin: the base the Mestre creates is not deleted.
var ErrBuiltin = errors.New("a base é criada pelo Mestre e não se apaga")

// ErrInUse: a base used by sheets, or a sheet used by living characters, is
// not deleted. The error lists who uses it.
var ErrInUse = errors.New("em uso")

// InUseError says who uses what was to be deleted.
type InUseError struct {
	Kind, Name string
	By         []string
}

func (e *InUseError) Error() string {
	return fmt.Sprintf("%s `%s` %s por %s", e.Kind, e.Name, ErrInUse, strings.Join(e.By, ", "))
}

// Unwrap lets errors.Is find ErrInUse.
func (e *InUseError) Unwrap() error { return ErrInUse }

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (c *Catalog) write(ctx context.Context, fn func(tx querier) error) error {
	return c.DB.WriteDurable(ctx, func(tx *sql.Tx) error { return fn(tx) })
}

func (c *Catalog) now() int64 { return c.Clock.Now().UTC().UnixMilli() }

func loadBase(ctx context.Context, q querier, name string) (Base, bool, error) {
	var doc []byte
	err := q.QueryRowContext(ctx, `SELECT doc FROM bases WHERE name = ?`, name).Scan(&doc)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Base{}, false, nil
	case err != nil:
		return Base{}, false, fmt.Errorf("base %s: %w", name, err)
	}
	var b Base
	if err := json.Unmarshal(doc, &b); err != nil {
		return Base{}, false, fmt.Errorf("base %s: stored document: %w", name, err)
	}
	return b.clone(), true, nil
}

func loadSheet(ctx context.Context, q querier, name string) (Sheet, bool, error) {
	var doc []byte
	err := q.QueryRowContext(ctx, `SELECT doc FROM sheets WHERE name = ?`, name).Scan(&doc)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Sheet{}, false, nil
	case err != nil:
		return Sheet{}, false, fmt.Errorf("sheet %s: %w", name, err)
	}
	var s Sheet
	if err := json.Unmarshal(doc, &s); err != nil {
		return Sheet{}, false, fmt.Errorf("sheet %s: stored document: %w", name, err)
	}
	return s.clone(), true, nil
}

func loadAll[T any](ctx context.Context, q querier, query string, args ...any) ([]T, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		var doc []byte
		if err := rows.Scan(&doc); err != nil {
			return nil, err
		}
		var v T
		if err := json.Unmarshal(doc, &v); err != nil {
			return nil, fmt.Errorf("stored document: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func loadBases(ctx context.Context, q querier) ([]Base, error) {
	bs, err := loadAll[Base](ctx, q, `SELECT doc FROM bases ORDER BY name`)
	for i := range bs {
		bs[i] = bs[i].clone()
	}
	return bs, err
}

func loadSheets(ctx context.Context, q querier, base string) ([]Sheet, error) {
	query, args := `SELECT doc FROM sheets ORDER BY name`, []any(nil)
	if base != "" {
		query, args = `SELECT doc FROM sheets WHERE base = ? ORDER BY name`, []any{base}
	}
	ss, err := loadAll[Sheet](ctx, q, query, args...)
	for i := range ss {
		ss[i] = ss[i].clone()
	}
	return ss, err
}

// Base is the base of that name.
func (c *Catalog) Base(ctx context.Context, name string) (Base, bool, error) {
	return loadBase(ctx, c.DB.Reader(), name)
}

// Bases are all the bases, by name.
func (c *Catalog) Bases(ctx context.Context) ([]Base, error) { return loadBases(ctx, c.DB.Reader()) }

// Sheet is the sheet of that name.
func (c *Catalog) Sheet(ctx context.Context, name string) (Sheet, bool, error) {
	return loadSheet(ctx, c.DB.Reader(), name)
}

// Sheets are all the sheets, by name.
func (c *Catalog) Sheets(ctx context.Context) ([]Sheet, error) {
	return loadSheets(ctx, c.DB.Reader(), "")
}

func (c *Catalog) putBase(ctx context.Context, q querier, b Base, builtin bool) error {
	doc, err := json.Marshal(b.clone())
	if err != nil {
		return err
	}
	flag := 0
	if builtin {
		flag = 1
	}
	_, err = q.ExecContext(ctx, `INSERT INTO bases (name, doc, builtin, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET doc = excluded.doc, updated_at = excluded.updated_at`,
		b.Name, doc, flag, c.now(), c.now())
	return err
}

func (c *Catalog) putSheet(ctx context.Context, q querier, s Sheet) error {
	doc, err := json.Marshal(s.clone())
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO sheets (name, base, doc, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET base = excluded.base, doc = excluded.doc, updated_at = excluded.updated_at`,
		s.Name, s.Base, doc, c.now(), c.now())
	return err
}

func (c *Catalog) class(ctx context.Context, name string) (*classes.Class, error) {
	if name == "" || c.Classes == nil {
		return nil, nil
	}
	cl, ok, err := c.Classes.Class(ctx, name)
	if err != nil || !ok {
		return nil, err
	}
	return cl, nil
}

func (c *Catalog) sheetContext(ctx context.Context, s Sheet, base Base) (SheetContext, error) {
	cl, err := c.class(ctx, s.Class)
	if err != nil {
		return SheetContext{}, err
	}
	return SheetContext{Base: base, Class: cl, ClassMissing: s.Class != "" && cl == nil}, nil
}

// SaveBase checks and saves a base, creating it or replacing the one of the
// same name. The sheets that use it are checked against the new version: it
// is not saved if one of them would stop being valid. The base `operador` is
// created by the Mestre; of it only the arguments change.
func (c *Catalog) SaveBase(ctx context.Context, b Base) error {
	b = b.clone()
	if err := ValidateBase(b); err != nil {
		return err
	}
	return c.write(ctx, func(tx querier) error { return c.saveBase(ctx, tx, b) })
}

func (c *Catalog) saveBase(ctx context.Context, tx querier, b Base) error {
	_, existed, err := loadBase(ctx, tx, b.Name)
	if err != nil {
		return err
	}
	if b.Name == OperatorBase {
		if !existed {
			return fmt.Errorf("%w: a base `%s` é criada pelo Mestre quando o Operador do PC está habilitado", ErrInvalid, OperatorBase)
		}
		if err := checkOperatorEdit(b); err != nil {
			return err
		}
	}
	sheets, err := loadSheets(ctx, tx, b.Name)
	if err != nil {
		return err
	}
	var bad []string
	for _, s := range sheets {
		sc, err := c.sheetContext(ctx, s, b)
		if err != nil {
			return err
		}
		if _, err := ValidateSheet(s, sc); err != nil {
			bad = append(bad, fmt.Sprintf("a ficha `%s` ficaria inválida (%s)", s.Name, strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": ")))
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalid, strings.Join(bad, "; "))
	}
	return c.putBase(ctx, tx, b, b.Name == OperatorBase)
}

// DeleteBase deletes a base nobody uses. The one the Mestre creates is not
// deleted, and a base used by sheets is refused with the list of them.
func (c *Catalog) DeleteBase(ctx context.Context, name string) error {
	return c.write(ctx, func(tx querier) error {
		if _, ok, err := loadBase(ctx, tx, name); err != nil || !ok {
			if err == nil {
				err = fmt.Errorf("%w: a base `%s` não existe", ErrInvalid, name)
			}
			return err
		}
		if name == OperatorBase {
			return ErrBuiltin
		}
		sheets, err := loadSheets(ctx, tx, name)
		if err != nil {
			return err
		}
		if len(sheets) > 0 {
			by := make([]string, len(sheets))
			for i, s := range sheets {
				by[i] = "`" + s.Name + "`"
			}
			return &InUseError{Kind: "a base", Name: name, By: by}
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM bases WHERE name = ?`, name)
		return err
	})
}

// SaveSheet checks and saves a sheet. The warnings are what is saved but has
// no effect (an allow of the restrictions that the class does not give).
func (c *Catalog) SaveSheet(ctx context.Context, s Sheet) (warnings []string, err error) {
	s = s.clone()
	err = c.write(ctx, func(tx querier) error {
		base, _, err := loadBase(ctx, tx, s.Base)
		if err != nil {
			return err
		}
		sc, err := c.sheetContext(ctx, s, base)
		if err != nil {
			return err
		}
		if warnings, err = ValidateSheet(s, sc); err != nil {
			return err
		}
		return c.putSheet(ctx, tx, s)
	})
	return warnings, err
}

// DeleteSheet deletes a sheet no living character was born from; the others
// refuse it, with the list of who uses it.
func (c *Catalog) DeleteSheet(ctx context.Context, name string) error {
	if _, ok, err := c.Sheet(ctx, name); err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("%w: a ficha `%s` não existe", ErrInvalid, name)
		}
		return err
	}
	if c.Users != nil {
		by, err := c.Users.SheetUsers(ctx, name)
		if err != nil {
			return err
		}
		if len(by) > 0 {
			return &InUseError{Kind: "a ficha", Name: name, By: by}
		}
	}
	return c.write(ctx, func(tx querier) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM sheets WHERE name = ?`, name)
		return err
	})
}

// Snapshot is the copy of a sheet and its base that a character keeps from
// birth: later edits apply to new characters only.
type Snapshot struct {
	Sheet Sheet `json:"sheet"`
	Base  Base  `json:"base"`
}

// Snapshot copies the sheet and its base as they are now.
func (c *Catalog) Snapshot(ctx context.Context, sheet string) (Snapshot, error) {
	s, ok, err := c.Sheet(ctx, sheet)
	switch {
	case err != nil:
		return Snapshot{}, err
	case !ok:
		return Snapshot{}, fmt.Errorf("%w: a ficha `%s` não existe", ErrInvalid, sheet)
	}
	b, ok, err := c.Base(ctx, s.Base)
	switch {
	case err != nil:
		return Snapshot{}, err
	case !ok:
		return Snapshot{}, fmt.Errorf("%w: a base `%s` da ficha `%s` não existe", ErrInvalid, s.Base, sheet)
	}
	return Snapshot{Sheet: s, Base: b}, nil
}

// Choose matches the machines of the Guild against a character of this sheet
// (docs/guilda/ficha-da-maquina.md#casamento-e-escolha-da-máquina). The
// choice's Err is the error of the creation when none fits. class is the class
// the character will have: the sheet's, or the one `recruit --class` gave.
func (c *Catalog) Choose(ctx context.Context, sheet, class string) (machinesheet.Choice, error) {
	if c.Machines == nil {
		return machinesheet.Choice{}, errors.New("presets: no machine sheets to choose from")
	}
	snap, err := c.Snapshot(ctx, sheet)
	if err != nil {
		return machinesheet.Choice{}, err
	}
	if class == "" {
		class = snap.Sheet.Class
	}
	cl, err := c.class(ctx, class)
	if err != nil {
		return machinesheet.Choice{}, err
	}
	req, err := Requirements(snap.Base, snap.Sheet, cl)
	if err != nil {
		return machinesheet.Choice{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	machines, err := c.Machines.Sheets(ctx)
	if err != nil {
		return machinesheet.Choice{}, err
	}
	return Choose(snap.Base, req, machines), nil
}
