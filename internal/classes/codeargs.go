package classes

import (
	"regexp"
	"strings"
)

// Programs that run a command given in an argument
// (docs/classes/lista-de-seguros.md#o-que-fica-fora): tmux new 'cmd',
// screen -dm cmd, expect -c 'spawn cmd', vim -c '!cmd', less +!cmd,
// man -P cmd, gdb -ex 'shell cmd', tar -I cmd, sort --compress-program=cmd,
// make --eval='t:;cmd', zip -TT cmd, rsync -e cmd, ssh -o ProxyCommand=cmd,
// lldb -o 'platform shell cmd', and the inline code of an interpreter (or of a
// debugger: gdb -ex 'python …') that calls system("cmd"). None of
// them is in the list of known-safe programs, so the part is medio; when the
// command is a literal the motor reads, it is analysed as a command line
// too, so a fixed rule in it denies.

// argCommands are the literal command lines a program runs from its
// arguments.
func argCommands(cn string, args []string) []string {
	base := normalizeInterpreter(cn)
	switch {
	case cn == "tmux":
		return tmuxCommands(args)
	case cn == "screen":
		return screenCommands(args)
	case cn == "expect":
		return scriptCalls(optValues(args, "-c"), expectCall)
	case vimNames[cn]:
		var out []string
		for _, v := range append(optValues(args, "-c", "--cmd"), plusArgs(args)...) {
			if _, after, ok := strings.Cut(v, "!"); ok && strings.TrimSpace(after) != "" {
				out = append(out, after)
			}
		}
		return out
	case cn == "emacs" || cn == "emacsclient":
		return scriptCalls(optValues(args, "--eval", "-eval", "--execute"), emacsCall)
	case cn == "less" || cn == "more":
		var out []string
		for _, v := range plusArgs(args) {
			if after, ok := strings.CutPrefix(strings.TrimLeft(v, "+"), "!"); ok && strings.TrimSpace(after) != "" {
				out = append(out, after)
			}
		}
		return out
	case cn == "man":
		return optValues(args, "-P", "--pager", "-H", "--html")
	case cn == "gdb":
		var out []string
		for _, v := range optValues(args, gdbCommandOptions...) {
			// a value may hold several commands, one per line (a body of if,
			// while or define)
			for _, line := range strings.Split(v, "\n") {
				line = gdbUnwrap(line)
				if cmd, ok := gdbShellCommand(line); ok {
					out = append(out, cmd)
				} else if after, ok := gdbExecWrapper(line); ok {
					out = append(out, after)
				} else {
					// python import os; os.system("cmd"), call (int)system("cmd")
					out = append(out, scriptCalls([]string{strings.TrimSpace(line)}, codeCall)...)
				}
			}
		}
		return out
	case cn == "lldb":
		var out []string
		for _, v := range optValues(args, "-o", "--one-line", "-O", "--one-line-before-file", "-k", "--one-line-on-crash") {
			v = lldbUnalias(v)
			if cmd, ok := lldbShellCommand(v); ok {
				out = append(out, cmd)
			} else {
				v = strings.TrimSpace(v)
				out = append(out, scriptCalls([]string{v}, codeCall)...)
			}
		}
		return out
	case cn == "tar" || cn == "bsdtar" || cn == "gtar":
		out := optValues(args, "-I", "--use-compress-program", "--to-command", "--rsh-command", "--rmt-command")
		for _, v := range optValues(args, "--checkpoint-action") {
			if after, ok := strings.CutPrefix(v, "exec="); ok {
				out = append(out, after)
			}
		}
		return out
	case cn == "sort":
		return optValues(args, "--compress-program")
	case cn == "make" || cn == "gmake":
		var out []string
		for _, v := range optValues(args, "--eval", "-E") {
			out = append(out, makeRecipes(v)...)
		}
		return out
	case cn == "zip":
		return optValues(args, "-TT", "--unzip-command")
	case cn == "rsync":
		return optValues(args, "-e", "--rsh", "--rsync-path")
	case cn == "ssh" || cn == "scp" || cn == "sftp":
		var out []string
		for _, v := range optValues(args, "-o") {
			k, val, ok := strings.Cut(v, "=")
			if !ok {
				k, val, ok = strings.Cut(v, " ")
			}
			if l := strings.ToLower(k); ok && (l == "proxycommand" || l == "localcommand" || l == "knownhostscommand") {
				out = append(out, val)
			}
		}
		return out
	case inlineInterpreters[base] || stdinCode[base] || base == "r" || base == "julia":
		return scriptCalls(args, codeCall)
	}
	return nil
}

// gdbShellRe and gdbTargetRe read the gdb commands that run a shell command: gdb
// takes any unambiguous prefix of a command, and the ones it would not take
// are denied all the same (fail closed): sh, she, shel and shell, ! , pipe (and
// |), and target … | command.
var (
	gdbShellRe  = regexp.MustCompile(`^\s*(?:!|sh(?:e(?:l(?:l)?)?)?\b)\s*(.*)$`)
	gdbPipeRe   = regexp.MustCompile(`^\s*(?:\||pi(?:p(?:e)?)?\b)\s*(.*)$`)
	gdbTargetRe = regexp.MustCompile(`^\s*ta(?:r(?:g(?:e(?:t)?)?)?)?\s+\S+[^|]*?\|\s*(.+)$`)
)

// gdbCommandOptions are the options of gdb whose value is a command: the long
// ones with one dash or two, and the unambiguous prefixes gdb takes of them.
var gdbCommandOptions = []string{"-ex", "--ex", "-iex", "--iex",
	"--eval-command", "-eval-command", "--eval-comman", "--eval", "-eval", "--ev", "-ev",
	"--init-eval-command", "-init-eval-command", "--init-eval", "-init-eval", "--init-ev", "-init-ev"}

var (
	gdbWithRe  = regexp.MustCompile(`^\s*w(?:i(?:t(?:h)?)?)?\s+.*?\s--\s+(.+)$`)
	gdbAliasRe = regexp.MustCompile(`^\s*al(?:i(?:a(?:s)?)?)?\s+(?:-\S+\s+)*[^=]+=\s*(.+)$`)
	gdbWrapRe  = regexp.MustCompile(`^\s*set\s+exec-wrapper\s+(.+)$`)
)

// gdbUnwrap is the command that gdb's with SETTING -- COMMAND and alias
// NAME = COMMAND hold (the alias runs when it is used; the definition is
// where the command is written, so it is read there).
func gdbUnwrap(line string) string {
	for range 4 {
		if m := gdbWithRe.FindStringSubmatch(line); m != nil {
			line = m[1]
		} else if m := gdbAliasRe.FindStringSubmatch(line); m != nil {
			line = m[1]
		} else {
			break
		}
	}
	return line
}

// gdbExecWrapper is the command line of set exec-wrapper: the program gdb
// starts the debugged program through.
func gdbExecWrapper(line string) (string, bool) {
	if m := gdbWrapRe.FindStringSubmatch(line); m != nil {
		return strings.TrimSpace(m[1]), true
	}
	return "", false
}

// lldbAliasRe is command alias [options] NAME COMMAND…: the command is read
// where it is written.
var lldbAliasRe = regexp.MustCompile(`^\s*(?:command|comm\w*)\s+alias\s+(?:-\S+\s+)*\S+\s+(.+)$`)

func lldbUnalias(v string) string {
	if m := lldbAliasRe.FindStringSubmatch(v); m != nil {
		return m[1]
	}
	return v
}

// gdbShellCommand is the shell command line a gdb command runs, when it is
// one of shell, ! , pipe, | or target remote |: pipe COMMAND | SHELL_COMMAND
// runs what follows the first | (or the delimiter of pipe -d DELIM COMMAND
// DELIM SHELL_COMMAND).
func gdbShellCommand(v string) (string, bool) {
	if m := gdbShellRe.FindStringSubmatch(v); m != nil && strings.TrimSpace(m[1]) != "" {
		return strings.TrimSpace(m[1]), true
	}
	if m := gdbTargetRe.FindStringSubmatch(v); m != nil {
		return strings.TrimSpace(m[1]), true
	}
	if m := gdbPipeRe.FindStringSubmatch(v); m != nil {
		rest := strings.TrimSpace(m[1])
		delim := "|"
		if after, ok := strings.CutPrefix(rest, "-d"); ok {
			fields := strings.Fields(after)
			if len(fields) > 0 {
				delim = fields[0]
				rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(after), delim))
			}
		}
		if _, shell, ok := strings.Cut(rest, delim); ok && strings.TrimSpace(shell) != "" {
			return strings.TrimSpace(shell), true
		}
	}
	return "", false
}

// lldbShellRe reads platform shell [options] [--] COMMAND with the prefixes
// lldb takes for its command and subcommand (and shell by itself).
var lldbShellRe = regexp.MustCompile(`^\s*(?:pl(?:a(?:t(?:f(?:o(?:r(?:m)?)?)?)?)?)?\s+)?s(?:h(?:e(?:l(?:l)?)?)?)?\s+(.*)$`)

// lldbShellCommand is the command line that platform shell (or shell) runs.
func lldbShellCommand(v string) (string, bool) {
	m := lldbShellRe.FindStringSubmatch(v)
	if m == nil {
		return "", false
	}
	rest := strings.TrimSpace(m[1])
	for strings.HasPrefix(rest, "-") && rest != "-" {
		word, after := cutField(rest)
		if word == "--" {
			rest = after
			break
		}
		if word == "-t" || word == "-s" { // the timeout and the shell take a value
			_, after = cutField(after)
		}
		rest = after
	}
	return rest, rest != ""
}

// cutField cuts the first word, up to a space or a tab, off the text and
// returns it with the rest, trimmed.
func cutField(s string) (word, rest string) {
	s = strings.TrimSpace(s)
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i:])
}

var vimNames = set("vim vi nvim view ex gvim vimdiff rvim evim gview nvi elvis")

// optValues are the values of the options: -x v, -xv, --long v and
// --long=v.
func optValues(args []string, names ...string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		for _, n := range names {
			switch {
			case a == n && i+1 < len(args):
				out = append(out, args[i+1])
				i++
			case strings.HasPrefix(n, "--") && strings.HasPrefix(a, n+"="):
				out = append(out, a[len(n)+1:])
			case !strings.HasPrefix(n, "--") && len(a) > len(n) && strings.HasPrefix(a, n) && !strings.HasPrefix(a, "--"):
				out = append(out, strings.TrimPrefix(a[len(n):], "="))
			default:
				continue
			}
			break
		}
	}
	return out
}

// plusArgs are the +cmd arguments of an editor or a pager.
func plusArgs(args []string) []string {
	var out []string
	for _, a := range args {
		if strings.HasPrefix(a, "+") && len(a) > 1 {
			out = append(out, a[1:])
		}
	}
	return out
}

// tmuxShellSubs are the tmux commands whose operands are a shell command;
// tmuxKeys type keys into a shell.
var (
	tmuxShellSubs = set("new-session new new-window neww split-window splitw respawn-pane respawnp respawn-window respawnw " +
		"run-shell run display-popup popup pipe-pane pipep if-shell if")
	tmuxKeys   = set("send-keys send")
	tmuxValued = set("-c -e -F -n -s -t -x -y -f -l -p -T -b -h -w -S -L -E -B -I -M -O -R -X")
)

func tmuxCommands(args []string) []string {
	// global options of tmux, then the commands, split at ;
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		if args[i] == "-L" || args[i] == "-S" || args[i] == "-f" || args[i] == "-c" || args[i] == "-T" {
			if args[i] == "-c" && i+1 < len(args) {
				return []string{args[i+1]} // tmux -c 'cmd': a shell command
			}
			i++
		}
		i++
	}
	var out []string
	cmd := []string{}
	flush := func() {
		if len(cmd) == 0 {
			return
		}
		sub, rest := cmd[0], cmd[1:]
		var ops []string
		for j := 0; j < len(rest); j++ {
			if strings.HasPrefix(rest[j], "-") && len(ops) == 0 {
				if tmuxValued[rest[j]] {
					j++
				}
				continue
			}
			ops = append(ops, rest[j])
		}
		switch {
		case tmuxShellSubs[sub] && len(ops) > 0:
			out = append(out, strings.Join(ops, " "))
		case tmuxKeys[sub] && len(ops) > 0:
			out = append(out, strings.Join(ops, " "))
		}
		cmd = cmd[:0]
	}
	for ; i < len(args); i++ {
		if args[i] == ";" || args[i] == `\;` {
			flush()
			continue
		}
		cmd = append(cmd, args[i])
	}
	flush()
	return out
}

func screenCommands(args []string) []string {
	valued := set("-S -c -e -h -p -T -t -L -Logfile -s -x -r -R -D -d")
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-X":
			rest := args[i+1:]
			if len(rest) > 1 && (rest[0] == "stuff" || rest[0] == "exec" || rest[0] == "screen") {
				t := strings.Join(rest[1:], " ")
				for _, esc := range []string{`\n`, `\r`, "^M", "^J"} {
					t = strings.ReplaceAll(t, esc, "\n")
				}
				return []string{t}
			}
			return nil
		case strings.HasPrefix(a, "-"):
			if valued[a] && a != "-d" && a != "-r" && a != "-R" && a != "-D" && a != "-x" {
				i++
			}
		default:
			return []string{strings.Join(args[i:], " ")}
		}
	}
	return nil
}

// makeRecipes are the commands of a makefile text given to --eval: the
// recipe after the ; of a rule, the lines that start with a tab, and the
// text of $(shell …).
func makeRecipes(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "\t"):
			out = append(out, strings.TrimSpace(line))
		case strings.Contains(line, ":"):
			_, after, _ := strings.Cut(line, ":")
			if _, recipe, ok := strings.Cut(after, ";"); ok {
				out = append(out, recipe)
			}
		}
		for _, m := range makeShell.FindAllStringSubmatch(line, -1) {
			out = append(out, m[1])
		}
	}
	return out
}

var makeShell = regexp.MustCompile(`\$[({]shell\s+([^)}]*)[)}]`)

var (
	// codeCall finds the literal command an interpreter's inline code runs:
	// system("cmd"), os.system('cmd'), subprocess.run("cmd", shell=True),
	// execSync('cmd'), exec("cmd"), shell_exec, popen, os.execute, run(`cmd`)
	// and the backtick strings of ruby, perl and julia.
	codeCall   = regexp.MustCompile("(?:system|popen|exec|execSync|spawnSync|spawn|shell_exec|passthru|run|call|check_call|check_output|getoutput|getstatusoutput|Popen|execute|qx)\\s*\\(?\\s*(?:\"((?:\\\\.|[^\"\\\\])*)\"|'((?:\\\\.|[^'\\\\])*)'|`([^`]*)`|\\[([^\\]]*)\\])|`([^`]*)`|do shell script\\s+\"([^\"]*)\"")
	expectCall = regexp.MustCompile(`(?:spawn|exec|system)\s+([^;\n\]]+)`)
	emacsCall  = regexp.MustCompile(`(?:shell-command|async-shell-command|call-process-shell-command|shell-command-to-string)\s+"((?:\\.|[^"\\])*)"`)
	listItem   = regexp.MustCompile(`"((?:\\.|[^"\\])*)"|'((?:\\.|[^'\\])*)'`)
)

// scriptCalls are the literal commands the code texts run, by re: the
// first group that matched; a list (["git", "push"]) is joined by spaces.
func scriptCalls(texts []string, re *regexp.Regexp) []string {
	var out []string
	for _, t := range texts {
		for _, m := range re.FindAllStringSubmatch(t, -1) {
			for g := 1; g < len(m); g++ {
				if m[g] == "" {
					continue
				}
				if re == codeCall && g == 4 {
					// the list form of subprocess and spawn
					var words []string
					for _, it := range listItem.FindAllStringSubmatch(m[g], -1) {
						words = append(words, it[1]+it[2])
					}
					if len(words) > 0 {
						out = append(out, strings.Join(words, " "))
					}
					break
				}
				out = append(out, m[g])
				break
			}
		}
	}
	return out
}

// findExecs are the commands of the -exec, -execdir, -ok and -okdir of a
// find, each up to its ; or + (without one, find runs nothing).
func findExecs(args []word) [][]word {
	var out [][]word
	for i := 0; i < len(args); i++ {
		switch args[i].text {
		case "-exec", "-execdir", "-ok", "-okdir":
			j := i + 1
			for j < len(args) && args[j].text != ";" && args[j].text != "+" {
				j++
			}
			if j > i+1 && j < len(args) {
				out = append(out, args[i+1:j])
			}
			i = j
		}
	}
	return out
}

// gitRunsProgram is why a git command line uses a feature that runs a
// program (an alias or a config key that runs one, given by -c or
// --config-env; rebase -x, bisect run, difftool -x, grep -O), with the
// literal commands it runs: alto in the risk, and never run on its own by
// the decision (git.go).
func gitRunsProgram(args []word) (why string, cmds []string) {
	config := func(raw string, unread bool) {
		key, val, _ := strings.Cut(raw, "=")
		switch {
		case strings.HasPrefix(strings.ToLower(key), "alias."):
			why = "git define um alias na linha de comando"
			if cv := strings.TrimSpace(val); !unread && strings.HasPrefix(cv, "!") {
				cmds = append(cmds, strings.TrimPrefix(cv, "!"))
			}
		case configRunsProgram(key):
			why = "git " + key + " roda um programa"
			if !unread && val != "" && configValueIsCommand(key) {
				cmds = append(cmds, val)
			}
		}
	}
	i := 0
	for i < len(args) {
		a := args[i].text
		if !strings.HasPrefix(a, "-") || a == "-" {
			break
		}
		switch {
		case a == "-c" && i+1 < len(args):
			i++
			config(args[i].text, args[i].expand)
		case strings.HasPrefix(a, "-c") && !strings.HasPrefix(a, "--"):
			config(a[2:], args[i].expand)
		case a == "--config-env" && i+1 < len(args):
			i++
			config(args[i].text, true)
		case strings.HasPrefix(a, "--config-env="):
			config(strings.TrimPrefix(a, "--config-env="), true)
		default:
			if name, _, hasVal := strings.Cut(a, "="); gitGlobalValue[name] && !hasVal {
				i++
			}
		}
		i++
	}
	if i >= len(args) {
		return why, cmds
	}
	sub, rest := args[i].text, args[i+1:]
	ext := func(cmd string) {
		why = "git " + sub + " roda um comando externo"
		if strings.TrimSpace(cmd) != "" {
			cmds = append(cmds, cmd)
		}
	}
	switch sub {
	case "rebase", "am", "sequence":
		if w := flagValue(rest, "-x", "--exec"); w != nil {
			ext(w.text)
		}
	case "bisect":
		if ps := positionals(rest, nil); len(ps) > 0 && ps[0] == "run" {
			ext(afterWord(rest, "run"))
		}
	case "difftool":
		if w := flagValue(rest, "-x", "--extcmd"); w != nil {
			ext(w.text)
		}
	case "grep":
		if v, ok := grepOpenPager(rest); ok {
			ext(v)
		}
	}
	return why, cmds
}
