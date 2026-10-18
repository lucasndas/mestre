package classes

import (
	"slices"
	"strings"
)

// A package runner downloads a package and runs the program it brings: npx,
// bunx and pnpx; exec, dlx and x of npm, pnpm and yarn; bun x; deno run
// npm:<package> and deno x; uvx, uv tool run and pipx run. When that program
// is the CLI of an agent (claude, codex, agy), a personagem would be born
// outside the engine, and the line is a fixed denial
// (docs/classes/permissoes.md#regras-fixas-de-segurança).

// runnerRest are the arguments a package runner passes on after its own
// options and subcommand: the command (or package) it runs and its arguments.
// The subcommands of the package managers (exec, dlx, x) and the programs
// that are a runner by themselves (npx, bunx, uvx…) are taken from args; more
// than one rest is possible when a word may be an option's value or the
// subcommand (npm --prefix x exec claude).
func runnerRests(cn string, args []word) [][]word {
	after := func(rests [][]word, subs ...string) [][]word {
		var out [][]word
		for _, r := range rests {
			out = append(out, subRests(r, subs...)...)
		}
		return out
	}
	switch cn {
	case "npx", "bunx", "pnpx", "uvx", "dx":
		return [][]word{args}
	case "npm", "pnpm", "yarn":
		return subRests(args, "exec", "dlx", "x")
	case "bun", "deno":
		return subRests(args, "x")
	case "pipx":
		return subRests(args, "run")
	case "uv":
		return after(subRests(args, "tool"), "run")
	}
	return nil
}

// subRests are the arguments after each word that may be one of the
// subcommands subs of a package manager. The words before the subcommand are
// options and the values of options, and the first word that is not an
// option or right after one is the subcommand; a word right after an option
// may be its value or the subcommand, so it counts as both.
func subRests(args []word, subs ...string) [][]word {
	var out [][]word
	afterOption := false
	for j, a := range args {
		if strings.HasPrefix(a.text, "-") {
			afterOption = true
			continue
		}
		if slices.Contains(subs, a.text) {
			out = append(out, args[j+1:])
		}
		if !afterOption {
			break
		}
		afterOption = false
	}
	return out
}

// agentRunner reports a package runner line that starts the CLI of an
// agent. cn is the program and args its arguments.
func agentRunner(cn string, args []word) bool {
	for _, rest := range runnerRests(cn, args) {
		if runsAgentCLI(wordsText(rest)) {
			return true
		}
	}
	if cn == "deno" {
		// deno run [options] npm:<package>, jsr:<package>: wherever the
		// specifier is among the options and their values.
		return slices.ContainsFunc(args, func(a word) bool {
			l := strings.ToLower(a.text)
			return (strings.HasPrefix(l, "npm:") || strings.HasPrefix(l, "jsr:")) && isAgentPackage(a.text)
		})
	}
	return false
}

// runnerCommands are the commands a package runner runs, as the words of a
// command each: what it finds after its options, which is a program on the
// PATH or in node_modules/.bin as well as a package (npx git push runs git,
// pnpm exec claude runs claude). The command holds for the fixed rules: git
// push, a CLI of an agent or an elevation command denies whatever launches it.
func runnerCommands(cn string, args []word) [][]word {
	var out [][]word
	for _, rest := range runnerRests(cn, args) {
		if op := runnerOperand(rest); len(op) > 0 {
			out = append(out, op)
		}
	}
	return out
}

// runnerOperand is the first operand of a runner and what follows it,
// after the options (the package or the command to run) and the values of
// the options that take one.
func runnerOperand(args []word) []word {
	for i := 0; i < len(args); i++ {
		a := args[i].text
		switch {
		case a == "-p" || a == "--package" || a == "--from" || a == "--spec" || runnerValued[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return args[i:]
		}
	}
	return nil
}

// runnerCall is the shell command a package runner is given with -c or
// --call (npx -c 'git push'): a command line like the one of sh -c.
func runnerCall(cn string, args []word) (string, bool) {
	for _, rest := range runnerRests(cn, args) {
		for i := 0; i < len(rest); i++ {
			a := rest[i].text
			switch {
			case strings.HasPrefix(a, "--call="):
				return strings.TrimPrefix(a, "--call="), true
			case (a == "-c" || a == "--call") && i+1 < len(rest):
				return rest[i+1].text, true
			case !strings.HasPrefix(a, "-"):
				i = len(rest) // the package, and the options of the program it runs
			}
		}
	}
	return "", false
}

// runnerValued are the options of the runners that take the next word, which
// is not the package: the interpreter, the extra packages, the registry.
var runnerValued = set("--python --with --with-editable --registry --cache --prefix --index --index-url --extra-index-url --default-index --pip-args --workspace --cwd --dir --filter")

// runsAgentCLI reports whether a package runner's first operand is a CLI of
// an agent, or an option of the runner names it as the package (-p,
// --package, --from, --spec).
func runsAgentCLI(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-p" || a == "--package" || a == "--from" || a == "--spec" {
			if i+1 < len(args) && isAgentPackage(args[i+1]) {
				return true
			}
			i++
			continue
		}
		for _, o := range []string{"--package=", "--from=", "--spec="} {
			if strings.HasPrefix(a, o) && isAgentPackage(strings.TrimPrefix(a, o)) {
				return true
			}
		}
		if runnerValued[a] {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return isAgentPackage(a)
	}
	return false
}

// isAgentPackage reports the name of a package (or of its program) that is
// the CLI of an agent: claude, claude-code, codex, agy, antigravity, with a
// scope (@anthropic-ai/claude-code, @openai/codex), a version (claude@1.2,
// claude==1.2), extras (claude-code[x]) and the npm: and jsr: of a specifier.
func isAgentPackage(p string) bool {
	p = strings.ToLower(p)
	p = strings.TrimPrefix(strings.TrimPrefix(p, "npm:"), "jsr:")
	scope := 0
	if strings.HasPrefix(p, "@") {
		scope = 1 // the scope's own @
	}
	if i := strings.IndexAny(p[scope:], "@=<>~![;"); i >= 0 {
		p = p[:scope+i]
	}
	base := p[strings.LastIndex(p, "/")+1:]
	for _, ext := range []string{".exe", ".cmd", ".bat", ".ps1"} {
		base = strings.TrimSuffix(base, ext)
	}
	switch base {
	case "claude", "claude-code", "codex", "agy", "antigravity":
		return true
	}
	return false
}
