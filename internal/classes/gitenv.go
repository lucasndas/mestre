package classes

import (
	"cmp"
	"fmt"
	"strings"
)

// The global configuration of git is read from the home of its user. The
// engine fixes it for each personagem with GIT_CONFIG_GLOBAL, so a line that
// moves the home (HOME, XDG_CONFIG_HOME…) or takes GIT_CONFIG_GLOBAL out of
// the environment hands git another ~/.gitconfig: one the agent can plant,
// with a core.fsmonitor or an alias that runs a program. A git that runs
// under such a line is a fixed denial (docs/classes/permissoes.md#regras-fixas-de-segurança),
// whatever wraps the assignment (a prefix, env, export, declare, a lone
// assignment, $env:), and whatever runs git (a wrapper, a shell, a
// substitution).

// homeVars are the variables that choose which global configuration git
// reads: the home of the user, the folder of its configuration and, on
// Windows, the ones git falls back to.
var homeVars = set("HOME XDG_CONFIG_HOME USERPROFILE HOMEDRIVE HOMEPATH")

func homeLike(name string) bool { return homeVars[strings.ToUpper(name)] }

// shellVarBuiltins are the builtins that set variables: the names they set
// are checked like the prefix assignments of a command.
var shellVarBuiltins = set("export declare typeset readonly local setx set")

// pwshEnvSet and pwshEnvRemove are the cmdlets that set and remove an item
// of the Env: drive, which is the environment (Set-Item Env:HOME x,
// Remove-Item Env:GIT_CONFIG_GLOBAL).
var (
	pwshEnvSet    = set("set-item si new-item ni set-content sc add-content ac")
	pwshEnvRemove = set("remove-item ri rm del erase rd clear-item cli clear-content clc")
)

// envDrive is the variable an argument names on the Env: drive, or "".
func envDrive(a string) string {
	if len(a) > len("env:") && strings.EqualFold(a[:len("env:")], "env:") {
		return a[len("env:"):]
	}
	return ""
}

// varNames are the variables a builtin sets (export X=1, declare X=1,
// setx X 1, set X=1) and the ones it takes out of the environment of the
// commands that follow (unset X, export -n X, declare +x X, and the Env:
// drive of PowerShell).
func varNames(cn string, args []string) (assigned, removed []string) {
	switch {
	case pwshEnvSet[cn] || pwshEnvRemove[cn]:
		for _, a := range args {
			if n := envDrive(a); n != "" {
				if pwshEnvSet[cn] {
					assigned = append(assigned, n)
				} else {
					removed = append(removed, n)
				}
			}
		}
	case cn == "unset":
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				removed = append(removed, a)
			}
		}
	case shellVarBuiltins[cn]:
		unexport := false
		for _, a := range args {
			switch {
			case cn == "export" && strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "n"):
				unexport = true
			case (cn == "declare" || cn == "typeset" || cn == "local") && strings.HasPrefix(a, "+") && strings.Contains(a, "x"):
				unexport = true
			}
		}
		for i, a := range args {
			if strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+") {
				continue
			}
			if n, _, ok := strings.Cut(a, "="); ok {
				assigned = append(assigned, n)
				if unexport {
					removed = append(removed, n)
				}
			} else if cn == "setx" && i == 0 {
				assigned = append(assigned, a)
			} else if unexport {
				removed = append(removed, a)
			}
		}
	}
	return assigned, removed
}

// pwshEnvAssign is the variable a PowerShell part assigns with $env:NAME =
// value, or "".
func pwshEnvAssign(p *simple) string {
	if len(p.words) == 0 || !strings.HasPrefix(strings.ToLower(p.words[0].text), "$env:") {
		return ""
	}
	rest := p.words[0].text[len("$env:"):]
	if n, _, ok := strings.Cut(rest, "="); ok {
		return n
	}
	if len(p.words) > 1 && strings.HasPrefix(p.words[1].text, "=") {
		return rest
	}
	return ""
}

// envRemoved denies taking a variable of the configuration of git, the one
// the engine sets for the personagem, out of the environment.
func (e *evaluator) envRemoved(res *result, name string) {
	if strings.HasPrefix(strings.ToUpper(name), "GIT_CONFIG") {
		res.fixedDeny(fmt.Sprintf("%s tirada do ambiente: a configuração global do git do personagem é do motor", name))
	}
}

// homeMoved is the variable that moves the global configuration of git for
// the command being analysed, or "".
func (e *evaluator) homeMoved() string { return cmp.Or(e.homeScope, e.homeSet) }

// assignedHome is the variable among the prefix assignments of a part that
// moves the global configuration of git, or "".
func assignedHome(p *simple) string {
	for _, a := range p.assigns {
		if n := a.text[:a.assign]; homeLike(n) {
			return n
		}
	}
	return ""
}

// noteHome follows what the part just analysed did to the environment of the
// parts that come after it on the line: a lone assignment, export, declare,
// unset and the like, and $env:X = in PowerShell. (The assignments that
// prefix a command belong to it alone, and env to its own command.)
func (e *evaluator) noteHome(p *simple) {
	if len(p.words) == 0 {
		if n := assignedHome(p); n != "" {
			e.homeSet = n
		}
		return
	}
	if n := pwshEnvAssign(p); homeLike(n) {
		e.homeSet = n
		return
	}
	assigned, removed := varNames(canonical(p.words[0].text), wordsText(p.words[1:]))
	for _, n := range append(assigned, removed...) {
		if homeLike(n) {
			e.homeSet = n
		}
	}
}

// envInner is the command an env line runs, and why it is at least ask. The
// options are read as env reads them: a cluster of short options (-iu NAME)
// ends at the first that takes a value, which is the rest of the cluster or
// the next word, and NAME=value words go on to the environment of the
// command. -u NAME takes a variable out of it, -S splits its value into more
// arguments, -C changes the folder.
func (e *evaluator) envInner(args []word, res *result) (inner []word, extra string) {
	opts, splits := true, 0
	assign := func(name string) {
		e.gitEnv(res, name)
		if homeLike(name) {
			e.homeScope = name
		}
	}
	unset := func(name string) {
		e.envRemoved(res, name)
		if homeLike(name) {
			e.homeScope = name
		}
	}
	// value applies the option letter or long name to its value.
	value := func(opt, val string, rest []word) ([]word, bool) {
		switch opt {
		case "u", "unset":
			unset(val)
		case "C", "chdir":
			extra = "env -C"
		case "S", "split-string":
			extra = "env -S"
			if splits++; splits > 64 {
				return nil, false
			}
			var split []word
			for _, f := range strings.Fields(val) {
				w := word{text: f, assign: -1}
				if eq := strings.IndexByte(f, '='); eq > 0 && nameRe.MatchString(f[:eq]) {
					w.assign = eq
				}
				split = append(split, w)
			}
			return append(split, rest...), true
		}
		return rest, true
	}
	for len(args) > 0 {
		w := args[0]
		a := w.text
		switch {
		case opts && a == "--":
			opts = false
			args = args[1:]
		case opts && strings.HasPrefix(a, "--"):
			name, val, hasVal := strings.Cut(a[2:], "=")
			args = args[1:]
			switch name {
			case "unset", "chdir", "split-string", "argv0":
				if !hasVal {
					if len(args) == 0 {
						return nil, extra
					}
					val, args = args[0].text, args[1:]
				}
				var ok bool
				if args, ok = value(name, val, args); !ok {
					return args, "env -S"
				}
			}
		case opts && strings.HasPrefix(a, "-") && a != "-":
			args = args[1:]
		cluster:
			for j := 1; j < len(a); j++ {
				switch c := a[j]; c {
				case 'u', 'C', 'S', 'a', 'P':
					val := a[j+1:]
					if val == "" {
						if len(args) == 0 {
							return nil, extra
						}
						val, args = args[0].text, args[1:]
					}
					var ok bool
					if args, ok = value(string(c), val, args); !ok {
						return args, "env -S"
					}
					break cluster
				}
			}
		case w.assign > 0:
			assign(a[:w.assign])
			args = args[1:]
		default:
			return args, extra
		}
	}
	return nil, extra
}
