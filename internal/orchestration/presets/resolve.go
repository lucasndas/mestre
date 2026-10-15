package presets

import (
	"fmt"
	"slices"
	"strings"

	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/providers"
)

// Resolved is a base for the system of one machine: the command and
// arguments of that system. It is what a character keeps as the copy used at
// birth (docs/orquestracao/bases-e-fichas.md#onde-ficam-e-quem-edita), so it
// is plain data.
type Resolved struct {
	Name    string   `json:"name"`
	OS      string   `json:"os"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Shell   string   `json:"shell,omitempty"`
	Cwd     string   `json:"cwd,omitempty"`
	// Env is KEY=VALUE, sorted.
	Env   []string `json:"env,omitempty"`
	Ready Ready    `json:"ready"`
}

// For resolves the base for a system: the command and arguments of its
// platform entry, or the base's own where the entry gives none.
func (b Base) For(os string) (Resolved, error) {
	for _, p := range b.Platforms {
		if p.OS != os {
			continue
		}
		r := Resolved{Name: b.Name, OS: os, Command: b.Command, Args: slices.Clone(b.Args), Shell: b.Shell,
			Cwd: b.Cwd, Env: envList(b.Env), Ready: b.Ready}
		if p.Command != "" {
			r.Command = p.Command
		}
		if p.Args != nil {
			r.Args = slices.Clone(p.Args)
		}
		return r, nil
	}
	return Resolved{}, fmt.Errorf("a base `%s` não roda em %s", b.Name, os)
}

// Vars are the values of the variables of `cwd` for one character.
type Vars struct {
	// Mesa is the worktree of the table on the machine (for the Grand
	// Master, the main clone), Workspace the main clone on the Citadel or
	// <machine data>/workspaces/<slug> on a Tower, Home the home folder of
	// the agents' account.
	Mesa, Workspace, Home string
}

// Dir expands the variables of the start folder; the empty `cwd` is {mesa}.
func (r Resolved) Dir(v Vars) (string, error) {
	cwd := r.Cwd
	if cwd == "" {
		cwd = "{" + VarMesa + "}"
	}
	var err error
	out := varRe.ReplaceAllStringFunc(cwd, func(m string) string {
		var val string
		switch name := m[1 : len(m)-1]; name {
		case VarMesa:
			val = v.Mesa
		case VarWorkspace:
			val = v.Workspace
		case VarHome:
			val = v.Home
		default:
			err = fmt.Errorf("base `%s`: variável `%s` desconhecida", r.Name, m)
		}
		if val == "" && err == nil {
			err = fmt.Errorf("base `%s`: a variável `%s` não tem valor aqui", r.Name, m)
		}
		return val
	})
	return out, err
}

// Launch is the base the adapter's BuildLaunch receives: the folder
// expanded, and the command wrapped in its shell when the base has one.
func (r Resolved) Launch(v Vars) (providers.Base, error) {
	dir, err := r.Dir(v)
	if err != nil {
		return providers.Base{}, err
	}
	b := providers.Base{Name: r.Name, Command: r.Command, Args: slices.Clone(r.Args), Env: slices.Clone(r.Env), Dir: dir}
	if r.Shell != "" {
		b.Command, b.Args = shellWrap(r.Shell, r.Command, r.Args)
	}
	return b, nil
}

// shellWrap runs the command inside the shell: bash -c '…', pwsh -Command,
// cmd /c. Each word is quoted for that shell.
func shellWrap(shell, command string, args []string) (string, []string) {
	words := append([]string{command}, args...)
	switch shell {
	case "pwsh", "powershell":
		q := make([]string, len(words))
		for i, w := range words {
			q[i] = "'" + strings.ReplaceAll(w, "'", "''") + "'"
		}
		return shell, []string{"-NoProfile", "-Command", "& " + strings.Join(q, " ")}
	case "cmd":
		q := make([]string, len(words))
		for i, w := range words {
			q[i] = w
			if w == "" || strings.ContainsAny(w, " \t&|<>^()%!\"") {
				q[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
			}
		}
		return "cmd", []string{"/d", "/s", "/c", strings.Join(q, " ")}
	}
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return shell, []string{"-c", strings.Join(q, " ")}
}

// Choose matches every machine against a character of this sheet: the
// platforms and the list of machines of the base, then the requirements
// (docs/guilda/ficha-da-maquina.md#casamento-e-escolha-da-máquina). Offline
// is said first, as there. The order of preference and the wait for a slot
// are the character registry's.
func Choose(b Base, req machinesheet.Requirements, machines []machinesheet.Sheet) machinesheet.Choice {
	c := machinesheet.Choice{Verdicts: make([]machinesheet.Verdict, len(machines))}
	for i, m := range machines {
		c.Verdicts[i] = chooseOne(b, req, m)
	}
	return c
}

func chooseOne(b Base, req machinesheet.Requirements, m machinesheet.Sheet) machinesheet.Verdict {
	if !m.Online {
		return machinesheet.Match(m, req)
	}
	v := machinesheet.Verdict{MachineID: m.MachineID, Name: m.Name}
	if v.Name == "" {
		v.Name = m.MachineID
	}
	switch {
	case !slices.ContainsFunc(b.Platforms, func(p Platform) bool { return p.OS == m.OS }):
		v.Unmet, v.Reason = machinesheet.ReqOS, fmt.Sprintf("a base `%s` não roda em %s", b.Name, m.OS)
		return v
	case len(b.Machines) > 0 && !slices.Contains(b.Machines, m.MachineID):
		v.Unmet, v.Reason = "machines", fmt.Sprintf("a base `%s` não vale nesta máquina", b.Name)
		return v
	}
	return machinesheet.Match(m, req)
}
