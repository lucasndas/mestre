package presets

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/providers"
)

// ErrInvalid wraps every refusal when a base or a sheet is saved; the text
// says why, in Portuguese, for the Owner.
var ErrInvalid = errors.New("preset recusado")

var (
	baseNameRe = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
	envNameRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	varRe      = regexp.MustCompile(`\{([^{}]*)\}`)
	// AccountNameRe is the name of an account profile; it is also a folder
	// name under contas, so nothing else is accepted.
	accountNameRe = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
)

var shells = []string{"bash", "pwsh", "powershell", "cmd"}

// The variables of `cwd`.
const (
	VarMesa      = "mesa"
	VarWorkspace = "workspace"
	VarHome      = "home"
)

type problems []string

func (p *problems) add(format string, a ...any) { *p = append(*p, fmt.Sprintf(format, a...)) }

func (p problems) err() error {
	if len(p) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalid, strings.Join(p, "; "))
}

// secretLike reports whether a value would be masked as a secret.
func secretLike(masker *logging.Masker, v string) bool { return masker.Mask(v) != v }

func variants(b Base) [][]string {
	out := [][]string{b.Args}
	for _, p := range b.Platforms {
		if p.Args != nil {
			out = append(out, p.Args)
		}
	}
	return out
}

func commands(b Base) []string {
	out := []string{b.Command}
	for _, p := range b.Platforms {
		if p.Command != "" {
			out = append(out, p.Command)
		}
	}
	return out
}

// providerOf is the provider a base starts, from its command, when it is
// one of the three.
func providerOf(b Base) (providers.ID, bool) {
	for _, c := range commands(b) {
		if p, ok := providers.InferProvider(c); ok {
			return p, true
		}
	}
	return providers.Null, false
}

// isAgentBase: a base the Mestre knows starts an agent, or waits for the
// hooks of one.
func isAgentBase(b Base) bool {
	_, known := providerOf(b)
	return known || b.Ready.Mode == ReadyHook
}

func envList(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	slices.Sort(out)
	return out
}

// cwdInsideMesa: the empty cwd is {mesa}; otherwise it starts with {mesa}
// and does not climb out of it.
func cwdInsideMesa(cwd string) bool {
	if cwd == "" {
		return true
	}
	rest, ok := strings.CutPrefix(cwd, "{"+VarMesa+"}")
	if !ok || (rest != "" && rest[0] != '/' && rest[0] != '\\') {
		return false
	}
	return !slices.Contains(strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' }), "..") && !varRe.MatchString(rest)
}

// ValidateBase checks a base by itself, as it is saved
// (docs/orquestracao/bases-e-fichas.md#regras-e-erros): fields, platforms,
// the start folder, the variables and arguments the provider's list allows,
// values that look like secrets, `--chrome` outside the Operator of the PC
// and the requirements, with the interface requirement only in the base
// `operador`. The sheets that use it are checked by ValidateSheet.
func ValidateBase(b Base) error {
	var p problems
	if !baseNameRe.MatchString(b.Name) {
		p.add("nome `%s`: só letras minúsculas, dígitos e `-`, de 1 a 64 caracteres", b.Name)
	}
	if strings.TrimSpace(b.Command) == "" || strings.ContainsAny(b.Command, "\x00\r\n") {
		p.add("`command` é obrigatório")
	}
	if len(b.Platforms) == 0 {
		p.add("a base não tem nenhuma plataforma; `platforms` lista os sistemas onde ela roda")
	}
	seen := map[string]bool{}
	for _, pl := range b.Platforms {
		switch {
		case pl.OS != Linux && pl.OS != Windows:
			p.add("plataforma `%s`: `os` aceita `linux` ou `windows`", pl.OS)
		case seen[pl.OS]:
			p.add("plataforma `%s` repetida", pl.OS)
		}
		seen[pl.OS] = true
		if strings.ContainsAny(pl.Command, "\x00\r\n") {
			p.add("plataforma `%s`: `command` inválido", pl.OS)
		}
	}
	if b.Shell != "" && !slices.Contains(shells, b.Shell) {
		p.add("`shell` aceita %s", quote(shells))
	}
	p = append(p, checkCwd(b)...)
	p = append(p, checkReady(b.Ready)...)
	p = append(p, checkMachines(b.Machines)...)

	provider, known := providerOf(b)
	agent := isAgentBase(b)
	for k := range b.Env {
		if !envNameRe.MatchString(k) {
			p.add("variável `%s`: nome inválido", k)
		}
	}
	masker := logging.NewMasker()
	for _, kv := range envList(b.Env) {
		if k, v, _ := strings.Cut(kv, "="); secretLike(masker, v) {
			p.add("variável `%s`: o valor parece um segredo; segredos ficam no cofre, nunca na base", k)
		}
	}
	for _, args := range variants(b) {
		for _, a := range args {
			if secretLike(masker, a) {
				p.add("argumento `%s` parece um segredo; segredos não vão em argumento de linha de comando", logging.Redacted)
			}
			if isChromeFlag(a) && b.Name != OperatorBase {
				p.add("a opção `--chrome` só vale na base `%s`; nenhuma outra base vira Operador do PC", OperatorBase)
			}
		}
	}
	check := providers.Null
	switch {
	case known:
		check = provider
	case agent:
		check = providers.Agent
	}
	for _, args := range variants(b) {
		if err := providers.CheckBase(check, args, envList(b.Env)); err != nil {
			p.add("%v", err)
			break
		}
	}
	if _, err := machinesheet.ParseRequires(b.Requires, machinesheet.Scope{OperatorBase: b.Name == OperatorBase}); err != nil {
		p.add("%v", err)
	}
	return p.err()
}

func isChromeFlag(a string) bool { return a == "--chrome" || strings.HasPrefix(a, "--chrome=") }

func checkCwd(b Base) (p problems) {
	for _, m := range varRe.FindAllStringSubmatch(b.Cwd, -1) {
		if !slices.Contains([]string{VarMesa, VarWorkspace, VarHome}, m[1]) {
			p.add("`cwd`: variável `{%s}` desconhecida; as aceitas são `{mesa}`, `{workspace}` e `{home}`", m[1])
		}
	}
	if strings.ContainsAny(b.Cwd, "\x00\r\n") {
		p.add("`cwd` inválido")
	}
	if isAgentBase(b) && b.Name != OperatorBase && !cwdInsideMesa(b.Cwd) {
		p.add("`cwd` `%s` fica fora de `{mesa}`; numa base de agente a pasta inicial é a mesa ou uma pasta dentro dela", b.Cwd)
	}
	return p
}

func checkReady(r Ready) (p problems) {
	switch r.Mode {
	case ReadyHook:
	case ReadyPattern:
		if r.Pattern == "" {
			p.add("`ready.pattern` vazio")
		} else if _, err := regexp.Compile(r.Pattern); err != nil {
			p.add("`ready.pattern` não é uma expressão regular válida: %v", err)
		}
	case ReadyDelay:
		if r.Delay <= 0 || r.Delay > 3600 {
			p.add("`ready.delay` é de 1 a 3600 segundos")
		}
	default:
		p.add("`ready` é obrigatório: `hook`, `{pattern: <regex>}` ou `{delay: <segundos>}`")
	}
	return p
}

func checkMachines(ids []string) (p problems) {
	seen := map[string]bool{}
	for _, id := range ids {
		switch {
		case strings.TrimSpace(id) == "":
			p.add("`machines` tem um id vazio")
		case seen[id]:
			p.add("`machines` repete `%s`", id)
		}
		seen[id] = true
	}
	return p
}

func quote(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = "`" + s + "`"
	}
	return strings.Join(q, ", ")
}

// SheetContext is what a sheet is checked against.
type SheetContext struct {
	// Base is the base the sheet names.
	Base Base
	// Class is the sheet's class as the Guild has it; nil when the sheet has
	// none or it was not found (ClassMissing).
	Class *classes.Class
	// ClassMissing is true when the sheet names a class that was not found.
	ClassMissing bool
}

// ValidateSheet checks a sheet against its base and class, as it is saved
// (docs/orquestracao/bases-e-fichas.md#ficha). It returns the warnings of
// what is saved but has no effect (an allow the class does not give).
func ValidateSheet(s Sheet, c SheetContext) (warnings []string, err error) {
	var p problems
	name := strings.TrimSpace(s.Name)
	if name == "" || name != s.Name || len([]rune(name)) > 80 || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		p.add("nome `%s`: de 1 a 80 caracteres, sem espaços nas pontas nem caracteres de controle", s.Name)
	}
	if s.Base == "" || s.Base != c.Base.Name {
		p.add("a base `%s` não existe", s.Base)
	}
	if !slices.Contains(Agents(), s.Agent) {
		p.add("`agent` aceita %s", quote(Agents()))
	}
	provider := providers.Null
	if s.Agent != AgentShell && slices.Contains(Agents(), s.Agent) {
		provider = providers.ID(s.Agent)
	}
	b := c.Base
	if b.Ready.Mode == ReadyHook && s.Agent == AgentShell {
		p.add("a base `%s` espera o hook (`ready: hook`), que um terminal `shell` não dispara; use outro agente ou outra base", b.Name)
	}
	if inferred, ok := providerOf(b); ok && provider != providers.Null && inferred != provider {
		p.add("a base `%s` inicia o %s e a ficha pede o agente `%s`", b.Name, inferred, s.Agent)
	}
	if provider == providers.Null && isKnownAgentBase(b) {
		p.add("a base `%s` inicia um agente e a ficha é de `shell`", b.Name)
	}
	if s.Base == c.Base.Name {
		for _, args := range variants(b) {
			if err := providers.CheckBase(provider, args, envList(b.Env)); err != nil {
				p.add("base `%s` com o agente `%s`: %v", b.Name, s.Agent, err)
				break
			}
		}
		if provider != providers.Null && b.Name != OperatorBase && !cwdInsideMesa(b.Cwd) {
			p.add("a base `%s` começa fora de `{mesa}`, e o agente de uma ficha trabalha dentro dela", b.Name)
		}
	}
	switch s.Lifecycle {
	case "", Ephemeral, Persistent:
	default:
		p.add("`lifecycle` aceita `ephemeral` ou `persistent`")
	}
	if s.MaxIdle != "" {
		if d, perr := time.ParseDuration(s.MaxIdle); perr != nil || d <= 0 {
			p.add("`max_idle` `%s`: use uma duração como `240m`", s.MaxIdle)
		}
	}
	if s.MaxCopies != nil && *s.MaxCopies < 1 {
		p.add("`max_copies` é um inteiro a partir de 1")
	}
	for _, sk := range s.ExtraSkills {
		if strings.TrimSpace(sk) == "" {
			p.add("`extra_skills` tem um nome vazio")
		}
	}
	if s.Account != "" {
		switch {
		case !accountNameRe.MatchString(s.Account):
			p.add("`account` `%s`: nome de perfil inválido (letras minúsculas, dígitos e `-`)", s.Account)
		case s.Agent == AgentShell:
			p.add("`account` não vale para um terminal `shell`, que não usa conta de provedor")
		}
	}
	p = append(p, sheetClass(s, c)...)
	if _, rerr := Requirements(c.Base, s, c.Class); rerr != nil {
		p.add("%v", rerr)
	}
	if len(s.Restrictions) > 0 {
		perms, perr := s.Permissions()
		switch {
		case perr != nil:
			p.add("`restrictions`: %v", perr)
		case c.Class != nil:
			rev := classes.ReviewSheet(c.Class.Permissions, perms)
			for _, r := range rev.Refused {
				p.add("`restrictions`: %s", r)
			}
			for _, r := range rev.Ignored {
				warnings = append(warnings, "restrictions: "+r)
			}
		case s.Class != "":
			warnings = append(warnings, fmt.Sprintf("a classe `%s` não foi encontrada agora; as restrições serão conferidas com ela quando o personagem nascer", s.Class))
		}
	}
	if s.Class != "" && c.Class == nil && c.ClassMissing && c.Base.Name != OperatorBase {
		warnings = append(warnings, fmt.Sprintf("a classe `%s` não foi encontrada; cada workspace a resolve quando o personagem nasce", s.Class))
	}
	return warnings, p.err()
}

// isKnownAgentBase: the base's command is the CLI of one of the providers.
func isKnownAgentBase(b Base) bool { _, ok := providerOf(b); return ok }

// sheetClass: the base `operador` takes only sheets whose class has
// `operator: true` (docs/orquestracao/bases-e-fichas.md#base-especial-operador-do-pc).
func sheetClass(s Sheet, c SheetContext) (p problems) {
	if c.Base.Name != OperatorBase {
		return nil
	}
	if s.Agent != AgentClaudeCode {
		p.add("a base `%s` inicia o Claude Code; o agente da ficha é `claude-code`", OperatorBase)
	}
	if s.Class == "" {
		return p // incomplete: the panel shows it, and a request fails
	}
	switch {
	case c.Class == nil:
		p.add("classe `%s` não encontrada; a base `%s` só aceita fichas cuja classe tem `operator: true`", s.Class, OperatorBase)
	case c.Class.Permissions == nil || c.Class.Permissions.Operator == nil || !*c.Class.Permissions.Operator:
		p.add("a classe `%s` não tem `operator: true`; a base `%s` só aceita fichas de classe do Operador", s.Class, OperatorBase)
	}
	return p
}

// Permissions reads the restrictions of the sheet as a permissions.yaml.
func (s Sheet) Permissions() (*classes.Permissions, error) {
	if len(s.Restrictions) == 0 {
		return nil, nil
	}
	data, err := yaml.Marshal(s.Restrictions)
	if err != nil {
		return nil, err
	}
	perms, err := classes.ParsePermissions(data)
	if err != nil {
		return nil, errors.New(strings.TrimPrefix(err.Error(), classes.ErrInvalid.Error()+": "))
	}
	return perms, nil
}

// Requirements are what a character of this sheet needs from the machine:
// the sum of the base, the sheet and the class, with the provider of the
// agent and the account profile the sheet asks for
// (docs/guilda/ficha-da-maquina.md#requisitos). The platforms of the base
// and its list of machines restrict the choice too (Choose).
func Requirements(b Base, s Sheet, class *classes.Class) (machinesheet.Requirements, error) {
	scope := machinesheet.Scope{OperatorBase: b.Name == OperatorBase}
	br, err := machinesheet.ParseRequires(b.Requires, scope)
	if err != nil {
		return machinesheet.Requirements{}, fmt.Errorf("base `%s`: %w", b.Name, err)
	}
	sr, err := machinesheet.ParseRequires(s.Requires, scope)
	if err != nil {
		return machinesheet.Requirements{}, fmt.Errorf("ficha `%s`: %w", s.Name, err)
	}
	all := []machinesheet.Requirements{br, sr}
	if class != nil {
		all = append(all, class.Requires)
	}
	sum, err := machinesheet.Combine(all...)
	if err != nil {
		return machinesheet.Requirements{}, err
	}
	if s.Agent == AgentShell || !slices.Contains(Agents(), s.Agent) {
		return sum, nil
	}
	sum.CLI = sum.CLI || s.HasSkills()
	return sum.WithAgent(s.Agent, s.Account)
}
