package machinesheet

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

// The requirements a base, a character sheet or a class can declare
// (docs/guilda/ficha-da-maquina.md#requisitos).
const (
	ReqOS            = "os"
	ReqGPU           = "gpu"
	ReqMemMin        = "mem_min"
	ReqProvider      = "provider"
	ReqBrowser       = "browser"
	ReqTranscription = "transcricao"
	ReqOperator      = "operador"
	ReqGUI           = "gui"
)

// AcceptedRequirements are the names ParseRequires accepts, in the order the
// document lists them.
func AcceptedRequirements() []string {
	return []string{ReqOS, ReqGPU, ReqMemMin, ReqProvider, ReqBrowser, ReqTranscription, ReqOperator, ReqGUI}
}

// Requirements are what a character needs from the machine it runs on. The
// zero value asks for nothing.
type Requirements struct {
	// OS is "linux", "windows" or "" for any.
	OS          string
	GPU         bool
	MemMin      uint64
	Provider    string
	Browser     bool
	Transcribes bool
	Operator    bool
	GUI         bool
	// Account is the account profile the character sheet asks for ("" is
	// the default profile of the provider). It is not a requirement the
	// author declares: it comes from the `account` field of the sheet.
	Account string
	// OperatorBase is true when the requirements come from the Operator of
	// the PC base or its sheets, the only ones that may ask for a GUI.
	OperatorBase bool
}

// Scope says where the requirements are declared.
type Scope struct {
	// OperatorBase is true in the `operador` base and in the sheets of it.
	OperatorBase bool
}

// ErrRequires: a `requires` that is refused when it is saved.
var ErrRequires = errors.New("requisito recusado")

// ParseRequires reads the `requires` map of a base, a sheet or a class. An
// unknown requirement, a value of the wrong type and `gui: true` outside the
// Operator base are refused with the reason, naming the accepted ones.
func ParseRequires(raw map[string]any, scope Scope) (Requirements, error) {
	r := Requirements{OperatorBase: scope.OperatorBase}
	for _, k := range sortedKeys(raw) {
		v := raw[k]
		var err error
		switch k {
		case ReqOS:
			s, ok := v.(string)
			if !ok || (s != OSLinux && s != OSWindows) {
				err = fmt.Errorf("`os` aceita `linux` ou `windows`")
			}
			r.OS = s
		case ReqGPU:
			r.GPU, err = boolValue(k, v)
		case ReqMemMin:
			r.MemMin, err = bytesValue(v)
		case ReqProvider:
			s, ok := v.(string)
			if !ok || !slices.Contains(KnownProviders(), s) {
				err = fmt.Errorf("`provider` aceita %s", quoteList(KnownProviders()))
			}
			r.Provider = s
		case ReqBrowser:
			r.Browser, err = boolValue(k, v)
		case ReqTranscription:
			r.Transcribes, err = boolValue(k, v)
		case ReqOperator:
			r.Operator, err = boolValue(k, v)
		case ReqGUI:
			if r.GUI, err = boolValue(k, v); err == nil && r.GUI && !scope.OperatorBase {
				err = fmt.Errorf("`gui: true` (precisa de interface) só vale na base `operador` e nas fichas dela")
			}
		default:
			err = fmt.Errorf("requisito `%s` desconhecido; os aceitos são %s", k, quoteList(AcceptedRequirements()))
		}
		if err != nil {
			return Requirements{}, fmt.Errorf("%w: %w", ErrRequires, err)
		}
	}
	return r, nil
}

func boolValue(name string, v any) (bool, error) {
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("`%s` aceita `true` ou `false`", name)
	}
	return b, nil
}

// bytesValue accepts a whole, non-negative number of bytes, as YAML and JSON
// decode it.
func bytesValue(v any) (uint64, error) {
	bad := fmt.Errorf("`mem_min` é um número inteiro de bytes")
	switch n := v.(type) {
	case int:
		if n >= 0 {
			return uint64(n), nil
		}
	case int64:
		if n >= 0 {
			return uint64(n), nil
		}
	case uint64:
		return n, nil
	case float64:
		if n >= 0 && n == math.Trunc(n) && n < math.MaxUint64 {
			return uint64(n), nil
		}
	}
	return 0, bad
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func quoteList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = "`" + s + "`"
	}
	return strings.Join(q, ", ")
}

// WithAgent adds the provider of a character sheet (`agent`) as the
// `provider` requirement, and the account profile it asks for. The agent is
// not repeated as a requirement.
func (r Requirements) WithAgent(provider, account string) (Requirements, error) {
	if provider == "" {
		return r, nil
	}
	if r.Provider != "" && r.Provider != provider {
		return Requirements{}, fmt.Errorf("%w: `provider: %s` não combina com o agente `%s`", ErrRequires, r.Provider, provider)
	}
	r.Provider, r.Account = provider, account
	return r, nil
}

// Combine adds the requirements of the base, the sheet and the class: a
// character needs all of them. Two different systems or providers cannot
// both be met and are refused.
func Combine(all ...Requirements) (Requirements, error) {
	var out Requirements
	for _, r := range all {
		if r.OS != "" {
			if out.OS != "" && out.OS != r.OS {
				return Requirements{}, fmt.Errorf("%w: `os: %s` e `os: %s` não podem valer juntos", ErrRequires, out.OS, r.OS)
			}
			out.OS = r.OS
		}
		if r.Provider != "" {
			if out.Provider != "" && out.Provider != r.Provider {
				return Requirements{}, fmt.Errorf("%w: `provider: %s` e `provider: %s` não podem valer juntos", ErrRequires, out.Provider, r.Provider)
			}
			out.Provider = r.Provider
		}
		if r.Account != "" {
			out.Account = r.Account
		}
		out.MemMin = max(out.MemMin, r.MemMin)
		out.GPU = out.GPU || r.GPU
		out.Browser = out.Browser || r.Browser
		out.Transcribes = out.Transcribes || r.Transcribes
		out.Operator = out.Operator || r.Operator
		out.GUI = out.GUI || r.GUI
		out.OperatorBase = out.OperatorBase || r.OperatorBase
	}
	return out, nil
}

// The codes of Verdict.Unmet: "offline" or the name of the requirement.
const (
	UnmetOffline = "offline"
)

// Verdict is how one machine fares against a character.
type Verdict struct {
	MachineID string
	Name      string
	// OK is true when the machine is online and meets every requirement.
	OK bool
	// Unmet is the first requirement not met ("offline" or the name of the
	// requirement); Reason says why, in a short text for the Owner.
	Unmet  string
	Reason string
}

// String is "name: reason" for a machine that does not fit, "name" for one
// that does.
func (v Verdict) String() string {
	if v.OK {
		return v.Name
	}
	return v.Name + ": " + v.Reason
}

// Match checks one machine against the requirements and reports the first one
// not met, in the order of the document: offline, os, gpu, mem_min, provider,
// browser, transcricao, operador, gui.
func Match(s Sheet, r Requirements) Verdict {
	name := s.Name
	if name == "" {
		name = s.MachineID
	}
	v := Verdict{MachineID: s.MachineID, Name: name}
	fail := func(unmet, format string, a ...any) Verdict {
		v.Unmet, v.Reason = unmet, fmt.Sprintf(format, a...)
		return v
	}
	switch {
	case !s.Online:
		return fail(UnmetOffline, "offline")
	case r.OS != "" && s.OS != r.OS:
		return fail(ReqOS, "o sistema é %s e o pedido é %s", s.OS, r.OS)
	case r.GPU && len(s.Resources.GPUs) == 0:
		return fail(ReqGPU, "sem GPU")
	case r.MemMin > s.Resources.MemBytes:
		return fail(ReqMemMin, "memória de %s, o mínimo é %s", gib(s.Resources.MemBytes), gib(r.MemMin))
	}
	if r.Provider != "" {
		if bad := matchProvider(s, r); bad != "" {
			return fail(ReqProvider, "%s", bad)
		}
	}
	switch {
	case r.Browser && s.Browser == nil:
		return fail(ReqBrowser, "sem navegador")
	case r.Transcribes && !s.Transcricao:
		return fail(ReqTranscription, "transcrição indisponível (whisper-server não verificado)")
	case r.Operator && !s.Operador.Enabled:
		return fail(ReqOperator, "o Operador do PC não está habilitado")
	case r.GUI && !r.OperatorBase:
		return fail(ReqGUI, "interface só para a base `operador`")
	case r.GUI && !s.GUI:
		return fail(ReqGUI, "sem sessão de usuário com área de trabalho")
	}
	v.OK = true
	return v
}

func matchProvider(s Sheet, r Requirements) string {
	p, ok := s.Provider(r.Provider)
	if !ok {
		return fmt.Sprintf("o provedor `%s` não está instalado", r.Provider)
	}
	pr, ok := p.ProfileFor(r.Account)
	if !ok {
		if r.Account == "" {
			return fmt.Sprintf("o provedor `%s` não tem perfil de conta padrão", r.Provider)
		}
		return fmt.Sprintf("perfil de conta `%s` não existe", r.Account)
	}
	switch {
	case !pr.LoggedIn:
		return fmt.Sprintf("perfil de conta `%s` não está logado", pr.Name)
	case !pr.Conformant && pr.Version != "":
		return fmt.Sprintf("perfil de conta `%s` não é conforme na versão %s", pr.Name, pr.Version)
	case !pr.Conformant:
		return fmt.Sprintf("perfil de conta `%s` não é conforme", pr.Name)
	}
	return ""
}

func gib(b uint64) string {
	return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
}

// Choice is the result of matching a character against the machines.
type Choice struct {
	// Verdicts has one entry per machine, in the order they were given.
	Verdicts []Verdict
}

// Fit are the machines that meet the requirements, in the order given. The
// order of preference is the caller's (docs/orquestracao/personagens.md).
func (c Choice) Fit() []Verdict {
	var out []Verdict
	for _, v := range c.Verdicts {
		if v.OK {
			out = append(out, v)
		}
	}
	return out
}

// ErrNoMachine: no machine meets the requirements. The character does not run
// on another one.
var ErrNoMachine = errors.New("nenhuma máquina atende ao personagem")

// NoMachineError carries the reason of each machine.
type NoMachineError struct{ Verdicts []Verdict }

func (e *NoMachineError) Error() string {
	if len(e.Verdicts) == 0 {
		return ErrNoMachine.Error() + ": a Guilda não tem máquinas"
	}
	parts := make([]string, len(e.Verdicts))
	for i, v := range e.Verdicts {
		parts[i] = v.String()
	}
	return ErrNoMachine.Error() + ": " + strings.Join(parts, "; ")
}

// Unwrap lets errors.Is find ErrNoMachine.
func (e *NoMachineError) Unwrap() error { return ErrNoMachine }

// Err is nil when at least one machine fits, and a *NoMachineError with the
// reason of each machine otherwise.
func (c Choice) Err() error {
	if len(c.Fit()) > 0 {
		return nil
	}
	return &NoMachineError{Verdicts: c.Verdicts}
}

// Choose matches every sheet against the requirements.
func Choose(sheets []Sheet, r Requirements) Choice {
	c := Choice{Verdicts: make([]Verdict, len(sheets))}
	for i, s := range sheets {
		c.Verdicts[i] = Match(s, r)
	}
	return c
}
