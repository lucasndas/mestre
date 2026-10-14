package classes

import (
	"path"
	"path/filepath"
	"strings"
)

// Links and programs made in the line. A link is a write of the link and,
// through it, of its target: ln -s ../.git/hooks/pre-commit src/e is a
// write of .git/hooks/pre-commit, a fixed denial
// (docs/classes/permissoes.md#regras-fixas-de-segurança). A program the
// line writes before it runs it (ln -s, cp, install, chmod +x, a
// redirection) is not one the text shows: the rest of the line is at least
// ask and medio, and a link or copy of a program is analysed as that
// program too (ln -s /usr/bin/git g && ./g push is git push).

// linkPair is one link a command makes.
type linkPair struct {
	link, target word
	// fromLink: a relative target is read from the link's folder (a
	// symbolic link); otherwise from the current folder (a hard link, ln
	// -r).
	fromLink bool
}

// targetWord is the target as a path from the current folder.
func (lp linkPair) targetWord() word {
	t := lp.target
	slash := strings.ReplaceAll(t.text, `\`, "/")
	if !lp.fromLink || t.tilde || t.expand || strings.HasPrefix(slash, "/") || filepath.IsAbs(t.text) || len(slash) > 1 && slash[1] == ':' {
		return t
	}
	t.text = path.Dir(strings.ReplaceAll(lp.link.text, `\`, "/")) + "/" + slash
	t.glob = t.glob || lp.link.glob
	t.expand = t.expand || lp.link.expand
	return t
}

// baseIn is the word dir/base(of): where a link or a copy into a folder
// lands.
func baseIn(dir, of word) word {
	nw := dir
	nw.text = strings.TrimRight(dir.text, `/\`) + "/" + filepath.Base(filepath.FromSlash(strings.ReplaceAll(of.text, `\`, "/")))
	nw.glob = dir.glob || of.glob
	nw.expand = dir.expand || of.expand
	return nw
}

// linkPairs are the links ln, link and mklink make. With one destination
// that may be a folder, both readings are kept.
func linkPairs(cn string, args []word) []linkPair {
	var out []linkPair
	switch cn {
	case "mklink":
		var o []word
		symbolic := true
		for _, a := range args {
			switch strings.ToLower(a.text) {
			case "/h", "/j":
				symbolic = false
			case "/d":
			default:
				o = append(o, a)
			}
		}
		if len(o) >= 2 {
			out = append(out, linkPair{link: o[0], target: o[1]})
			if symbolic {
				out = append(out, linkPair{link: o[0], target: o[1], fromLink: true})
			}
		}
		return out
	}
	symbolic := cn == "ln" && (hasShortFlag(args, 's') || hasFlag(args, "--symbolic"))
	relative := hasShortFlag(args, 'r') || hasFlag(args, "--relative")
	from := symbolic && !relative
	o := operandWords(args, map[string]bool{"-t": true, "-S": true, "--suffix": true, "--target-directory": true})
	if t := flagValue(args, "-t", "--target-directory"); t != nil {
		for _, s := range o {
			out = append(out, linkPair{link: baseIn(*t, s), target: s, fromLink: from})
		}
		return out
	}
	switch len(o) {
	case 0:
	case 1:
		// ln -s x: the link is ./base(x)
		out = append(out, linkPair{link: baseIn(word{text: ".", assign: -1}, o[0]), target: o[0], fromLink: from})
	default:
		dest, targets := o[len(o)-1], o[:len(o)-1]
		for _, s := range targets {
			if len(targets) == 1 {
				out = append(out, linkPair{link: dest, target: s, fromLink: from})
			}
			out = append(out, linkPair{link: baseIn(dest, s), target: s, fromLink: from})
		}
	}
	return out
}

// operandWords are the words that are not options; the options of valued
// consume the next word.
func operandWords(args []word, valued map[string]bool) []word {
	var o []word
	for i := 0; i < len(args); i++ {
		a := args[i].text
		if a == "--" {
			return append(o, args[i+1:]...)
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			if valued[a] {
				i++
			}
			continue
		}
		o = append(o, args[i])
	}
	return o
}

// copyPairs are the files cp, install and mv make from one source: the
// copy is that program.
func copyPairs(cn string, args []word) []linkPair {
	if cn != "cp" && cn != "install" && cn != "mv" {
		return nil
	}
	ws := operandWords(args, map[string]bool{"-t": true, "-S": true, "-m": true, "-o": true, "-g": true, "--suffix": true, "--target-directory": true})
	if t := flagValue(args, "-t", "--target-directory"); t != nil {
		var out []linkPair
		for _, s := range ws {
			out = append(out, linkPair{link: baseIn(*t, s), target: s})
		}
		return out
	}
	if len(ws) != 2 {
		return nil
	}
	return []linkPair{{link: ws[1], target: ws[0]}, {link: baseIn(ws[1], ws[0]), target: ws[0]}}
}

// lineFacts are what the parts of a line made so far: the paths written,
// and the links and copies of a program, by key, with the key of the
// program.
type lineFacts struct {
	written map[string]bool
	links   map[string]string
}

func newLineFacts() *lineFacts {
	return &lineFacts{written: map[string]bool{}, links: map[string]string{}}
}

// wordKeys are the keys a literal path argument names, tilde included.
func (e *evaluator) wordKeys(a word, st *shellState) ([]string, bool) {
	if a.expand || a.text == "" || a.glob {
		return nil, false
	}
	text := a.text
	if a.tilde {
		if e.env.Home == "" {
			return nil, false
		}
		text = e.env.Home + text[1:]
	}
	return e.resolve(text, st)
}

// noteEffects records what a simple command writes, links and copies, for
// the parts after it.
func (e *evaluator) noteEffects(p *simple, d dialect, st *shellState) {
	if st.facts == nil {
		return
	}
	for _, r := range p.redirs {
		if r.writes() && !isDevice(r.target.text) {
			e.noteWritten(r.target, st)
		}
	}
	ws := p.words
	if len(ws) == 0 {
		return
	}
	cn := canonical(ws[0].text)
	args := ws[1:]
	pairs := append(linkPairs(cn, args), copyPairs(cn, args)...)
	if cn != "ln" && cn != "link" && cn != "mklink" {
		// What the command writes is a program created in the line (its
		// content is now unknown). The synthetic write of a link's target
		// is not such a creation, so ln is left out here.
		uses, _ := pathUses(cn, args, d)
		for _, u := range uses {
			if u.write {
				e.noteWritten(u.w, st)
			}
		}
	}
	for _, lp := range pairs {
		lks, ok := e.wordKeys(lp.link, st)
		if !ok {
			continue
		}
		// The link path itself is created: running it is unanalysable
		// unless it maps to a known program.
		for _, k := range lks {
			st.facts.written[k] = true
		}
		tks, ok := e.wordKeys(lp.targetWord(), st)
		if !ok || len(tks) == 0 {
			continue
		}
		for _, k := range lks {
			st.facts.links[k] = tks[0]
		}
	}
}

func (e *evaluator) noteWritten(w word, st *shellState) {
	if st.facts == nil {
		return
	}
	ks, _ := e.wordKeys(w, st)
	for _, k := range ks {
		st.facts.written[k] = true
	}
}

// madeProgram is what the line made of the program a command runs: written
// is true when the program, or the script an interpreter runs, was written
// earlier in the line; targets are the programs it stands for (a link or a
// copy made earlier in the line, or a link on disk to another program).
func (e *evaluator) madeProgram(cn string, ws []word, st *shellState) (written bool, targets []string) {
	name := ws[0]
	byPath := !name.expand && (strings.ContainsAny(name.text, `/\`) || strings.HasPrefix(name.text, ".") && cn != ".")
	var cands []word
	if byPath {
		cands = append(cands, name)
	}
	cands = append(cands, scriptsOf(cn, ws[1:])...)
	for i, c := range cands {
		ks, ok := e.wordKeys(c, st)
		if !ok {
			continue
		}
		for _, k := range ks {
			if st.facts != nil && st.facts.written[k] {
				written = true
			}
			if i > 0 || !byPath {
				continue
			}
			if t, ok := st.facts.linkOf(k); ok {
				targets = append(targets, t)
			} else if b := canonical(k); b != cn && b != "" {
				// a link on disk (Normalize resolved it) to another program
				targets = append(targets, k)
			}
		}
	}
	return written, targets
}

func (f *lineFacts) linkOf(k string) (string, bool) {
	if f == nil {
		return "", false
	}
	t, ok := f.links[k]
	return t, ok
}

// asProgram is ws with the program replaced by the one at key k.
func asProgram(k string, ws []word) []word {
	return append([]word{{text: filepath.FromSlash(k), assign: -1}}, ws[1:]...)
}

// scriptsOf is the file an interpreter or shell runs as its program (the
// one whose being written earlier in the line makes the command
// unanalysable): the first operand of an interpreter, the script of a
// shell.
func scriptsOf(cn string, args []word) []word {
	if w, ok := interpreterScript(cn, args); ok && !w.expand {
		return []word{w}
	}
	return nil
}
