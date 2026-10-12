package main

import (
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// doc is one parsed markdown file.
type doc struct {
	path     string // relative to the repository root, with '/'
	lines    []string
	inCode   []bool // line is inside a fenced code block (or is a fence)
	headings []heading
	links    []link
	anchors  map[string]bool
}

type heading struct {
	level int
	text  string
	line  int // 1-based
}

type link struct {
	target string
	line   int
}

var (
	headingRe    = regexp.MustCompile(`^(#{1,6})[ \t]+(.*?)[ \t]*#*[ \t]*$`)
	inlineCodeRe = regexp.MustCompile("`[^`\n]*`")
	linkRe       = regexp.MustCompile(`\]\(([^)\s]+)(?:[ \t]+"[^"]*")?\)`)
	mdLinkTextRe = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	schemeRe     = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
)

func parseDoc(p string, content []byte) *doc {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	d := &doc{path: p, lines: lines, inCode: make([]bool, len(lines))}
	fence := ""
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if fence != "" {
			d.inCode[i] = true
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:3]
			d.inCode[i] = true
			continue
		}
		if m := headingRe.FindStringSubmatch(l); m != nil {
			d.headings = append(d.headings, heading{level: len(m[1]), text: m[2], line: i + 1})
		}
		for _, m := range linkRe.FindAllStringSubmatch(inlineCodeRe.ReplaceAllString(l, ""), -1) {
			d.links = append(d.links, link{target: m[1], line: i + 1})
		}
	}
	return d
}

// lineCount is the number of lines of the file.
func (d *doc) lineCount() int { return len(d.lines) }

// h2 returns the level-2 headings.
func (d *doc) h2() []heading {
	var out []heading
	for _, h := range d.headings {
		if h.level == 2 {
			out = append(out, h)
		}
	}
	return out
}

func (d *doc) hasSection(name string) bool {
	for _, h := range d.h2() {
		if h.text == name {
			return true
		}
	}
	return false
}

// section returns the 1-based line range (start, end] of the body of the
// level-2 section name, or ok=false.
func (d *doc) section(name string) (start, end int, ok bool) {
	for _, h := range d.headings {
		if ok && h.level <= 2 {
			return start, h.line - 1, true
		}
		if h.level == 2 && h.text == name {
			start, ok = h.line, true
		}
	}
	return start, len(d.lines), ok
}

// linksIn returns the links whose line is in (start, end].
func (d *doc) linksIn(start, end int) []link {
	var out []link
	for _, l := range d.links {
		if l.line > start && l.line <= end {
			out = append(out, l)
		}
	}
	return out
}

// anchorSet computes the GitHub anchors of the headings.
func (d *doc) anchorSet() map[string]bool {
	if d.anchors != nil {
		return d.anchors
	}
	d.anchors = map[string]bool{}
	count := map[string]int{}
	for _, h := range d.headings {
		s := slug(h.text)
		n := count[s]
		count[s] = n + 1
		if n == 0 {
			d.anchors[s] = true
		} else {
			d.anchors[s+"-"+strconv.Itoa(n)] = true
		}
	}
	return d.anchors
}

// slug follows GitHub: lower case, accented letters kept, punctuation
// removed except '-' and '_', spaces become '-'.
func slug(heading string) string {
	t := mdLinkTextRe.ReplaceAllString(heading, "$1")
	t = strings.ReplaceAll(t, "`", "")
	var b strings.Builder
	for _, r := range strings.ToLower(t) {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r), r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// resolved is a link target inside the repository.
type resolved struct {
	file   string // path relative to the root; "" when the link is external
	anchor string
	ok     bool // false when the link leaves the repository
}

// resolve turns a link of doc d into a repository path.
func resolve(d *doc, target string) resolved {
	if schemeRe.MatchString(target) || strings.HasPrefix(target, "//") {
		return resolved{ok: true}
	}
	p, anchor, _ := strings.Cut(target, "#")
	if u, err := url.PathUnescape(p); err == nil {
		p = u
	}
	if a, err := url.PathUnescape(anchor); err == nil {
		anchor = a
	}
	if p == "" {
		return resolved{file: d.path, anchor: anchor, ok: true}
	}
	full := path.Clean(path.Join(path.Dir(d.path), p))
	if full == ".." || strings.HasPrefix(full, "../") || path.IsAbs(p) {
		return resolved{file: full}
	}
	return resolved{file: full, anchor: anchor, ok: true}
}

func exists(fsys fs.FS, p string) bool {
	_, err := fs.Stat(fsys, p)
	return err == nil
}
