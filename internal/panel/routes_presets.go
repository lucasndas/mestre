package panel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/mestre-ia/mestre/internal/orchestration/presets"
	"github.com/mestre-ia/mestre/internal/users"
)

func init() { RegisterArea("presets", mountPresets) }

const (
	pinPresetSave   = "presets.save"
	pinPresetDelete = "presets.delete"
	pinPresetImport = "presets.import"
)

func mountPresets(r *Router) {
	p := r.Panel()
	// bases decide which program a terminal runs: the Owner only, and PIN to change them
	r.Require("GET /api/presets", p.handlePresets, users.BasesVaultPolicy)
	r.Require("GET /api/presets/export", p.handlePresetsExport, users.BasesVaultPolicy)
	r.Sensitive("PUT /api/presets/bases/{name}", pinPresetSave, users.BasesVaultPolicy, p.handleSaveBase)
	r.Sensitive("PUT /api/presets/sheets/{name}", pinPresetSave, users.BasesVaultPolicy, p.handleSaveSheet)
	r.Sensitive("DELETE /api/presets/bases/{name}", pinPresetDelete, users.BasesVaultPolicy, p.handleDeleteBase)
	r.Sensitive("DELETE /api/presets/sheets/{name}", pinPresetDelete, users.BasesVaultPolicy, p.handleDeleteSheet)
	r.Require("POST /api/presets/import/plan", p.handleImportPlan, users.BasesVaultPolicy)
	r.Sensitive("POST /api/presets/import", pinPresetImport, users.BasesVaultPolicy, p.handleImport)
}

func (p *Panel) presets(w http.ResponseWriter) (PresetSource, bool) {
	src := p.mg().Presets
	if src == nil {
		WriteError(w, Err(CodeUnavailable, "as bases e fichas não estão disponíveis agora", nil))
		return nil, false
	}
	return src, true
}

func yamlOf(v any) string {
	b, err := yaml.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func readyText(r presets.Ready) string {
	switch r.Mode {
	case presets.ReadyPattern:
		return "pattern: " + r.Pattern
	case presets.ReadyDelay:
		return fmt.Sprintf("delay: %ds", r.Delay)
	}
	return string(r.Mode)
}

func (p *Panel) handlePresets(w http.ResponseWriter, r *http.Request) {
	src, ok := p.presets(w)
	if !ok {
		return
	}
	bases, err := src.Bases(r.Context())
	if err == nil {
		var sheets []presets.Sheet
		if sheets, err = src.Sheets(r.Context()); err == nil {
			WriteJSON(w, http.StatusOK, presetsPage(bases, sheets))
			return
		}
	}
	p.log.Error("panel: listing the bases and sheets", "err", err)
	WriteError(w, errInternal())
}

func presetsPage(bases []presets.Base, sheets []presets.Sheet) PresetsPage {
	page := PresetsPage{Bases: []BaseRow{}, Sheets: []SheetRow{}}
	for _, b := range bases {
		row := BaseRow{Name: b.Name, Command: b.Command, Ready: readyText(b.Ready), Builtin: b.Name == presets.OperatorBase, Platforms: []string{}, YAML: yamlOf(b)}
		for _, pl := range b.Platforms {
			row.Platforms = append(row.Platforms, pl.OS)
		}
		page.Bases = append(page.Bases, row)
	}
	for _, s := range sheets {
		page.Sheets = append(page.Sheets, SheetRow{Name: s.Name, Base: s.Base, Agent: s.Agent, Class: s.Class, Account: s.Account,
			Lifecycle: s.LifecycleOrDefault(), Incomplete: s.Incomplete(), YAML: yamlOf(s)})
	}
	return page
}

// strictYAML decodes one document, refusing the fields it does not know.
func strictYAML(text string, v any) error {
	dec := yaml.NewDecoder(strings.NewReader(text))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("texto vazio")
		}
		return err
	}
	return nil
}

// presetError answers with what the catalog refused: the message names the field and,
// for a YAML that does not read, the line.
func (p *Panel) presetError(w http.ResponseWriter, err error) {
	var inUse *presets.InUseError
	switch {
	case errors.As(err, &inUse):
		WriteError(w, Err(CodePresetInUse, err.Error(), nil))
	case errors.Is(err, presets.ErrInvalid), errors.Is(err, presets.ErrBuiltin), errors.Is(err, presets.ErrOperatorClassMissing):
		WriteError(w, Err(CodePresetInvalid, err.Error(), nil))
	default:
		p.log.Error("panel: the bases and sheets", "err", err)
		WriteError(w, errInternal())
	}
}

func (p *Panel) handleSaveBase(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), UserOf(r)
	src, ok := p.presets(w)
	if !ok {
		return
	}
	var in PresetSave
	if !decode(w, r, &in) {
		return
	}
	var b presets.Base
	if err := strictYAML(in.YAML, &b); err != nil {
		WriteError(w, Err(CodePresetInvalid, "YAML ilegível: "+err.Error(), nil))
		return
	}
	if name := r.PathValue("name"); b.Name != name && name != "novo" {
		WriteError(w, Err(CodePresetInvalid, "o nome do YAML ("+b.Name+") não é o da base ("+name+"); renomear é criar outra e apagar esta", nil))
		return
	}
	before := p.baseYAML(ctx, src, b.Name)
	if err := src.SaveBase(ctx, b); err != nil {
		p.presetError(w, err)
		return
	}
	p.audit(ctx, "panel.base.saved", map[string]any{"user_id": me.ID, "base": b.Name, "before": before, "after": yamlOf(b), "ip": clientIP(r)})
	WriteJSON(w, http.StatusOK, PresetSaved{Name: b.Name, Warnings: []string{}})
}

func (p *Panel) baseYAML(ctx context.Context, src PresetSource, name string) string {
	all, err := src.Bases(ctx)
	if err != nil {
		return ""
	}
	for _, b := range all {
		if b.Name == name {
			return yamlOf(b)
		}
	}
	return ""
}

func (p *Panel) sheetYAML(ctx context.Context, src PresetSource, name string) string {
	all, err := src.Sheets(ctx)
	if err != nil {
		return ""
	}
	for _, s := range all {
		if s.Name == name {
			return yamlOf(s)
		}
	}
	return ""
}

func (p *Panel) handleSaveSheet(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), UserOf(r)
	src, ok := p.presets(w)
	if !ok {
		return
	}
	var in PresetSave
	if !decode(w, r, &in) {
		return
	}
	var s presets.Sheet
	if err := strictYAML(in.YAML, &s); err != nil {
		WriteError(w, Err(CodePresetInvalid, "YAML ilegível: "+err.Error(), nil))
		return
	}
	if name := r.PathValue("name"); s.Name != name && name != "novo" {
		WriteError(w, Err(CodePresetInvalid, "o nome do YAML ("+s.Name+") não é o da ficha ("+name+"); renomear é criar outra e apagar esta", nil))
		return
	}
	before := p.sheetYAML(ctx, src, s.Name)
	warnings, err := src.SaveSheet(ctx, s)
	if err != nil {
		p.presetError(w, err)
		return
	}
	p.audit(ctx, "panel.sheet.saved", map[string]any{"user_id": me.ID, "sheet": s.Name, "before": before, "after": yamlOf(s), "ip": clientIP(r)})
	WriteJSON(w, http.StatusOK, PresetSaved{Name: s.Name, Warnings: orEmpty(warnings)})
}

func (p *Panel) handleDeleteBase(w http.ResponseWriter, r *http.Request) {
	ctx, me, name := r.Context(), UserOf(r), r.PathValue("name")
	src, ok := p.presets(w)
	if !ok {
		return
	}
	before := p.baseYAML(ctx, src, name)
	if err := src.DeleteBase(ctx, name); err != nil {
		p.presetError(w, err)
		return
	}
	p.audit(ctx, "panel.base.deleted", map[string]any{"user_id": me.ID, "base": name, "before": before, "ip": clientIP(r)})
	WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (p *Panel) handleDeleteSheet(w http.ResponseWriter, r *http.Request) {
	ctx, me, name := r.Context(), UserOf(r), r.PathValue("name")
	src, ok := p.presets(w)
	if !ok {
		return
	}
	before := p.sheetYAML(ctx, src, name)
	if err := src.DeleteSheet(ctx, name); err != nil {
		p.presetError(w, err)
		return
	}
	p.audit(ctx, "panel.sheet.deleted", map[string]any{"user_id": me.ID, "sheet": name, "before": before, "ip": clientIP(r)})
	WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func split(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// handlePresetsExport gives the file of the export: everything, or the selection
// (`bases` and `sheets`, separated by commas), as YAML or JSON. It holds no secret.
// Part of CA-ADM-05.
func (p *Panel) handlePresetsExport(w http.ResponseWriter, r *http.Request) {
	src, ok := p.presets(w)
	if !ok {
		return
	}
	q := r.URL.Query()
	sel := presets.Selection{Bases: split(q.Get("bases")), Sheets: split(q.Get("sheets"))}
	sel.All = len(sel.Bases) == 0 && len(sel.Sheets) == 0
	doc, err := src.Export(r.Context(), sel)
	if err != nil {
		p.presetError(w, err)
		return
	}
	var (
		body []byte
		ext  = "yaml"
		mime = "application/yaml; charset=utf-8"
	)
	if q.Get("format") == "json" {
		ext, mime = "json", "application/json; charset=utf-8"
		body, err = doc.JSON()
	} else {
		body, err = doc.YAML()
	}
	if err != nil {
		p.log.Error("panel: writing the export", "err", err)
		WriteError(w, errInternal())
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", `attachment; filename="mestre-presets.`+ext+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, bytes.NewReader(body))
}

func (p *Panel) planOf(w http.ResponseWriter, src PresetSource, r *http.Request, in *ImportFile) (*presets.Plan, bool) {
	doc, err := presets.ParseDocument([]byte(in.Content))
	if err != nil {
		p.presetError(w, err)
		return nil, false
	}
	plan, err := src.PlanImport(r.Context(), doc)
	if err != nil {
		p.presetError(w, err)
		return nil, false
	}
	return plan, true
}

// handleImportPlan checks the whole file and says which names already exist, so the
// Owner picks replace, rename or skip for each. It writes nothing.
func (p *Panel) handleImportPlan(w http.ResponseWriter, r *http.Request) {
	src, ok := p.presets(w)
	if !ok {
		return
	}
	var in ImportFile
	if !decode(w, r, &in) {
		return
	}
	plan, ok := p.planOf(w, src, r, &in)
	if !ok {
		return
	}
	out := ImportPlan{Bases: len(plan.Doc.Bases), Sheets: len(plan.Doc.Sheets), Conflicts: []ImportItem{}, Warnings: orEmpty(plan.Warnings)}
	for _, c := range plan.Conflicts {
		out.Conflicts = append(out.Conflicts, ImportItem{Kind: c.Kind, Name: c.Name})
	}
	WriteJSON(w, http.StatusOK, out)
}

// handleImport applies the file with the choices, all or nothing.
// Part of CA-ADM-05.
func (p *Panel) handleImport(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), UserOf(r)
	src, ok := p.presets(w)
	if !ok {
		return
	}
	var in ImportFile
	if !decode(w, r, &in) {
		return
	}
	plan, ok := p.planOf(w, src, r, &in)
	if !ok {
		return
	}
	choices := map[presets.Item]presets.Choice{}
	for _, c := range in.Choices {
		act := presets.Action(c.Action)
		if act != presets.Replace && act != presets.Rename && act != presets.Skip || c.Kind != "base" && c.Kind != "sheet" {
			WriteError(w, errBadRequest())
			return
		}
		choices[presets.Item{Kind: c.Kind, Name: c.Name}] = presets.Choice{Action: act, NewName: c.NewName}
	}
	res, err := src.ApplyImport(ctx, plan, choices)
	if err != nil {
		p.presetError(w, err)
		return
	}
	p.audit(ctx, "panel.presets.imported", map[string]any{"user_id": me.ID, "bases": len(plan.Doc.Bases), "sheets": len(plan.Doc.Sheets),
		"added": res.Added, "replaced": res.Replaced, "renamed": res.Renamed, "skipped": res.Skipped, "ip": clientIP(r)})
	WriteJSON(w, http.StatusOK, ImportResult{Added: res.Added, Replaced: res.Replaced, Renamed: res.Renamed, Skipped: res.Skipped})
}
