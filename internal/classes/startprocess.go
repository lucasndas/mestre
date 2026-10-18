package classes

import "strings"

// Start-Process (and saps, its alias, and start, the alias in PowerShell and
// a command of cmd.exe) runs the program it names with the arguments of
// -ArgumentList. What it runs is a command of its own, so it is analysed as
// one: a fixed rule on it denies (git push, the CLI of an agent, an elevation
// command), as it would without the launcher
// (docs/classes/permissoes.md#regras-fixas-de-segurança).

// startParams are the parameters of Start-Process and the common ones of
// PowerShell, true for those that take a value and false for the switches.
var startParams = map[string]bool{
	"filepath": true, "argumentlist": true, "workingdirectory": true, "credential": true, "verb": true, "windowstyle": true,
	"redirectstandarderror": true, "redirectstandardinput": true, "redirectstandardoutput": true,
	"wait": false, "passthru": false, "nonewwindow": false, "loaduserprofile": false, "usenewenvironment": false,
	"erroraction": true, "warningaction": true, "informationaction": true, "errorvariable": true, "warningvariable": true,
	"informationvariable": true, "outvariable": true, "outbuffer": true, "pipelinevariable": true,
	"verbose": false, "debug": false, "whatif": false, "confirm": false,
}

// startAliases are the other names PowerShell gives those parameters.
var startAliases = map[string]string{
	"path": "filepath", "pspath": "filepath", "args": "argumentlist", "lup": "loaduserprofile", "nnw": "nonewwindow",
	"ea": "erroraction", "wa": "warningaction", "infa": "informationaction", "ev": "errorvariable", "wv": "warningvariable",
	"iv": "informationvariable", "ov": "outvariable", "ob": "outbuffer", "pv": "pipelinevariable", "vb": "verbose",
	"db": "debug", "wi": "whatif", "cf": "confirm",
}

// startParam is the parameter a name stands for: its own name, an alias or
// the one parameter that starts with it, as PowerShell takes an
// abbreviation. ok is false for a name that is not one of them or that
// stands for more than one.
func startParam(name string) (canon string, ok bool) {
	n := strings.ToLower(name)
	if c, found := startAliases[n]; found {
		return c, true
	}
	if _, found := startParams[n]; found {
		return n, true
	}
	for p := range startParams {
		if strings.HasPrefix(p, n) {
			if canon != "" && canon != p {
				return "", false
			}
			canon = p
		}
	}
	for a, p := range startAliases {
		if strings.HasPrefix(a, n) {
			if canon != "" && canon != p {
				return "", false
			}
			canon = p
		}
	}
	return canon, canon != ""
}

// isPwshParam reports an unquoted word that PowerShell reads as a parameter.
func isPwshParam(w word) bool {
	return !w.quoted && !w.expand && len(w.text) > 1 && w.text[0] == '-' && (w.text[1]|0x20 >= 'a' && w.text[1]|0x20 <= 'z')
}

// splitArguments are the arguments an -ArgumentList value stands for. The
// items of a list ('/c','git push') are joined with spaces into the command
// line of the program, which reads it as Windows does: spaces separate, and
// double quotes group (and are dropped). So '-c','"git push"' gives bash the
// command git push, and '-c','git push' gives it git, with push for $0.
func splitArguments(w word) []word {
	var out []word
	var cur strings.Builder
	quoted, started := false, false
	flush := func() {
		if started {
			t := cur.String()
			out = append(out, word{text: t, quoted: w.quoted, expand: w.expand, glob: w.glob && strings.ContainsAny(t, "*?["), assign: -1})
			cur.Reset()
			started = false
		}
	}
	for _, r := range w.text {
		switch {
		case r == '"':
			quoted, started = !quoted, true
		case !quoted && (r == ' ' || r == '\t' || r == ','):
			flush()
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	flush()
	return out
}

// startPowerShell reads the arguments of Start-Process as PowerShell does:
// the program is -FilePath or the first positional, the arguments are the
// -ArgumentList or the second positional. unknown is a parameter that is not
// known, so that the line is not read whole. files are the paths of its own
// parameters: the folder it starts in and the files it reads and writes the
// streams of.
func startPowerShell(args []word) (prog *word, argv []word, files []pathUse, unknown bool) {
	var pos []word
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !isPwshParam(a) {
			pos = append(pos, a)
			continue
		}
		name, attached, hasAttached := strings.Cut(a.text[1:], ":")
		canon, ok := startParam(name)
		if !ok {
			unknown = true
			continue
		}
		var val *word
		switch {
		case hasAttached:
			w := a
			w.text = attached
			val = &w
		case startParams[canon] && i+1 < len(args):
			i++
			val = &args[i]
		}
		switch canon {
		case "filepath":
			prog = val
		case "workingdirectory", "redirectstandardinput":
			if val != nil {
				files = append(files, pathUse{*val, use{}})
			}
		case "redirectstandardoutput", "redirectstandarderror":
			if val != nil {
				files = append(files, pathUse{*val, use{write: true}})
			}
		case "argumentlist":
			for val != nil {
				argv = append(argv, splitArguments(*val)...)
				// 'a', 'b': a word that ends with the comma goes on in the next.
				if !strings.HasSuffix(val.text, ",") || i+1 >= len(args) {
					break
				}
				i++
				val = &args[i]
			}
		}
	}
	if prog == nil && len(pos) > 0 {
		prog, pos = &pos[0], pos[1:]
	}
	for _, w := range pos { // the second positional is the -ArgumentList
		argv = append(argv, splitArguments(w)...)
	}
	return prog, argv, files, unknown
}

// startCmd reads the arguments of start as cmd.exe does: its switches (/wait,
// /b, /min, /d <folder>…), the title in quotes and then the command.
func startCmd(args []word) []word {
	i := 0
	flags := func() {
		for i < len(args) {
			t := strings.ToLower(args[i].text)
			if args[i].quoted || len(t) < 2 || t[0] != '/' || strings.ContainsAny(t[1:], `/\`) {
				return
			}
			i++
			if (t == "/d" || t == "/node" || t == "/affinity") && i < len(args) {
				i++
			}
		}
	}
	flags()
	if i < len(args) && args[i].quoted {
		i++ // the title
	}
	flags()
	return args[i:]
}

// startCommands are the commands a Start-Process line runs, as the words of
// a command each: the program followed by its arguments. For start both
// readings are given, the one of PowerShell and the one of cmd.exe, and the
// more restrictive holds.
func startCommands(cn string, args []word) (cmds [][]word, unknown bool) {
	prog, argv, _, unknown := startPowerShell(args)
	if prog != nil {
		cmds = append(cmds, append([]word{*prog}, argv...))
	}
	if cn == "start" {
		if cmd := startCmd(args); len(cmd) > 0 {
			cmds = append(cmds, cmd)
		}
	}
	return cmds, unknown
}

// startPathUses are the paths a Start-Process line names for the checks of
// the paths: those of its own parameters (they can write a file) and the
// program and the arguments it is given, which are the program's to read,
// but not the switches of cmd.exe (/c, /wait) that are not a path.
func startPathUses(args []word) []pathUse {
	prog, argv, files, _ := startPowerShell(args)
	uses := files
	words := argv
	if prog != nil {
		words = append([]word{*prog}, argv...)
	}
	for _, w := range words {
		if looksLikePath(w.text) && !isCmdSwitch(w.text) {
			uses = append(uses, pathUse{w, use{}})
		}
	}
	return uses
}

// isCmdSwitch reports a switch of cmd.exe: a slash and a word (/c, /wait).
func isCmdSwitch(t string) bool {
	return len(t) > 1 && t[0] == '/' && !strings.ContainsAny(t[1:], `/\.:,`)
}
