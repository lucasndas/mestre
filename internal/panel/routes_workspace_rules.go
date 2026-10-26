package panel

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/safefile"
	"github.com/mestre-ia/mestre/internal/sessionzero"
	"github.com/mestre-ia/mestre/internal/users"
)

func init() { RegisterArea("workspace-rules", mountWorkspaceRules) }

// The files of the workspace the screens read and write, in .mestre/regras/ of the main
// clone (docs/sessao-zero/workspace.md#estrutura-de-mestre).
const (
	policyFile  = "politica-de-aprovacao.yaml"
	ceilingFile = "teto.yaml"
	// maxInstructions is the largest text of the extra instructions
	// (docs/orquestracao/grao-mestre.md#instruções-extras-do-usuário); the engine does not
	// start a character whose file is larger, so the panel never writes one.
	maxInstructions = 4 << 10
	// diffLines bounds what the audit keeps of the lines that changed in a file.
	diffLines = 200
)

var instructionFile = map[string]string{"grand_master": "grao-mestre.md", "table_masters": "mestre-da-mesa.md"}

func mountWorkspaceRules(r *Router) {
	p := r.Panel()
	r.Require("GET /api/rules/workspaces", p.handleRuleWorkspaces, users.Status)
	r.Require("GET /api/workspaces/{workspace}/ceiling", p.handleCeiling, users.Status)
	r.Require("POST /api/workspaces/{workspace}/ceiling/widen", p.handleWiden, users.RaiseCeiling)
	r.Require("GET /api/workspaces/{workspace}/policy", p.handlePolicy, users.BasesVaultPolicy)
	r.Require("PUT /api/workspaces/{workspace}/policy", p.handleSavePolicy, users.BasesVaultPolicy)
	r.Require("GET /api/workspaces/{workspace}/instructions", p.handleInstructions, users.ConfigureWorkspace)
	r.Require("PUT /api/workspaces/{workspace}/instructions", p.handleSaveInstructions, users.ConfigureWorkspace)
}

// fileMu makes the read, the comparison and the write of a file of the workspace one step.
var fileMu sync.Mutex

func (p *Panel) handleRuleWorkspaces(w http.ResponseWriter, r *http.Request) {
	slugs, err := p.actingWorkspaces(r.Context(), UserOf(r))
	if err != nil {
		WriteError(w, errInternal())
		return
	}
	WriteJSON(w, http.StatusOK, RuleWorkspaces{Workspaces: listOf(slugs)})
}

// ruleDir is the folder of the rules of a workspace of the person, or the answer that
// there is none.
func (p *Panel) ruleDir(w http.ResponseWriter, r *http.Request) (string, bool) {
	b, ok := p.backend(w)
	if !ok {
		return "", false
	}
	ws := r.PathValue("workspace")
	if !p.workspaceOf(w, r, ws) {
		return "", false
	}
	root := b.Principal(ws)
	if root == "" || !filepath.IsAbs(root) {
		WriteError(w, Err(CodeNoFolder, "o workspace ainda não tem a pasta principal", nil))
		return "", false
	}
	return filepath.Join(root, sessionzero.MestreDir, "regras"), true
}

func versionOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// readRule reads a file under the rules; a file that does not exist is (nil, nil).
func readRule(path string, limit int64) ([]byte, error) {
	data, _, err := readRuleInfo(path, limit)
	return data, err
}

// readRuleInfo is readRule with the state of the file it read, for the guard of the write.
func readRuleInfo(path string, limit int64) ([]byte, safefile.Info, error) {
	data, info, err := safefile.Read(path, limit)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, safefile.Info{}, nil
	}
	return data, info, err
}

// errChangedOnDisk is what the guard of a write says: the file is not the one that was read.
var errChangedOnDisk = errors.New("the file changed on disk since it was read")

// unchangedSince stops the write when the file is not the version that was read, checked
// right before the rename: an edit made by hand in between is never overwritten.
func unchangedSince(read safefile.Info) safefile.Options {
	return safefile.Options{Sync: true, Guard: func(cur safefile.Info) error {
		if !cur.Same(read) {
			return errChangedOnDisk
		}
		return nil
	}}
}

// fileError answers for a file the engine does not touch or that is too large.
func (p *Panel) fileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errChangedOnDisk):
		WriteError(w, Err(CodeFileChanged, "o arquivo mudou desde que você o abriu; abra de novo", nil))
	case errors.Is(err, safefile.ErrTooLarge):
		WriteError(w, Err(CodeTooLarge, "o arquivo é maior do que o limite", nil))
	case safefile.Refused(err):
		WriteError(w, Err(CodeFileRefused, "o arquivo é um link ou não é um arquivo comum; o Mestre não o toca", nil))
	default:
		p.log.Error("panel: a file of the rules", "err", err)
		WriteError(w, errInternal())
	}
}

func (p *Panel) handleCeiling(w http.ResponseWriter, r *http.Request) {
	dir, ok := p.ruleDir(w, r)
	if !ok {
		return
	}
	b, ctx, me := p.rulesBackend(), r.Context(), UserOf(r)
	ws := r.PathValue("workspace")
	data, err := readRule(filepath.Join(dir, ceilingFile), chain.MaxPolicyBytes)
	if err != nil {
		p.fileError(w, err)
		return
	}
	out := CeilingView{Workspace: ws, Present: data != nil, Text: string(data), Widenings: []DecreeRow{}, CanWiden: users.Can(me.Level, users.RaiseCeiling)}
	if data != nil {
		if _, err := classes.ParseCeiling(data); err != nil {
			out.Error = err.Error()
		} else {
			out.Valid = true
		}
	}
	views, err := b.ListWorkspace(ctx, ws, false)
	if err != nil {
		WriteError(w, errInternal())
		return
	}
	for _, v := range views {
		if v.Effect.Kind != decrees.EffectCeiling {
			continue
		}
		row, err := p.decreeRow(ctx, b, me, v)
		if err != nil {
			WriteError(w, errInternal())
			return
		}
		out.Widenings = append(out.Widenings, row)
	}
	WriteJSON(w, http.StatusOK, out)
}

// handleWiden opens the proposal of a decree that widens the ceiling. It never edits the
// file: the ceiling changes only when the author confirms the decree, with the PIN.
func (p *Panel) handleWiden(w http.ResponseWriter, r *http.Request) {
	b, ok := p.backend(w)
	if !ok {
		return
	}
	ws := r.PathValue("workspace")
	if !p.workspaceOf(w, r, ws) {
		return
	}
	var in WidenInput
	if !decode(w, r, &in) {
		return
	}
	ctx, me := r.Context(), UserOf(r)
	value, text := strings.TrimSpace(in.Value), strings.TrimSpace(in.Text)
	if !slices.Contains(permissionFields, in.Field) || value == "" || text == "" || len(text) > maxOrder {
		WriteError(w, errBadRequest())
		return
	}
	pending, err := b.Propose(ctx, decrees.Proposal{Author: me.ID, Kind: decrees.KindRule, Workspace: ws,
		Effect: decrees.Effect{Kind: decrees.EffectCeiling, Allow: grantOf(in.Field, value)},
		Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: ws}, Text: text, Origin: decrees.Origin{Channel: "panel"}})
	if err != nil {
		p.decreeError(w, err)
		return
	}
	p.audit(ctx, "panel.ceiling.widen_proposed", map[string]any{"user_id": me.ID, "workspace": ws, "proposal": pending.ID,
		"after": map[string]any{"allow": map[string]any{in.Field: value}, "pin": pending.PIN}, "ip": clientIP(r)})
	WriteJSON(w, http.StatusCreated, DecreeProposal{ID: pending.ID, Text: pending.Text, Order: pending.Order, PIN: pending.PIN, Why: pending.Why})
}

func (p *Panel) handlePolicy(w http.ResponseWriter, r *http.Request) {
	dir, ok := p.ruleDir(w, r)
	if !ok {
		return
	}
	data, err := readRule(filepath.Join(dir, policyFile), chain.MaxPolicyBytes)
	if err != nil {
		p.fileError(w, err)
		return
	}
	out := PolicyFile{Workspace: r.PathValue("workspace")}
	if data == nil {
		data, out.Default = []byte(sessionzero.DefaultPolicyYAML), true
	}
	out.Text, out.Version = string(data), versionOf(data)
	WriteJSON(w, http.StatusOK, out)
}

var yamlLine = regexp.MustCompile(`line (\d+)`)

// policyErrorLine is the line of the file an error of the policy points at: the one the YAML
// reader names, or, for a rule of the policy that is broken, the line of the key it is about.
func policyErrorLine(data []byte, err error) int {
	if m := yamlLine.FindStringSubmatch(err.Error()); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	var se *chain.SectionError
	if !errors.As(err, &se) {
		return 0
	}
	key := se.Item
	if i := strings.LastIndexAny(key, ".:/ "); i >= 0 {
		key = key[i+1:]
	}
	if key == "" {
		return 0
	}
	for i, l := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, key+":") || strings.HasPrefix(t, "- "+key+":") {
			return i + 1
		}
	}
	return 0
}

// changedLines is the lines that left and the ones that came, for the audit.
func changedLines(before, after string) (removed, added []string) {
	count := func(s string) map[string]int {
		m := map[string]int{}
		for _, l := range strings.Split(s, "\n") {
			m[l]++
		}
		return m
	}
	b, a := count(before), count(after)
	for _, l := range strings.Split(before, "\n") {
		if a[l] > 0 {
			a[l]--
		} else if len(removed) < diffLines {
			removed = append(removed, l)
		}
	}
	for _, l := range strings.Split(after, "\n") {
		if b[l] > 0 {
			b[l]--
		} else if len(added) < diffLines {
			added = append(added, l)
		}
	}
	return removed, added
}

func plainText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

func (p *Panel) handleSavePolicy(w http.ResponseWriter, r *http.Request) {
	dir, ok := p.ruleDir(w, r)
	if !ok {
		return
	}
	var in PolicyInput
	if !decode(w, r, &in) {
		return
	}
	ctx, me, ws := r.Context(), UserOf(r), r.PathValue("workspace")
	if len(in.Text) > chain.MaxPolicyBytes {
		WriteError(w, Err(CodeTooLarge, "o arquivo é maior do que o limite", map[string]any{"max_bytes": chain.MaxPolicyBytes}))
		return
	}
	data := []byte(in.Text)
	if !plainText(in.Text) {
		WriteError(w, Err(CodeInvalidPolicy, "o arquivo precisa ser texto UTF-8", map[string]any{"line": 0}))
		return
	}
	// validated whole before anything is written: an invalid file saves nothing
	if _, _, err := chain.ParsePolicyFile(data); err != nil {
		line := policyErrorLine(data, err)
		p.audit(ctx, "panel.policy.refused", map[string]any{"user_id": me.ID, "workspace": ws, "line": line, "ip": clientIP(r)})
		WriteError(w, Err(CodeInvalidPolicy, "arquivo de política inválido: "+err.Error(), map[string]any{"line": line, "reason": err.Error()}))
		return
	}
	path := filepath.Join(dir, policyFile)
	fileMu.Lock()
	defer fileMu.Unlock()
	current, read, err := readRuleInfo(path, chain.MaxPolicyBytes)
	if err != nil {
		p.fileError(w, err)
		return
	}
	if current == nil {
		current = []byte(sessionzero.DefaultPolicyYAML)
	}
	if versionOf(current) != in.Version {
		WriteError(w, Err(CodeFileChanged, "o arquivo mudou desde que você o abriu; abra de novo", nil))
		return
	}
	if err := safefile.Write(path, data, unchangedSince(read)); err != nil {
		p.fileError(w, err)
		return
	}
	removed, added := changedLines(string(current), in.Text)
	p.audit(ctx, "panel.policy.saved", map[string]any{"user_id": me.ID, "workspace": ws,
		"before": map[string]any{"version": versionOf(current), "removed": removed},
		"after":  map[string]any{"version": versionOf(data), "added": added}, "ip": clientIP(r)})
	WriteJSON(w, http.StatusOK, PolicyFile{Workspace: ws, Text: in.Text, Version: versionOf(data)})
}

func (p *Panel) readInstructions(dir string) (Instructions, error) {
	out := Instructions{MaxBytes: maxInstructions}
	for role, name := range instructionFile {
		data, err := readRule(filepath.Join(dir, "instrucoes", name), maxInstructions)
		if err != nil {
			return out, err
		}
		if role == "grand_master" {
			out.GrandMaster = string(data)
		} else {
			out.TableMasters = string(data)
		}
	}
	return out, nil
}

func (p *Panel) handleInstructions(w http.ResponseWriter, r *http.Request) {
	dir, ok := p.ruleDir(w, r)
	if !ok {
		return
	}
	out, err := p.readInstructions(dir)
	if err != nil {
		p.fileError(w, err)
		return
	}
	out.Workspace = r.PathValue("workspace")
	WriteJSON(w, http.StatusOK, out)
}

func (p *Panel) handleSaveInstructions(w http.ResponseWriter, r *http.Request) {
	dir, ok := p.ruleDir(w, r)
	if !ok {
		return
	}
	var in InstructionsInput
	if !decode(w, r, &in) {
		return
	}
	name, known := instructionFile[in.Role]
	if !known || !plainText(in.Text) {
		WriteError(w, errBadRequest())
		return
	}
	if len(in.Text) > maxInstructions {
		WriteError(w, Err(CodeTooLarge, "o texto passa de 4 KiB", map[string]any{"max_bytes": maxInstructions}))
		return
	}
	ctx, me, ws := r.Context(), UserOf(r), r.PathValue("workspace")
	path := filepath.Join(dir, "instrucoes", name)
	fileMu.Lock()
	defer fileMu.Unlock()
	before, read, err := readRuleInfo(path, maxInstructions)
	if err != nil {
		p.fileError(w, err)
		return
	}
	if err := safefile.Write(path, []byte(in.Text), unchangedSince(read)); err != nil {
		p.fileError(w, err)
		return
	}
	p.audit(ctx, "panel.instructions.saved", map[string]any{"user_id": me.ID, "workspace": ws, "role": in.Role,
		"before": map[string]any{"text": string(before)}, "after": map[string]any{"text": in.Text}, "ip": clientIP(r)})
	out, err := p.readInstructions(dir)
	if err != nil {
		p.fileError(w, err)
		return
	}
	out.Workspace = ws
	WriteJSON(w, http.StatusOK, out)
}
