package classes

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/providers"
)

// DefaultProviderHookSeconds is the time limit the hook files of the
// providers give `mestre hook`; a synchronous hook of a class is cut to
// ProviderMargin below it.
const DefaultProviderHookSeconds = 30

// SyncTimeout is the time limit a synchronous hook runs with: what the class
// declared, cut to 2 s below the time limit of the provider.
func SyncTimeout(declaredSeconds, providerSeconds int) time.Duration {
	return time.Duration(max(min(declaredSeconds, providerSeconds-ProviderMargin), 1)) * time.Second
}

// ProviderReport is the section of `mestre class check` for one provider.
type ProviderReport struct {
	Provider providers.ID `json:"provider"`
	Name     string       `json:"name"`
	// Native is how the permissions become native controls: the arguments
	// and files the adapter produces, and what each holds.
	Native []string `json:"native"`
	// DispatcherOnly is what stays with the dispatcher alone.
	DispatcherOnly []string `json:"dispatcher_only"`
	// Hooks are the hooks of the class that run with this provider, and
	// NotRunning those that do not, with the reason.
	Hooks      []string `json:"hooks"`
	NotRunning []string `json:"not_running"`
	// Notes are the rest: reduced monitoring, a time limit that is cut.
	Notes []string `json:"notes,omitempty"`
}

// Report is the result of `mestre class check`.
type Report struct {
	Class string     `json:"class"`
	Scope ClassScope `json:"scope"`
	Table string     `json:"table,omitempty"`
	// State says which version was checked and where it stands against the
	// approved one.
	State string `json:"state"`
	Hash  string `json:"hash"`
	// Problems make the class invalid; Warnings do not.
	Problems []Problem `json:"problems"`
	Warnings []Problem `json:"warnings"`
	// Ceiling is the check against the ceiling of the workspace; nil when the
	// permissions could not be read.
	Ceiling   *CeilingCheck    `json:"ceiling,omitempty"`
	App       AppReport        `json:"app"`
	Providers []ProviderReport `json:"providers"`
	// Proposal is the pending proposal of the class, if any.
	Proposal *int64 `json:"proposal,omitempty"`
}

// Valid reports whether the class has no problem.
func (r Report) Valid() bool { return len(r.Problems) == 0 }

// AboveCeiling reports whether the class passes the ceiling of today: the
// pipeline cuts what passes, nothing is deleted.
func (r Report) AboveCeiling() bool { return r.Ceiling != nil && r.Ceiling.Above() }

// String is the text of the report.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "classe %s (%s): %s\n", r.Class, scopeLabel(r.Scope), r.State)
	if r.Valid() {
		b.WriteString("pacote válido\n")
	} else {
		fmt.Fprintf(&b, "%d problema(s):\n", len(r.Problems))
		for _, p := range r.Problems {
			b.WriteString("  - " + p.String() + "\n")
		}
	}
	for _, w := range r.Warnings {
		b.WriteString("  aviso: " + w.String() + "\n")
	}
	if r.Ceiling != nil {
		b.WriteString("teto: " + strings.ReplaceAll(strings.TrimPrefix(r.Ceiling.String(), "classe "+r.Class+": "), "\n", "\n  ") + "\n")
	}
	b.WriteString(r.App.String() + "\n")
	for _, p := range r.Providers {
		b.WriteString(p.Name + "\n")
		for _, l := range p.Native {
			b.WriteString("  controle nativo: " + l + "\n")
		}
		for _, l := range p.DispatcherOnly {
			b.WriteString("  só no despachante: " + l + "\n")
		}
		for _, l := range p.Hooks {
			b.WriteString("  hook que roda: " + l + "\n")
		}
		for _, l := range p.NotRunning {
			b.WriteString("  hook que não roda: " + l + "\n")
		}
		for _, l := range p.Notes {
			b.WriteString("  nota: " + l + "\n")
		}
	}
	return b.String()
}

var providerNames = []struct {
	id   providers.ID
	name string
}{{providers.ClaudeCode, "Claude Code"}, {providers.Codex, "Codex"}, {providers.Antigravity, "Antigravity"}}

// Check validates a class by name, as `mestre class check "<classe>"` does,
// from the folder on disk (what people edit): the package, the ceiling, the
// App and, for each provider, how the permissions become native controls and
// which hooks do not run there. A folder that differs from the approved
// version is recorded as a proposal, like at the start of a turn.
func (l *Library) Check(ctx context.Context, name, table string) (Report, error) {
	for _, s := range scopes(table) {
		k := ClassKey{Scope: s.Scope, Table: s.Table, Name: name}
		rec, approved, err := l.Records.Approved(ctx, l.ws(k.Scope), k)
		if err != nil {
			return Report{}, err
		}
		var rp *ClassRecord
		if approved {
			rp = &rec
		}
		e, err := l.reconcile(ctx, k, rp)
		if err != nil {
			return Report{}, err
		}
		if !approved && e.State != EntryNew {
			continue // no such class in this scope
		}
		files := rec.Files
		if dir, err := l.Roots.Dir(k); err == nil {
			if on, err := ReadDir(dir); err == nil {
				files = on
			}
		}
		if files == nil {
			continue
		}
		state := "versão aprovada"
		switch e.State {
		case EntryPending:
			state = "alterada fora do Mestre: vale a última versão aprovada e há uma proposta pendente"
		case EntryNew:
			state = "ainda sem versão aprovada: só há uma proposta"
		case EntryMissing:
			state = "a pasta sumiu: vale a última versão aprovada"
		case EntryUnreadable:
			state = "pasta ilegível como pacote: vale a última versão aprovada"
		}
		rep, err := l.CheckFiles(ctx, files, k)
		if err != nil {
			return Report{}, err
		}
		rep.State = state
		if e.Proposal != nil {
			rep.Proposal = &e.Proposal.ID
		}
		return rep, nil
	}
	return Report{}, fmt.Errorf("%w: %q", ErrNotVisible, name)
}

// CheckFiles is the check of a package that has not been saved yet, or of
// the files of a saved one.
func (l *Library) CheckFiles(ctx context.Context, files Files, k ClassKey) (Report, error) {
	ex := Examine(files, ExamineOptions{Scope: k.Scope, Dir: k.Name, Skill: l.Skill})
	rep := Report{
		Class: k.Name, Scope: k.Scope, Table: k.Table, State: "pacote sem versão aprovada", Hash: ex.Class.Hash,
		Problems: ex.Problems, Warnings: ex.Warnings,
	}
	if rep.Problems == nil {
		rep.Problems = []Problem{}
	}
	if rep.Warnings == nil {
		rep.Warnings = []Problem{}
	}
	c := ex.Class
	ceil, err := l.ceiling(ctx)
	if err != nil {
		return Report{}, err
	}
	if c.Permissions != nil {
		cc := CheckClass(k.Name, c.Permissions, ceil)
		rep.Ceiling = &cc
	}
	if c.App.Kind != "" {
		rep.App = l.appReport(ctx, c, k.Table)
		rep.Problems = append(rep.Problems, l.appProblems(ctx, c, k)...)
	} else {
		rep.App = AppReport{Link: c.Meta.App, Label: "sem vínculo válido"}
	}
	osName := l.OS
	if osName == "" {
		osName = runtime.GOOS
	}
	rep.Providers = l.providerReports(c, ceil, k, osName, &rep)
	return rep, nil
}

func (l *Library) providerReports(c *Class, ceil *Ceiling, k ClassKey, goos string, rep *Report) []ProviderReport {
	var rules *Rules
	var env Env
	wt, home := checkRoot(goos, "worktree"), checkRoot(goos, "perfil")
	if c.Permissions != nil {
		r, err := Build(Layers{
			AgentID: "check", Class: k.Name, Permissions: c.Permissions, Ceiling: ceil.Maximum(),
			Placeholders: map[string]string{"mesa.branch": "mesa"}, Now: l.Clock.Now(),
		})
		if err != nil {
			rep.Problems = append(rep.Problems, Problem{PermissionsFile, "regras efetivas: " + err.Error()})
		} else {
			rules = r
			env = Env{AgentID: "check", Worktree: wt, OS: goos}
		}
	}
	reg := providers.NewRegistry(providers.Options{})
	var out []ProviderReport
	for _, pn := range providerNames {
		pr := ProviderReport{Provider: pn.id, Name: pn.name, Native: []string{}, DispatcherOnly: []string{}, Hooks: []string{}, NotRunning: []string{}}
		ad, err := reg.For(pn.id)
		if err != nil {
			pr.Notes = append(pr.Notes, err.Error())
			out = append(out, pr)
			continue
		}
		if ad.Info().Reduced != "" {
			pr.Notes = append(pr.Notes, "monitoramento reduzido: este provedor não tem todos os eventos do Mestre (ver o mapa de eventos do adaptador)")
		}
		if rules != nil {
			pr.Native, pr.DispatcherOnly = nativeLines(ad, rules, pn.id, goos, wt, home)
		}
		for _, ev := range HookEvents {
			for _, h := range c.Hooks[ev] {
				label := fmt.Sprintf("%s/%s (%s)", ev, h.Name, h.Mode)
				switch {
				case ProviderLacks(pn.id, ev):
					pr.NotRunning = append(pr.NotRunning, fmt.Sprintf("%s/%s: %s não tem o evento %s", ev, h.Name, pn.name, ev))
				default:
					pr.Hooks = append(pr.Hooks, label)
				}
			}
		}
		out = append(out, pr)
	}
	for _, ev := range HookEvents {
		for _, h := range c.Hooks[ev] {
			if h.Mode == ModeSync && h.Timeout > DefaultProviderHookSeconds-ProviderMargin {
				rep.Warnings = append(rep.Warnings, Problem{fmt.Sprintf("%s %s %q", HooksFile, ev, h.Name),
					fmt.Sprintf("`timeout` de %d s: um hook sync roda com no máximo %d s, 2 s abaixo dos %d s que os provedores dão ao `mestre hook`",
						h.Timeout, DefaultProviderHookSeconds-ProviderMargin, DefaultProviderHookSeconds)})
			}
		}
	}
	if rules != nil {
		rep.Warnings = append(rep.Warnings, l.hookPipeline(c, rules, env, goos)...)
	}
	return out
}

// hookPipeline warns of a hook whose `run` the permissions of the class
// would not allow: the run of each hook passes through the same pipeline as
// a command of the character, so a hook the pipeline refuses never runs.
func (l *Library) hookPipeline(c *Class, rules *Rules, env Env, goos string) []Problem {
	var out []Problem
	for _, ev := range HookEvents {
		for _, h := range c.Hooks[ev] {
			cmd := h.Command(goos)
			d := Decide(context.Background(), env, rules, providers.Event{
				Version: providers.EventVersion, Event: providers.EventPreTool, AgentID: "check", Cwd: env.Worktree,
				Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: cmd},
			})
			if d.Verdict != Allow {
				out = append(out, Problem{
					fmt.Sprintf("%s %s %q", HooksFile, ev, h.Name),
					fmt.Sprintf("o `run` seria %s pelo pipeline de permissões da classe (%s); o hook não roda: acrescente o comando a `commands.allow`", verdictWord(d.Verdict), d.Reason),
				})
			}
		}
	}
	return out
}

func verdictWord(v Verdict) string {
	if v == Ask {
		return "submetido a aprovação"
	}
	return "negado"
}

func checkRoot(goos, name string) string {
	if goos == "windows" {
		return `C:\mestre-check\` + name
	}
	return "/mestre-check/" + name
}

// nativeLines describes what the native controls of a provider hold of the
// rules: the generic content (writing, denied files, commands, network) and
// the arguments and files the adapter produces for this provider.
func nativeLines(ad providers.Adapter, r *Rules, id providers.ID, goos, wt, home string) (native, dispatcher []string) {
	ch := NativeCharacter{AgentID: "check", Provider: id, OS: goos, Worktree: wt, ProfileHome: home}
	pol := NativePolicy(r, ch, providers.NativeFallbacks{})
	switch {
	case pol.NoWrite:
		native = append(native, "escrita: nada")
	default:
		native = append(native, "escrita: "+strings.Join(pol.Write, ", "))
	}
	native = append(native, fmt.Sprintf("arquivos negados: %d; comandos permitidos: %d; comandos negados: %d",
		len(pol.DenyFiles), len(pol.AllowCommands), len(pol.DenyCommands)))
	switch {
	case pol.Offline:
		native = append(native, "rede: sem internet")
	case pol.AnyDomain:
		native = append(native, "rede: qualquer domínio")
	default:
		native = append(native, "rede: "+strings.Join(pol.Domains, ", "))
	}
	if t, ok := providers.NativeOf(ad); ok {
		ctl, err := t.Native(pol)
		if err != nil {
			native = append(native, "erro ao traduzir: "+err.Error())
		} else {
			if len(ctl.Args) > 0 {
				native = append(native, "argumentos: "+strings.Join(ctl.Args, " "))
			}
			for _, f := range ctl.Files {
				p := strings.NewReplacer(wt, "<worktree>", home, "<perfil>").Replace(f.Path)
				native = append(native, "arquivo: "+filepath.ToSlash(p))
			}
		}
	} else {
		native = append(native, "este provedor não tem controles nativos")
	}
	if n := len(r.Commands.Ask); n > 0 {
		dispatcher = append(dispatcher, fmt.Sprintf("%d padrão(ões) de comando em ask (pedem aprovação)", n))
	}
	if n := len(r.Tools.Ask) + len(r.Tools.Allow) + len(r.Tools.Deny); n > 0 || r.Tools.Default != Deny {
		dispatcher = append(dispatcher, fmt.Sprintf("ferramentas mcp e outras: %d padrão(ões), restante em %s", n, r.Tools.Default))
	}
	if codexGap, agyGap := providers.CodexNoNativeNetwork(pol), providers.AntigravityNoNativeNetwork(pol); (id == providers.Codex && codexGap) || (id == providers.Antigravity && agyGap) {
		dispatcher = append(dispatcher, "internet: sem reforço nativo de rede neste sistema (a rede do provedor fica ligada; só o despachante a barra)")
	}
	if id == providers.Codex && providers.CodexNoNativeWrite(pol) {
		dispatcher = append(dispatcher, "escrita: sem barreira nativa de escrita (o sandbox do Codex fica em workspace-write para a CLI alcançar o motor; só o despachante barra a escrita)")
	}
	dispatcher = append(dispatcher, "git, rede por comando de shell, CLI e decretos")
	slices.Sort(dispatcher)
	return native, dispatcher
}
