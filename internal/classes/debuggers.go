package classes

import "strings"

// Debuggers, profilers and meters that run a command of their own: gdb
// --args, lldb --, perf stat and record, rr record and hyperfine. Like the
// wrappers of wrappers.go they are no program of the safe list and are not
// unwrapped for the risk or for the allow patterns, but what they run is a
// command like any other for the fixed rules: git push, the CLI of an agent,
// an elevation command and a write of .git/** deny whatever starts them
// (docs/classes/permissoes.md#regras-fixas-de-segurança).

// debugged are the commands that a debugger, a profiler or a meter runs, as
// the words of a command each, or nil.
func debugged(cn string, args []word) [][]word {
	var cmds [][]word
	switch cn {
	case "gdb":
		cmds = gdbRuns(args)
	case "lldb":
		cmds = lldbRuns(args)
	case "perf":
		cmds = perfRuns(args)
	case "rr":
		cmds = rrRuns(args)
	case "hyperfine":
		cmds = hyperfineRuns(args)
	}
	var out [][]word
	for _, c := range cmds {
		if len(c) > 0 {
			out = append(out, c)
		}
	}
	return out
}

// commandsAfter are the possible commands after the options of a program that
// runs one. A word that is not an option and does not follow one that may
// take a value starts the command; one that follows such an option starts it
// or is the value of the option, so both are given. valued are the options
// known to take the next word, which is then not the command; what follows --
// is the command.
func commandsAfter(args []word, valued map[string]bool) [][]word {
	var out [][]word
	maybeValue := false // the last word is an option that may take the next one
	for i := 0; i < len(args); i++ {
		a := args[i].text
		switch {
		case a == "--":
			return append(out, args[i+1:])
		case !strings.HasPrefix(a, "-") || a == "-":
			out = append(out, args[i:])
			if !maybeValue {
				return out
			}
			maybeValue = false
		case valued[a]:
			i++
			maybeValue = false
		case strings.Contains(a, "="):
			maybeValue = false
		default:
			maybeValue = true
		}
	}
	return out
}

// gdbRuns is the program that gdb [options] --args PROGRAM ARGS runs. gdb
// reads its long options with one dash or two, and any abbreviation of them
// that is not ambiguous.
func gdbRuns(args []word) [][]word {
	for i, a := range args {
		if name := strings.TrimLeft(a.text, "-"); strings.HasPrefix(a.text, "-") && len(name) >= 2 && strings.HasPrefix("args", name) {
			if i+1 < len(args) {
				return [][]word{args[i+1:]}
			}
			return nil
		}
	}
	return nil
}

// lldbValued are the options of lldb that take the next word.
var lldbValued = set("-a --arch -c --core -f --file -n --attach-name -p --attach-pid -o --one-line -O --one-line-before-file -k --one-line-on-crash " +
	"-K --source-on-crash -s --source -S --source-before-file -l --script-language")

// lldbRuns is the program that lldb [options] [--] PROGRAM ARGS runs, or the
// file of -f / --file with the arguments after --.
func lldbRuns(args []word) [][]word {
	cmds := commandsAfter(args, lldbValued)
	file := flagValue(args, "-f", "--file")
	if file == nil {
		return cmds
	}
	out := append([][]word{{*file}}, cmds...)
	for _, c := range cmds {
		out = append(out, append([]word{*file}, c...))
	}
	return out
}

// perfValued are the options of perf stat, record and trace that take the next
// word; the others are taken for options with none, and a word after them is
// read both ways (commandsAfter).
var perfValued = set("-e --event -p --pid -t --tid -C --cpu -r --repeat -I --interval-print -o --output -x --field-separator -G --cgroup -D --delay " +
	"-M --metrics -F --freq -c --count -m --mmap-pages -u --uid --timeout --pre --post -j --branch-filter --call-graph -k --clockid -S --snapshot --filter -i --input")

// perfRuns are the programs that perf stat, record, trace and ftrace run
// ([--] COMMAND after their options), also behind sched, lock, kmem, mem, c2c,
// timechart and kvm (perf sched record -- COMMAND), and the shell commands of
// perf stat --pre and --post.
func perfRuns(args []word) [][]word {
	var out [][]word
	for _, c := range commandsAfter(args, set("--buildid-dir")) {
		if len(c) == 0 { // "perf --": the command is what follows, and nothing does
			continue
		}
		sub, rest := c[0].text, c[1:]
		switch sub {
		case "stat", "record", "trace", "ftrace":
		case "sched", "lock", "kmem", "mem", "c2c", "timechart", "kvm":
			if rest = afterRecord(rest); rest == nil {
				continue
			}
		default:
			continue
		}
		for _, name := range []string{"--pre", "--post"} {
			if w := flagValue(rest, name); w != nil {
				out = append(out, shellOf(*w))
			}
		}
		out = append(out, commandsAfter(rest, perfValued)...)
	}
	return out
}

// afterRecord is what follows the record of perf sched record, perf kvm stat
// record…: the word, up to one other before it, after the options. It is nil
// for a subcommand that is not a recording.
func afterRecord(rest []word) []word {
	others := 0
	for i, w := range rest {
		switch {
		case strings.HasPrefix(w.text, "-"):
		case w.text == "record":
			return rest[i+1:]
		default:
			if others++; others > 1 {
				return nil
			}
		}
	}
	return nil
}

// rrValued are the options of rr record that take the next word.
var rrValued = set("-c --num-cpu-ticks -e --num-events -i --ignore-signal -o --output-trace-dir -t --continue-through-signal -v --env --bind-to-cpu --syscall-buffer-size")

// rrRuns is the program that rr record [options] COMMAND runs.
func rrRuns(args []word) [][]word {
	var out [][]word
	for _, c := range commandsAfter(args, nil) {
		if len(c) > 0 && c[0].text == "record" {
			out = append(out, commandsAfter(c[1:], rrValued)...)
		}
	}
	return out
}

// hyperfineCommandOptions are the options of hyperfine whose value is a shell
// command of its own, run before, after and around the timed ones.
var hyperfineCommandOptions = set("--prepare --setup --cleanup --conclude")

// hyperfineRuns are the shell commands hyperfine times and runs around them.
// Every word that is not an option is one (the value of an option that is not
// a command, like the number of --warmup 3, is read as one too, which does no
// harm), and so is the value of --prepare=CMD and its kin.
func hyperfineRuns(args []word) [][]word {
	var out [][]word
	for i, a := range args {
		switch t := a.text; {
		case t == "--":
			for _, r := range args[i+1:] {
				out = append(out, shellOf(r))
			}
			return out
		case strings.HasPrefix(t, "--"):
			if name, val, ok := strings.Cut(t, "="); ok && hyperfineCommandOptions[name] {
				out = append(out, shellWords(val))
			}
		case !strings.HasPrefix(t, "-") || t == "-":
			out = append(out, shellOf(a))
		default:
			// a cluster of short options: the first that takes a value takes
			// the rest of the word (-p'git push', -w3), or the next word when
			// it is the last letter, which the loop reads as a command
			for j := 1; j < len(t); j++ {
				if !strings.ContainsRune(hyperfineShortValued, rune(t[j])) {
					continue
				}
				if val := t[j+1:]; val != "" && strings.ContainsRune(hyperfineShortCommands, rune(t[j])) {
					out = append(out, shellWords(val))
				}
				break
			}
		}
	}
	return out
}

// hyperfineShortValued are the short options of hyperfine that take a value,
// and hyperfineShortCommands the ones whose value is a shell command
// (-p --prepare, -s --setup, -c --conclude, -C --cleanup).
const (
	hyperfineShortValued   = "wmMrpscCSLnu"
	hyperfineShortCommands = "pscC"
)
