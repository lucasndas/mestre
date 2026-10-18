package classes

import "strings"

// The read-only commands (docs/classes/formato-das-permissoes.md#comandos)
// run by default, so what they read is judged as any path: the operands and
// the files their options name (grep -f, wc --files0-from, rg --ignore-file),
// in the forms with a space and with an equal sign. An option the command has
// without a value (cat -n, wc -c, ls -A, grep -E) must not swallow the file
// after it, so the options that take a value are those of each command, not
// a list for all of them.

// readValued are, for each read-only command, the options that take the next
// word as a value that is not a path (a count, a pattern, a glob, a word of
// a list): the word is not a path to read. An option that takes a file is not
// here: its value is read as a path (readFileOptions).
var readValued = map[string]map[string]bool{
	"head": set("-n -c --lines --bytes"),
	"tail": set("-n -c -s --lines --bytes --sleep-interval --pid --max-unchanged-stats"),
	"ls":   set("-I -T -w --ignore --hide --width --tabsize --block-size --sort --format --time --time-style --indicator-style --quoting-style"),
	"grep": set("-e -m -A -B -C -d -D --regexp --max-count --after-context --before-context --context --directories --devices --include --exclude --exclude-dir --label --binary-files"),
	"rg": set("-e -g -t -T -j -m -A -B -C -M -E -r -d --regexp --glob --iglob --type --type-not --type-add --type-clear --max-count --max-depth --max-filesize " +
		"--context --after-context --before-context --sort --sortr --color --colors --encoding --engine --replace --threads --max-columns --dfa-size-limit " +
		"--regex-size-limit --path-separator --field-context-separator --field-match-separator --context-separator --pre-glob --hyperlink-format --generate"),
}

// readFileOptions are the options that take a file to read: the value is a
// path, judged like an operand. -f is the file of patterns of grep and rg.
var readFileOptions = set("-f --file --files0-from --exclude-from --ignore-file")

// readPatternOptions are the options that give the patterns, so that the
// first operand is not one.
var readPatternOptions = set("-e -f --regexp --file")

// readsCodeOptions are the options of the read-only commands that run a
// program: rg --pre runs it on every file it searches, --hostname-bin runs
// one to name the host. A command with one of them is not only reading.
var readsCodeOptions = map[string]map[string]bool{
	"rg": set("--pre --hostname-bin"),
}

// readOnlyUses are the paths a read-only command reads and whether it is
// still only reading: its operands (the first of grep and rg is the pattern,
// unless an option gives them), the files named by the options, in any of
// their forms (-f F, -fF, --file F, --file=F, a cluster that ends in -f), and
// the value of an option written with an equal sign when it is a path.
func readOnlyUses(cn string, args []word, deep bool) (uses []pathUse, onlyReading bool) {
	valued, code := readValued[cn], readsCodeOptions[cn]
	grepLike := cn == "grep" || cn == "rg"
	onlyReading = true
	named := cn == "rg" && (hasFlag(args, "--files") || hasFlag(args, "--type-list")) // no pattern: rg --files <path>
	// named: an option gave the patterns, or the command takes none
	var operands []word
	file := func(w word, text string) {
		w.text = text
		uses = append(uses, pathUse{w, use{}})
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		t := a.text
		if t == "--" {
			operands = append(operands, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(t, "-") || t == "-" {
			operands = append(operands, a)
			continue
		}
		if !strings.HasPrefix(t, "--") {
			// A short option or a cluster of them (-n, -rn, -A3, -fFILE, -rf
			// FILE): the first letter that takes a value ends the cluster, and
			// the value is the rest of it or the next word.
			for j := 1; j < len(t); j++ {
				opt := "-" + t[j:j+1]
				isFile := opt == "-f" && grepLike
				if !isFile && !valued[opt] {
					continue
				}
				if isFile {
					named = true
				}
				named = named || readPatternOptions[opt]
				val, hasVal := t[j+1:], j+1 < len(t)
				switch {
				case !hasVal && i+1 < len(args):
					i++
					if isFile {
						file(args[i], args[i].text)
					}
				case hasVal && isFile:
					file(a, val)
				}
				break
			}
			if name, val, ok := strings.Cut(t, "="); ok && looksLikePath(val) && !valued[name] {
				file(a, val)
			}
			continue
		}
		name, val, hasVal := strings.Cut(t, "=")
		switch {
		case code[name]:
			onlyReading = false
			if !hasVal {
				i++ // its value is a program, not a path
			}
		case readFileOptions[name]:
			named = named || readPatternOptions[name]
			switch {
			case hasVal:
				file(a, val)
			case i+1 < len(args):
				i++
				file(args[i], args[i].text)
			}
		case valued[name]:
			named = named || readPatternOptions[name]
			if !hasVal {
				i++
			}
		case hasVal && looksLikePath(val):
			file(a, val) // --opt=PATH of an option that is not known to take anything else
		}
	}
	if (grepLike || patternFirst[cn]) && !named && len(operands) > 0 {
		operands = operands[1:] // the pattern
	}
	for _, w := range operands {
		uses = append(uses, pathUse{w, use{recursive: deep}})
	}
	return uses, onlyReading
}
