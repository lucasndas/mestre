package classes

import (
	"regexp"
	"strings"
)

// Interpreters launder the risk of a script: bash -x x.sh, python3.11 x.py,
// pwsh -File x.ps1 and strace ./x.sh all run x, and a script changed at the
// table stays medio whatever the launcher
// (docs/classes/classificacao-de-risco.md#sinais-de-risco). The option
// skipping, the versioned-name normalizer and the runner unwrapping serve
// both the permission decision and the risk.

// versionedInterpreter matches an interpreter name with a version suffix:
// python3.11, python2.7, pypy3, node20, ruby3.2, php8.
var versionedInterpreter = regexp.MustCompile(`^(python|pypy|node|nodejs|ruby|perl|php|lua|deno|bun|tsx|pwsh|powershell)[0-9._-]*[0-9]$`)

// normalizeInterpreter folds a versioned interpreter name to its base, so
// the tables and the signals see python3.11 as python3 and node20 as node.
func normalizeInterpreter(cn string) string {
	if interpreters[cn] || inlineInterpreters[cn] || shells[cn] {
		return cn
	}
	if m := versionedInterpreter.FindStringSubmatch(cn); m != nil {
		switch base := m[1]; base {
		case "pypy":
			return "python"
		case "nodejs":
			return "node"
		default:
			if base == "python" && strings.HasPrefix(cn, "python2") {
				return "python2"
			}
			if base == "python" {
				return "python3"
			}
			return base
		}
	}
	return cn
}

// interpreterValueOpts are the options of an interpreter that take a value
// which is not its script: skipping them reaches the script.
var interpreterValueOpts = map[string]map[string]bool{
	"_shell":  {"-o": true, "+o": true, "--rcfile": true, "--init-file": true},
	"python":  {"-W": true, "-X": true, "--check-hash-based-pycs": true},
	"python2": {"-W": true, "-X": true},
	"python3": {"-W": true, "-X": true, "--check-hash-based-pycs": true},
	"node":    {"-r": true, "--require": true, "--experimental-loader": true, "--loader": true, "--import": true, "--conditions": true, "--max-old-space-size": true},
	"deno":    {"--allow-read": true, "--allow-write": true, "--allow-net": true, "--allow-env": true, "--allow-run": true, "--importmap": true, "--config": true},
	"ruby":    {"-I": true, "-r": true, "-C": true, "-E": true, "-K": true},
	"perl":    {"-I": true, "-m": true, "-M": true},
	"php":     {"-d": true, "-c": true},
}

// interpreterCodeOpts are the options after which the next word, or the
// option's attached value, is inline code or a module, never a script
// file: the command runs code the text shows (handled as a signal or a
// nested shell), not a file.
var interpreterCodeOpts = map[string]bool{
	"-c": true, "-e": true, "--eval": true, "-E": true, "-m": true, "--module": true,
	"--command": true, "-command": true,
}

// interpreterScript is the script file an interpreter runs, after its own
// options; ok is false when it runs inline code (python -c), a module
// (python -m), reads from stdin, or the program is not an interpreter.
func interpreterScript(cn string, args []word) (word, bool) {
	base := normalizeInterpreter(cn)
	isShell := shells[base]
	if !isShell && !interpreters[base] && !inlineInterpreters[base] {
		return word{}, false
	}
	valued := interpreterValueOpts[base]
	if isShell {
		valued = interpreterValueOpts["_shell"]
	}
	for i := 0; i < len(args); i++ {
		a := args[i].text
		if a == "--" {
			if i+1 < len(args) {
				return args[i+1], true
			}
			return word{}, false
		}
		if !strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "+") || a == "-" {
			return args[i], true // the first operand is the script
		}
		// PowerShell: -File names the script, -Command is inline code.
		if base == "pwsh" || base == "powershell" {
			l := strings.ToLower(a)
			if l == "-file" || l == "-f" {
				if i+1 < len(args) {
					return args[i+1], true
				}
				return word{}, false
			}
			if l == "-command" || l == "-c" || l == "-encodedcommand" || l == "-e" || l == "-ec" || l == "-enc" {
				return word{}, false
			}
			continue
		}
		// A shell's -c means a command string, -s reads stdin (not a
		// script file), -o/+o take a value, other clusters are flags.
		if isShell {
			if a == "-o" || a == "+o" {
				i++
				continue
			}
			if !strings.HasPrefix(a, "--") && strings.ContainsAny(a[1:], "cs") {
				return word{}, false
			}
			continue
		}
		name, val, hasVal := strings.Cut(a, "=")
		if interpreterCodeOpts[name] {
			return word{}, false // inline code or a module: not a file
		}
		if valued[name] && !hasVal {
			i++ // the option's value is the next word
		}
		_ = val
	}
	return word{}, false
}

// runnerUnwrap returns the command a runner runs: a tracer (strace, ltrace)
// is transparent; a project runner (uv run, poetry run, deno run, bun run,
// npx tsx) runs the program named after its subcommand and options; awk -f
// and sed -f run a script file. ok is false for anything else.
func runnerUnwrap(cn string, args []word) (inner []word, ok bool) {
	switch cn {
	case "strace", "ltrace", "catchsegv", "valgrind", "faketime", "chrt", "taskset", "numactl", "eatmydata":
		rest := skipDashOpts(args, map[string]bool{
			"-o": true, "-e": true, "-p": true, "-E": true, "-u": true, "-s": true, "-S": true, "-P": true,
			"--output": true, "-c": true, "-n": true,
		})
		return rest, len(rest) > 0
	case "uv", "poetry", "pipenv", "pdm", "rye", "hatch", "pixi":
		return afterRun(args)
	case "deno", "bun":
		// deno run [opts] file, bun run file: the file after run is the
		// script, so a changed one is medio (the launcher does not launder
		// it). A package script (bun run build) is not a file.
		rest, ok := afterRun(args)
		if !ok || len(rest) == 0 || !looksLikeScriptFile(rest[0].text) {
			return nil, false
		}
		return rest[:1], true
	}
	return nil, false
}

// scriptExts are the suffixes of a file an interpreter or a runner runs as
// a script of the worktree.
var scriptExts = []string{".sh", ".bash", ".zsh", ".ksh", ".py", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".rb", ".pl", ".php", ".lua", ".ps1"}

// looksLikeScriptFile reports a plain argument that names a script file by
// its extension (x.py, build.sh), so a runner's operand or a bare program
// is treated as a script of the worktree.
func looksLikeScriptFile(t string) bool {
	if t == "" || strings.HasPrefix(t, "-") {
		return false
	}
	l := strings.ToLower(t)
	for _, e := range scriptExts {
		if strings.HasSuffix(l, e) {
			return true
		}
	}
	return false
}

// afterRun is the command after a "run" (or "exec") subcommand and its
// options, for the project runners (uv run python x.py).
func afterRun(args []word) ([]word, bool) {
	i := 0
	for i < len(args) && args[i].text != "run" && args[i].text != "exec" {
		if !strings.HasPrefix(args[i].text, "-") {
			return nil, false // not a run invocation
		}
		i++
	}
	if i >= len(args) {
		return nil, false
	}
	rest := args[i+1:]
	// Skip the runner's own options before the command (uv run --with x py).
	rest = skipDashOpts(rest, map[string]bool{
		"--with": true, "--python": true, "-p": true, "--extra": true, "--group": true, "--index": true,
		"--env-file": true, "--directory": true, "--project": true, "--no-project": false,
	})
	if len(rest) == 0 {
		return nil, false
	}
	return rest, true
}

// skipDashOpts returns the operands of args after a run of options at the
// front; the options in valued consume the next word.
func skipDashOpts(args []word, valued map[string]bool) []word {
	i := 0
	for i < len(args) {
		a := args[i].text
		if a == "--" {
			return args[i+1:]
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			break
		}
		if valued[a] {
			i++
		}
		i++
	}
	return args[i:]
}
