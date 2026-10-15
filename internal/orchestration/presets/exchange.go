package presets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// MaxDocument is the largest file an import reads.
const MaxDocument = 1 << 20

// ParseDocument reads an export file, YAML or JSON (the JSON is read as
// JSON, and anything else as YAML). It checks the shape only: unknown fields,
// the kind and a version this binary reads. What the bases and sheets say is
// checked by PlanImport.
func ParseDocument(data []byte) (*Document, error) {
	if len(data) > MaxDocument {
		return nil, fmt.Errorf("%w: o arquivo passa de %d bytes", ErrInvalid, MaxDocument)
	}
	var d Document
	var err error
	if t := bytes.TrimSpace(data); len(t) > 0 && t[0] == '{' {
		dec := json.NewDecoder(bytes.NewReader(t))
		dec.DisallowUnknownFields()
		err = dec.Decode(&d)
	} else {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		err = dec.Decode(&d)
		if errors.Is(err, io.EOF) {
			err = errors.New("arquivo vazio")
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%w: arquivo ilegível: %v", ErrInvalid, err)
	}
	switch {
	case d.Kind != Kind:
		return nil, fmt.Errorf("%w: `kind` é `%s`; o esperado é `%s`", ErrInvalid, d.Kind, Kind)
	case d.Version > Version:
		return nil, fmt.Errorf("%w: versão %d do arquivo; esta instalação lê até a versão %d", ErrInvalid, d.Version, Version)
	case d.Version < 1:
		return nil, fmt.Errorf("%w: `version` é obrigatório; a esperada é %d", ErrInvalid, Version)
	}
	for i := range d.Bases {
		d.Bases[i] = d.Bases[i].clone()
	}
	for i := range d.Sheets {
		d.Sheets[i] = d.Sheets[i].clone()
	}
	return &d, nil
}

// YAML is the file in YAML.
func (d *Document) YAML() ([]byte, error) { return yaml.Marshal(d) }

// JSON is the file in JSON.
func (d *Document) JSON() ([]byte, error) { return json.MarshalIndent(d, "", "  ") }

// Selection says what to export; the zero value is everything. The sheets
// carry their bases along, so the file stands by itself.
type Selection struct {
	Bases, Sheets []string
	// All exports every base and sheet, except the base the Mestre creates
	// and the sheets of it: every installation has its own. A file that
	// names the base `operador` imports where the installation has it.
	All bool
}

// Export builds the file of the selection. It holds no secret: an env that
// looks like one is refused when the base is saved.
func (c *Catalog) Export(ctx context.Context, sel Selection) (*Document, error) {
	bases, err := c.Bases(ctx)
	if err != nil {
		return nil, err
	}
	sheets, err := c.Sheets(ctx)
	if err != nil {
		return nil, err
	}
	d := &Document{Kind: Kind, Version: Version}
	wantBase := map[string]bool{}
	for _, n := range sel.Bases {
		wantBase[n] = true
	}
	known := map[string]bool{}
	for _, s := range sheets {
		known[s.Name] = true
	}
	for _, n := range sel.Sheets {
		if !known[n] {
			return nil, fmt.Errorf("%w: a ficha `%s` não existe", ErrInvalid, n)
		}
	}
	for _, s := range sheets {
		if (sel.All && s.Base != OperatorBase) || slices.Contains(sel.Sheets, s.Name) {
			d.Sheets = append(d.Sheets, s)
			if s.Base != OperatorBase {
				wantBase[s.Base] = true
			}
		}
	}
	for _, b := range bases {
		if (sel.All && b.Name != OperatorBase) || wantBase[b.Name] {
			d.Bases = append(d.Bases, b)
			delete(wantBase, b.Name)
		}
	}
	if len(wantBase) > 0 {
		names := make([]string, 0, len(wantBase))
		for n := range wantBase {
			names = append(names, n)
		}
		slices.Sort(names)
		return nil, fmt.Errorf("%w: a base `%s` não existe", ErrInvalid, names[0])
	}
	return d, nil
}

// Item names a base or a sheet of a file.
type Item struct {
	Kind string // "base" or "sheet"
	Name string
}

// Action is what the user picks for a name that already exists.
type Action string

// The three choices.
const (
	Replace Action = "replace"
	Rename  Action = "rename"
	Skip    Action = "skip"
)

// Choice is the answer for one conflict; NewName is for Rename.
type Choice struct {
	Action  Action
	NewName string
}

// Plan is a file already checked as a whole, and the names it shares with
// what exists.
type Plan struct {
	Doc *Document
	// Conflicts are the names that already exist; each one needs a Choice.
	Conflicts []Item
	// Warnings are what is saved but has no effect.
	Warnings []string
}

// state is the bases and sheets as they would be after an import.
type state struct {
	bases  map[string]Base
	sheets map[string]Sheet
}

// PlanImport checks the whole file, before anything is written: the
// version and the fields (ParseDocument), every base, every sheet against
// the bases of the file and of the Guild, and the names repeated inside the
// file. It writes nothing.
func (c *Catalog) PlanImport(ctx context.Context, d *Document) (*Plan, error) {
	st, err := c.currentState(ctx)
	if err != nil {
		return nil, err
	}
	plan := &Plan{Doc: d}
	for _, b := range d.Bases {
		if _, ok := st.bases[b.Name]; ok {
			plan.Conflicts = append(plan.Conflicts, Item{"base", b.Name})
		}
	}
	for _, s := range d.Sheets {
		if _, ok := st.sheets[s.Name]; ok {
			plan.Conflicts = append(plan.Conflicts, Item{"sheet", s.Name})
		}
	}
	// With every name taken as the file spells it (replacing).
	cand := &Document{Kind: Kind, Version: Version, Bases: d.Bases, Sheets: d.Sheets}
	warnings, err := c.checkState(ctx, st.overlay(cand), cand)
	plan.Warnings = warnings
	return plan, err
}

func (c *Catalog) currentState(ctx context.Context) (state, error) {
	return c.stateOf(ctx, c.DB.Reader())
}

func (c *Catalog) stateOf(ctx context.Context, q querier) (state, error) {
	bases, err := loadBases(ctx, q)
	if err != nil {
		return state{}, err
	}
	sheets, err := loadSheets(ctx, q, "")
	if err != nil {
		return state{}, err
	}
	st := state{bases: map[string]Base{}, sheets: map[string]Sheet{}}
	for _, b := range bases {
		st.bases[b.Name] = b
	}
	for _, s := range sheets {
		st.sheets[s.Name] = s
	}
	return st, nil
}

func (st state) overlay(d *Document) state {
	out := state{bases: map[string]Base{}, sheets: map[string]Sheet{}}
	for k, v := range st.bases {
		out.bases[k] = v
	}
	for k, v := range st.sheets {
		out.sheets[k] = v
	}
	for _, b := range d.Bases {
		out.bases[b.Name] = b
	}
	for _, s := range d.Sheets {
		out.sheets[s.Name] = s
	}
	return out
}

// checkState validates what the file brings (cand) and every sheet that uses
// a base the file brings, in the state after the import. All the problems
// come back at once.
func (c *Catalog) checkState(ctx context.Context, after state, cand *Document) (warnings []string, err error) {
	var p problems
	seen := map[Item]bool{}
	dup := func(it Item) bool {
		if seen[it] {
			p.add("%s `%s` aparece mais de uma vez no arquivo", map[string]string{"base": "a base", "sheet": "a ficha"}[it.Kind], it.Name)
			return true
		}
		seen[it] = true
		return false
	}
	fromFile := map[string]bool{}
	for _, b := range cand.Bases {
		fromFile[b.Name] = true
		if dup(Item{"base", b.Name}) {
			continue
		}
		if err := ValidateBase(b); err != nil {
			p.add("base `%s`: %s", b.Name, trimInvalid(err))
		}
		if b.Name == OperatorBase {
			if _, ok := after.bases[OperatorBase]; ok {
				if err := checkOperatorEdit(b); err != nil {
					p.add("base `%s`: %s", b.Name, trimInvalid(err))
				}
			}
		}
	}
	checked := map[string]bool{}
	for _, s := range cand.Sheets {
		checked[s.Name] = true
		if dup(Item{"sheet", s.Name}) {
			continue
		}
		w, err := c.checkSheetIn(ctx, after, s)
		warnings = append(warnings, w...)
		if err != nil {
			p.add("ficha `%s`: %s", s.Name, trimInvalid(err))
		}
	}
	for _, s := range after.sheets {
		if !checked[s.Name] && fromFile[s.Base] {
			if _, err := c.checkSheetIn(ctx, after, s); err != nil {
				p.add("a ficha `%s` ficaria inválida com a base `%s` do arquivo: %s", s.Name, s.Base, trimInvalid(err))
			}
		}
	}
	return warnings, p.err()
}

func (c *Catalog) checkSheetIn(ctx context.Context, after state, s Sheet) ([]string, error) {
	base, ok := after.bases[s.Base]
	if !ok {
		return nil, fmt.Errorf("%w: a base `%s` não existe nem no arquivo nem na Guilda", ErrInvalid, s.Base)
	}
	sc, err := c.sheetContext(ctx, s, base)
	if err != nil {
		return nil, err
	}
	return ValidateSheet(s, sc)
}

func trimInvalid(err error) string { return strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": ") }

// Applied counts what an import did.
type Applied struct {
	Added, Replaced, Renamed, Skipped int
}

// ApplyImport applies the plan with the user's choices, all at once or not
// at all: the final names are worked out (a renamed base takes the sheets of
// the file that name it along), the whole result is checked again, and one
// transaction writes it. A conflict without a choice, a new name that is
// taken, and a result that is not valid stop it before anything is written.
func (c *Catalog) ApplyImport(ctx context.Context, plan *Plan, choices map[Item]Choice) (Applied, error) {
	var res Applied
	err := c.write(ctx, func(tx querier) error {
		res = Applied{}
		cur, err := c.stateOf(ctx, tx)
		if err != nil {
			return err
		}
		final := &Document{Kind: Kind, Version: Version}
		renamedBase := map[string]string{}
		taken := map[Item]bool{}
		for k := range cur.bases {
			taken[Item{"base", k}] = true
		}
		for k := range cur.sheets {
			taken[Item{"sheet", k}] = true
		}
		for _, b := range plan.Doc.Bases {
			it := Item{"base", b.Name}
			nb, keep, err := decide(it, taken[it], choices, taken, &res)
			if err != nil {
				return err
			}
			if !keep {
				continue
			}
			if nb != b.Name {
				renamedBase[b.Name] = nb
				taken[Item{"base", nb}] = true
			}
			b.Name = nb
			final.Bases = append(final.Bases, b)
		}
		for _, s := range plan.Doc.Sheets {
			it := Item{"sheet", s.Name}
			ns, keep, err := decide(it, taken[it], choices, taken, &res)
			if err != nil {
				return err
			}
			if !keep {
				continue
			}
			if nb, ok := renamedBase[s.Base]; ok {
				s.Base = nb
			}
			if ns != s.Name {
				taken[Item{"sheet", ns}] = true
			}
			s.Name = ns
			final.Sheets = append(final.Sheets, s)
		}
		after := cur.overlay(final)
		if _, err := c.checkState(ctx, after, final); err != nil {
			return err
		}
		for _, b := range final.Bases {
			if err := c.putBase(ctx, tx, b, b.Name == OperatorBase); err != nil {
				return err
			}
		}
		for _, s := range final.Sheets {
			if err := c.putSheet(ctx, tx, s); err != nil {
				return err
			}
		}
		return nil
	})
	return res, err
}

// decide works out the final name of an item and whether it is written.
func decide(it Item, exists bool, choices map[Item]Choice, taken map[Item]bool, res *Applied) (name string, keep bool, err error) {
	if !exists {
		res.Added++
		return it.Name, true, nil
	}
	ch, ok := choices[it]
	if !ok {
		return "", false, fmt.Errorf("%w: %s `%s` já existe; escolha substituir, renomear ou pular", ErrInvalid, map[string]string{"base": "a base", "sheet": "a ficha"}[it.Kind], it.Name)
	}
	switch ch.Action {
	case Replace:
		res.Replaced++
		return it.Name, true, nil
	case Skip:
		res.Skipped++
		return it.Name, false, nil
	case Rename:
		if ch.NewName == "" || taken[Item{it.Kind, ch.NewName}] {
			return "", false, fmt.Errorf("%w: o novo nome `%s` de %s `%s` está vazio ou já existe", ErrInvalid, ch.NewName, map[string]string{"base": "a base", "sheet": "a ficha"}[it.Kind], it.Name)
		}
		res.Renamed++
		return ch.NewName, true, nil
	}
	return "", false, fmt.Errorf("%w: ação `%s` desconhecida; use replace, rename ou skip", ErrInvalid, ch.Action)
}
