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
	if sub, ok := subcommandScript(base, args); ok {
		return sub, sub.text != ""
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

// pwshScript reports a program word that names a PowerShell script
// (.\build.ps1): canonical drops the extension, so it is never the program
// of the same name (script, nice, bash).
func pwshScript(w word) bool {
	return !w.expand && strings.HasSuffix(strings.ToLower(w.text), ".ps1")
}

// parallelText is the command line of GNU parallel: the command before
// ::: with each input appended, or each input as a command when no command
// is given (parallel ::: 'git push').
func parallelText(rest []string) string {
	cmd, inputs := rest, []string(nil)
	for i, t := range rest {
		if t == ":::" || t == "::::" || t == ":::+" {
			cmd, inputs = rest[:i], rest[i+1:]
			break
		}
	}
	if inputs == nil {
		return strings.Join(cmd, " ")
	}
	lines := make([]string, 0, len(inputs))
	for _, in := range inputs {
		if strings.HasPrefix(in, ":::") {
			continue
		}
		lines = append(lines, strings.TrimSpace(strings.Join(cmd, " ")+" "+in))
	}
	return strings.Join(lines, "\n")
}

// shellText is nestedShell for a POSIX-like shell: its options are skipped
// with their values (-o posix, +O extglob, --rcfile x), -c takes the first
// operand after them as the command text, and a shell without a script (or
// with -s) reads it from the input. ok is false for a shell that runs a
// script file.
func shellText(cn string, texts []string) (nested string, d dialect, why string, ok bool) {
	command, stdin, rc := false, false, ""
	i := 0
	for ; i < len(texts); i++ {
		a := texts[i]
		if a == "-" {
			return "", dialectPOSIX, cn + " lendo o script da entrada" + rc, true
		}
		if a == "--" {
			i++
			break
		}
		if !strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "+") {
			break
		}
		switch name, _, hasVal := strings.Cut(a, "="); {
		case a == "-o" || a == "+o" || a == "-O" || a == "+O":
			i++ // the option's name
		case name == "--rcfile" || name == "--init-file":
			rc = " " + name
			if !hasVal {
				i++
			}
		case name == "--command" || name == "--commands":
			command = true
			if hasVal {
				return a[len(name)+1:], dialectPOSIX, cn + " " + name + rc, true
			}
		case strings.HasPrefix(a, "--") || strings.HasPrefix(a, "+"):
		default:
			command = command || strings.Contains(a[1:], "c")
			stdin = stdin || strings.Contains(a[1:], "s")
		}
	}
	switch {
	case command && i < len(texts):
		return texts[i], dialectPOSIX, cn + " -c" + rc, true
	case command:
		return "", dialectPOSIX, cn + " -c" + rc, true
	case stdin || i >= len(texts):
		return "", dialectPOSIX, cn + " lendo o script da entrada" + rc, true
	}
	return "", 0, "", false
}

// subcommandPrograms are the interpreters whose first operand may be a
// subcommand of their own (go test, bun install, deno fmt) instead of a
// script: only go run <file>, bun <file>, bun run <file>, deno run <file>
// and deno <file> run a file.
var subcommandPrograms = map[string]map[string]bool{
	"go":   nil,
	"bun":  set("install i add a remove rm update outdated test build x pm create init link unlink upgrade publish patch"),
	"deno": set("test fmt lint check task bench doc info eval repl install uninstall cache compile serve upgrade jupyter add remove outdated publish init coverage types vendor"),
}

// subcommandScript is the script of an interpreter of subcommandPrograms:
// ok is true when the program is one of them, and the word is empty when
// no file is run (go test ./..., bun install).
func subcommandScript(base string, args []word) (word, bool) {
	subs, ok := subcommandPrograms[base]
	if !ok {
		return word{}, false
	}
	i := 0
	for i < len(args) && strings.HasPrefix(args[i].text, "-") {
		i++
	}
	if i >= len(args) {
		return word{}, true
	}
	switch sub := args[i].text; {
	case sub == "run":
		rest := skipDashOpts(args[i+1:], nil)
		if len(rest) > 0 && (base == "go" && strings.HasSuffix(strings.ToLower(rest[0].text), ".go") || base != "go" && looksLikeScriptFile(rest[0].text)) {
			return rest[0], true
		}
		return word{}, true
	case base == "go" || subs[sub]:
		return word{}, true
	}
	return args[i], true // bun x.ts, deno x.ts
}

// tracerSpec is how a transparent runner takes its command: after its
// options (valued take the next word) and its required positionals (the
// priority of chrt, the mask of taskset, the time of faketime, the lock of
// flock).
type tracerSpec struct {
	valued      map[string]bool
	positionals int
}

var tracers = map[string]tracerSpec{
	"strace":   {valued: set("-o -e -p -E -u -s -S -P -a -b -I -O -X -U --output --trace --signal --status")},
	"ltrace":   {valued: set("-o -e -p -u -s -a -n -A -D -F -l -L -x --output --library")},
	"valgrind": {}, "catchsegv": {}, "eatmydata": {}, "unbuffer": {}, "nocache": {}, "proxychains": {valued: set("-f")},
	"proxychains4": {valued: set("-f")}, "torsocks": {valued: set("-u -p -a -P --user --pass --address --port")},
	"chrt":     {valued: set("-T -P -D --sched-runtime --sched-period --sched-deadline"), positionals: 1},
	"taskset":  {positionals: 1},
	"faketime": {valued: set("-p --date-prog"), positionals: 1},
	"numactl":  {valued: set("-N -C -m -i -p -P --interleave --membind --cpunodebind --physcpubind --preferred --cpubind")},
	"flock":    {valued: set("-w -E --timeout --conflict-exit-code"), positionals: 1},
}

// runnerUnwrap returns the command a runner runs: a tracer (strace, ltrace)
// is transparent; a project runner (uv run, poetry run, deno run, bun run,
// npx tsx) runs the program named after its subcommand and options; awk -f
// and sed -f run a script file. ok is false for anything else.
func runnerUnwrap(cn string, args []word) (inner []word, ok bool) {
	if cn == "flock" {
		// flock [opts] lock cmd…, or flock [opts] lock -c 'cmd' (getopt
		// takes -c anywhere): the text of -c is a shell's.
		for i, a := range args {
			if (a.text == "-c" || a.text == "--command") && i+1 < len(args) {
				return []word{{text: "sh", assign: -1}, {text: "-c", assign: -1}, args[i+1]}, true
			}
			if strings.HasPrefix(a.text, "--command=") {
				t := a
				t.text = strings.TrimPrefix(a.text, "--command=")
				return []word{{text: "sh", assign: -1}, {text: "-c", assign: -1}, t}, true
			}
		}
	}
	if spec, ok := tracers[cn]; ok {
		rest := skipDashOpts(args, spec.valued)
		if len(rest) <= spec.positionals {
			return nil, false
		}
		return rest[spec.positionals:], true
	}
	switch cn {
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
