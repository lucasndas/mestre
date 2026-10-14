package providers

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ScreenCall finds the call a native permission screen asks about, in its
// dialog (ScreenResult.Dialog; a whole screen works too): -1 when no call
// is there, and the screen is denied. Only the dialog counts, from the top
// of its box (or its header) to the menu at the bottom, so a line of the
// model above it is never the call (docs/provedores/adaptador.md).
//
//   - A call with a command is on the dialog when the command of the dialog
//     is exactly it, nothing more: the lines between the dialog's own texts
//     (its title or header, the "Reason:" of Codex, the question above the
//     menu, a "$ " before the first line) make up the command line by line,
//     or the command and then its description (Claude Code). A line break
//     inside a line of the command is a wrap of the screen only where the
//     box of the dialog shows the line was full (the next word did not
//     fit, or the line reaches the border); without a box with sides, a
//     command the screen wrapped is not recognized.
//   - A call with a path (a write, an edit) is on a dialog about a file
//     where a word is that path, exactly (relative to the call's Cwd, when
//     it has one), and no other word names another file.
//   - A call with a URL, likewise with the URL.
//
// When several calls are on the dialog the most recent one is taken,
// unless one of them was denied: then the most recent denied. The caller
// drops a call once it ends or its decision answered a screen, so a stale
// decision never answers the screen of another call.
func ScreenCall(dialog string, calls []CallDecision) int {
	lines := strings.Split(dialog, "\n")
	m, ok := bottomMenu(lines)
	if !ok {
		return -1
	}
	top, boxed := boxTop(lines, m)
	if !boxed {
		top = openTop(lines, m)
	}
	body := dialogBody(lines[top:m.top])
	found, choice := -1, ""
	for i := len(calls) - 1; i >= 0; i-- {
		if callOnDialog(calls[i], body) && (found < 0 || calls[i].Choice == "deny" && choice != "deny") {
			found, choice = i, calls[i].Choice
		}
	}
	return found
}

// openTop is the first line of a dialog drawn without a box over the menu
// m: the topmost title of a command dialog or Codex header above the menu
// (the topmost, so a title in the text of a command cannot cut the dialog
// shorter; one in the output of the model above only makes the dialog
// longer, and its content no call), else the first line of the paragraph
// right above the menu (Antigravity draws the command over its question).
func openTop(lines []string, m menu) int {
	for i := 0; i < m.top; i++ {
		if t := strings.TrimSpace(lines[i]); t == codexHeader || containsString(dialogTitles, t) || containsString(fileTitles, t) {
			return i
		}
	}
	i := m.top
	for i > 0 && strings.TrimSpace(lines[i-1]) != "" {
		i--
	}
	return i
}

// dline is one line of the body of a dialog: its text without the frame
// and the blanks around it, and, inside a box with sides, the column where
// the text ends and the last column the text may reach (0 without a box).
type dline struct {
	text        string
	end, maxEnd int
}

// dialogBody is the body of a dialog, its frame taken off. When every
// non-empty line has the sides of a box (│ … │), the columns of the text
// are kept, for the wraps; the padding is the smallest indentation inside
// the box.
func dialogBody(lines []string) []dline {
	out := make([]dline, len(lines))
	sided, pad := true, -1
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if !strings.HasPrefix(t, "│") || !strings.HasSuffix(t, "│") || utf8.RuneCountInString(t) < 2 {
			sided = false
			break
		}
	}
	for i, l := range lines {
		r := []rune(l)
		left, right := -1, -1
		for j, c := range r {
			if c == '│' {
				if left < 0 {
					left = j
				}
				right = j
			}
		}
		if !sided || left < 0 || left == right {
			out[i].text = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "│┃▌"))
			continue
		}
		inner := string(r[left+1 : right])
		t := strings.TrimSpace(inner)
		lead := utf8.RuneCountInString(inner) - utf8.RuneCountInString(strings.TrimLeft(inner, " "))
		out[i] = dline{text: t, end: left + 1 + lead + utf8.RuneCountInString(t), maxEnd: right}
		if t != "" && (pad < 0 || lead < pad) {
			pad = lead
		}
	}
	if sided && pad > 0 {
		for i := range out {
			if out[i].maxEnd > 0 {
				out[i].maxEnd -= pad
			}
		}
	}
	return out
}

// The texts a dialog draws around the command: the question above the menu
// (Claude Code, Antigravity), the title above the command (Claude Code and
// its fake), the header of the Codex approval and its reason.
var (
	dialogQuestions = []string{"Do you want to proceed?", "Run this command?"}
	dialogTitles    = []string{"Bash command", "PowerShell command", "Bash", "PowerShell"}
	fileTitles      = []string{"Create file", "Edit file", "Write file", "Overwrite file"}
	codexHeader     = "Would you like to run the following command?"
	// commandDialog and fileDialog tell what a dialog asks about, by its
	// own texts: a path never answers a dialog about a command, nor a
	// command one about a file.
	commandDialog = regexp.MustCompile(`(?m)^(?:Do you want to proceed\?|Run this command\?|Would you like to run the following command\?|(?:Bash|PowerShell)(?: command)?)$`)
	fileDialog    = regexp.MustCompile(`(?im)^(?:Do you want to (?:create|make this edit to|overwrite|write) .+\?|Allow [^\n]*\bfile\b[^\n]*\?|(?:Create|Edit|Write|Overwrite) file)$`)
)

// callOnDialog reports whether the dialog body shows the call.
func callOnDialog(c CallDecision, body []dline) bool {
	t := c.Tool
	if t == nil {
		return false
	}
	texts := make([]string, len(body))
	for i, l := range body {
		texts[i] = l.text
	}
	joined := strings.Join(texts, "\n")
	file, command := fileDialog.MatchString(joined), commandDialog.MatchString(joined)
	switch {
	case t.Command != "":
		sub, ok := commandSubject(body)
		if !ok || file {
			return false
		}
		if matchLines(sub, t.Command) {
			return true
		}
		var in struct {
			Description string `json:"description"`
		}
		return json.Unmarshal(t.RawInput, &in) == nil && in.Description != "" && matchLines(sub, t.Command+"\n"+in.Description)
	case len(t.Paths) > 0:
		return file && !command && wordsName(texts, t.Paths[0], c.Cwd, isPathWord)
	case t.URL != "":
		return !file && !command && wordsName(texts, t.URL, "", isURLWord)
	}
	return false
}

// commandSubject is the part of the body that is the command, the dialog's
// own texts taken off by their place: with the question above the menu,
// a title on the first line; without it, the Codex header, its "Reason:"
// paragraph and the "$ " of the first line of the command, which Codex
// always draws. ok is false when the body has neither form.
func commandSubject(body []dline) ([]dline, bool) {
	blank := func(l dline) bool { return l.text == "" }
	trim := func(b []dline) []dline {
		for len(b) > 0 && blank(b[0]) {
			b = b[1:]
		}
		for len(b) > 0 && blank(b[len(b)-1]) {
			b = b[:len(b)-1]
		}
		return b
	}
	b := trim(body)
	if len(b) == 0 {
		return nil, false
	}
	switch last := b[len(b)-1].text; {
	case containsString(dialogQuestions, last):
		b = trim(b[:len(b)-1])
		if len(b) > 0 && containsString(dialogTitles, b[0].text) {
			b = trim(b[1:])
		}
	case b[0].text == codexHeader:
		b = trim(b[1:])
		if len(b) > 0 && strings.HasPrefix(b[0].text, "Reason:") {
			for len(b) > 0 && !blank(b[0]) {
				b = b[1:]
			}
			b = trim(b)
		}
		if len(b) == 0 || !strings.HasPrefix(b[0].text, "$ ") {
			return nil, false
		}
	default:
		return nil, false
	}
	if len(b) == 0 {
		return nil, false
	}
	b = append([]dline{b[0]}, b[1:]...)
	if s, ok := strings.CutPrefix(b[0].text, "$ "); ok {
		b[0].text = strings.TrimSpace(s)
	}
	return b, true
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// matchLines reports whether the lines are exactly text: each line of the
// text (blanks around it ignored, as the dialog indents) is one line of
// the dialog, or several where the box shows the screen wrapped it: a
// break at a blank where the next word did not fit, a break inside a word
// where the line reaches the border.
func matchLines(sub []dline, text string) bool {
	logical := strings.Split(strings.Trim(strings.ReplaceAll(text, "\r\n", "\n"), " \t\n"), "\n")
	for i := range logical {
		logical[i] = strings.TrimSpace(logical[i])
	}
	j, k := 0, 0 // the line of the text and how much of it is matched
	for i, l := range sub {
		if j >= len(logical) {
			return false
		}
		cur := logical[j]
		if k == 0 {
			if l.text == "" {
				if cur != "" {
					return false
				}
				j++
				continue
			}
			if !strings.HasPrefix(cur, l.text) {
				return false
			}
			k = len(l.text)
		} else {
			prev, rest := sub[i-1], cur[k:]
			if strings.HasPrefix(rest, " ") {
				next := strings.TrimLeft(rest, " ")
				word, _, _ := strings.Cut(l.text, " ")
				if prev.maxEnd == 0 || prev.end+1+utf8.RuneCountInString(word) <= prev.maxEnd {
					return false // the word fitted: the break is not a wrap
				}
				k += len(rest) - len(next)
				rest = next
			} else if prev.maxEnd == 0 || prev.end < prev.maxEnd {
				return false // a break inside a word, on a line not full
			}
			if l.text == "" || !strings.HasPrefix(rest, l.text) {
				return false
			}
			k += len(l.text)
		}
		if k == len(cur) {
			j, k = j+1, 0
		}
	}
	return j == len(logical) && k == 0
}

// wordsName reports whether a word of the lines is target and no other
// word of its kind names something else: a dialog that also names another
// file (or URL) is ambiguous, and denied.
func wordsName(lines []string, target, cwd string, kind func(string) bool) bool {
	named := false
	for _, l := range lines {
		for _, w := range strings.FieldsFunc(l, func(r rune) bool {
			return r == ' ' || r == '\t' || strings.ContainsRune("()[]{}<>\"'`,;", r)
		}) {
			w = strings.TrimRight(w, "?:!.")
			if w == "" || !kind(w) {
				continue
			}
			switch {
			case sameTarget(w, target, cwd):
				named = true
			case !partOf(w, target, cwd):
				return false
			}
		}
	}
	return named
}

var fileName = regexp.MustCompile(`^[^/\\]+\.[A-Za-z0-9_-]{1,12}$`)

// isPathWord: a word with a separator or with the extension of a file.
func isPathWord(w string) bool {
	return strings.ContainsAny(w, `/\`) && strings.Trim(w, `/\`) != "" || fileName.MatchString(w)
}

func isURLWord(w string) bool {
	return strings.HasPrefix(w, "http://") || strings.HasPrefix(w, "https://")
}

// slashPath is p with forward slashes and cleaned.
func slashPath(p string) string { return path.Clean(strings.ReplaceAll(p, `\`, "/")) }

// absPath reports whether p is absolute on either system.
func absPath(p string) bool {
	return strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || len(p) > 2 && p[1] == ':' && (p[2] == '/' || p[2] == '\\')
}

// resolve is the word as a path: itself when absolute, else relative to
// cwd; "" when it is relative and there is no cwd.
func resolve(w, cwd string) string {
	switch {
	case absPath(w):
		return slashPath(w)
	case cwd == "":
		return ""
	}
	return path.Join(slashPath(cwd), slashPath(w))
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	// A Windows path is compared without case, as the system does.
	return len(a) > 1 && a[1] == ':' && strings.EqualFold(a, b)
}

// sameTarget reports whether the word names target.
func sameTarget(w, target, cwd string) bool {
	if isURLWord(target) {
		return w == target || strings.TrimSuffix(w, "/") == strings.TrimSuffix(target, "/")
	}
	r := resolve(w, cwd)
	return r != "" && samePath(r, slashPath(target))
}

// partOf reports whether the word is a piece of target that names nothing
// else: its file name (the question of Claude Code says only that) or a
// folder above it.
func partOf(w, target, cwd string) bool {
	if isURLWord(target) {
		return false
	}
	t := slashPath(target)
	if w == path.Base(t) {
		return true
	}
	r := resolve(w, cwd)
	return r != "" && strings.HasPrefix(t, r+"/")
}
