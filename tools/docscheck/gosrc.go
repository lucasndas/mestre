package main

import (
	"go/scanner"
	"go/token"
	"io/fs"
	"path"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

// codeMarker is a `// CA-<SIGLA>-<NN>` comment: the test that covers the
// criterion (verificacao-de-docs.md, check 12).
type codeMarker struct {
	ID   string
	File string
	Line int
	Test bool // the file is a _test.go
	// Suite is the suite the file belongs to (rápida, completa or pesada).
	Suite string
	// Placed says whether the comment sits right above a test: the line after
	// the comment it opens is `func Test…` or a `t.Run(`.
	Placed bool
}

// commentGroup is a run of consecutive line comments (or one block comment)
// that mentions a milestone or a spike; its lines are joined by a space so a
// sentence wrapped over two lines still matches.
type commentGroup struct {
	File  string
	Text  string
	lines []int // line number of each line of the group
	cuts  []int // offset in Text where each line starts
}

// lineAt is the source line of the byte offset off of the text.
func (g commentGroup) lineAt(off int) int {
	i := 0
	for i+1 < len(g.cuts) && g.cuts[i+1] <= off {
		i++
	}
	return g.lines[i]
}

// goScan is everything the checks over Go code read, in one pass.
type goScan struct {
	markers  []codeMarker
	comments []commentGroup
	refs     []docRef // the path of a document and #anchor, written in comments
}

// docRef is a reference to a heading of a document, written in a comment or a
// configuration file: the path of the document under docs/, then #heading.
type docRef struct {
	File   string
	Line   int
	Doc    string
	Anchor string
}

// docRefRe finds a reference with the path from the root of the repository.
var docRefRe = regexp.MustCompile(`\b(docs/[A-Za-z0-9_./-]+\.md)#([^\s)\]>,;:'"` + "`" + `]+)`)

// findRefs adds the references of the text, which starts at line.
func (s *goScan) findRefs(file, text string, line int) {
	for _, loc := range docRefRe.FindAllStringSubmatchIndex(text, -1) {
		s.refs = append(s.refs, docRef{file, line + strings.Count(text[:loc[0]], "\n"), text[loc[2]:loc[3]], strings.TrimRight(text[loc[4]:loc[5]], ".")})
	}
}

var (
	markerRe = regexp.MustCompile(`^(CA-[A-Z0-9]+(?:-[A-Z0-9]+)*-[0-9]{2})\b`)
	// staleGateRe keeps only the comments the stale-comment check may match.
	staleGateRe = regexp.MustCompile(`\bM[0-9]\b|(?i:spike)\s+[0-9]`)
)

// skipGoDir says whether the walk leaves the folder: like the go tool, the
// ones starting with "." or "_" (VCS data, agent checkouts, ...), plus
// node_modules, testdata and the panel.
func skipGoDir(p, name string) bool {
	if p == "." {
		return false
	}
	switch name {
	case "node_modules", "testdata", "web":
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// inSpikes: spike code is kept as reference and has no milestone to keep up.
func inSpikes(p string) bool {
	return strings.HasPrefix(p, "tools/spikes/") || strings.HasPrefix(p, "tools/winsandbox/spikes/")
}

// scanGo reads the comments of every Go file of the module, the files in
// parallel (the scan is what costs most in the check).
func scanGo(fsys fs.FS) *goScan {
	var paths []string
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		if d.IsDir() {
			if skipGoDir(p, d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") {
			paths = append(paths, p)
		}
		return nil
	})
	fset := token.NewFileSet()
	parts := make([]goScan, len(paths))
	var next atomic.Int64
	var wg sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), len(paths)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(paths) {
					return
				}
				if src, err := fs.ReadFile(fsys, paths[i]); err == nil {
					parts[i].scanFile(fset, paths[i], src)
				}
			}
		}()
	}
	wg.Wait()
	out := &goScan{}
	out.markers = append(out.markers, scanWeb(fsys)...)
	for i := range parts { // in path order: the same result on every run
		out.markers = append(out.markers, parts[i].markers...)
		out.comments = append(out.comments, parts[i].comments...)
		out.refs = append(out.refs, parts[i].refs...)
	}
	return out
}

func (s *goScan) scanFile(fset *token.FileSet, p string, src []byte) {
	file := fset.AddFile(p, -1, len(src))
	var sc scanner.Scanner
	sc.Init(file, src, func(token.Position, string) {}, scanner.ScanComments)
	isTest := strings.HasSuffix(p, "_test.go")
	track := !inSpikes(p)
	suite := fileSuite(p, src)
	lines := strings.Split(string(src), "\n")

	var cur *commentGroup
	prev := -10
	flush := func() {
		if cur != nil && staleGateRe.MatchString(cur.Text) {
			s.comments = append(s.comments, *cur)
		}
		cur = nil
	}
	for {
		pos, tok, lit := sc.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.COMMENT {
			continue
		}
		line := file.Line(pos)
		s.findRefs(p, lit, line)
		if strings.HasPrefix(lit, "//") {
			text := strings.TrimPrefix(lit, "//")
			if m := markerRe.FindStringSubmatch(strings.TrimSpace(text)); m != nil && track {
				s.markers = append(s.markers, codeMarker{ID: m[1], File: p, Line: line, Test: isTest, Suite: suite, Placed: aboveATest(lines, line)})
			}
			if !track {
				continue
			}
			if cur == nil || line != prev+1 {
				flush()
				cur = &commentGroup{File: p}
			}
			cur.cuts = append(cur.cuts, len(cur.Text))
			cur.lines = append(cur.lines, line)
			cur.Text += strings.TrimSpace(text) + " "
			prev = line
			continue
		}
		flush()
		if track {
			// A block comment: its lines are joined like the line comments.
			g := &commentGroup{File: p}
			for i, l := range strings.Split(lit, "\n") {
				g.cuts = append(g.cuts, len(g.Text))
				g.lines = append(g.lines, line+i)
				g.Text += strings.TrimSpace(l) + " "
			}
			if staleGateRe.MatchString(g.Text) {
				s.comments = append(s.comments, *g)
			}
		}
		prev = -10
	}
	flush()
}

// testLineRe is the line a marker may sit above: a test the suites run, or a
// subtest.
var testLineRe = regexp.MustCompile(`^(func Test[A-Z0-9_]\w*\(\w+ \*testing\.T\)|(\w+, \w+ :?= )?t\.Run\()`)

var buildLineRe = regexp.MustCompile(`^//go:build\s+(.*)$`)

// fileSuite is the suite a Go file runs in: `full` in its build line (not
// negated) or a _full_test.go name is the completa; `heavy` or `realaccount`
// the pesada; the rest the rápida.
func fileSuite(p string, src []byte) string {
	expr := ""
	for _, l := range strings.Split(string(src), "\n") {
		l = strings.TrimSpace(l)
		if m := buildLineRe.FindStringSubmatch(l); m != nil {
			expr = m[1]
			break
		}
		if strings.HasPrefix(l, "package ") {
			break
		}
	}
	has := func(tag string) bool {
		for _, f := range strings.FieldsFunc(expr, func(r rune) bool { return r != '_' && r != '!' && (r < 'a' || r > 'z') && (r < '0' || r > '9') }) {
			if f == tag {
				return true
			}
		}
		return false
	}
	switch {
	case has("ignore") || has("spike") || has("realcli") || has("ptylegacy"):
		return "nenhuma" // no suite builds it
	case has("heavy") || has("realaccount") || strings.HasSuffix(p, "_heavy_test.go") || strings.HasSuffix(p, "_realaccount_test.go"):
		return "pesada"
	case has("full") || strings.HasSuffix(p, "_full_test.go"):
		return "completa"
	}
	return "rápida"
}

// aboveATest says whether the line comment of number line opens a run of
// comment lines that ends right above `func Test…` or a `t.Run(`.
func aboveATest(lines []string, line int) bool {
	i := line // 0-based index of the next line
	for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "//") {
		i++
	}
	if i >= len(lines) {
		return false
	}
	return testLineRe.MatchString(strings.TrimSpace(lines[i]))
}

// scanConfigRefs reads the references in the files that are not Go (the
// workflows and the SQL migrations): the lines that carry a comment.
func scanConfigRefs(fsys fs.FS) []docRef {
	var out []docRef
	scan := &goScan{}
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		if d.IsDir() {
			if p == ".git" || d.Name() == "node_modules" || p == ".claude" || p == "docs" || d.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if ext := path.Ext(p); ext == ".yml" || ext == ".yaml" || ext == ".sql" {
			if b, err := fs.ReadFile(fsys, p); err == nil {
				scan.findRefs(p, string(b), 1)
			}
		}
		return nil
	})
	return append(out, scan.refs...)
}

// webTestRe is the file a marker may sit in: a Vitest test or an e2e spec of
// the panel.
var webTestRe = regexp.MustCompile(`\.(test|spec)\.tsx?$`)

// webTestLineRe is the line a marker may sit above in TypeScript: a test,
// a group of tests or a subtest.
var webTestLineRe = regexp.MustCompile(`^(test|it|describe)(\.\w+)*\(`)

// scanWeb reads the `// CA-…` markers of the tests of the panel (web/**/*.test.ts(x)
// and the e2e specs), without node_modules and the generated types. The
// e2e specs run a real browser: the suite is the pesada.
func scanWeb(fsys fs.FS) []codeMarker {
	var out []codeMarker
	_ = fs.WalkDir(fsys, "web", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // no web/ folder, or an unreadable entry
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || d.Name() == "gen" || d.Name() == "dist" {
				return fs.SkipDir
			}
			return nil
		}
		if !webTestRe.MatchString(p) {
			return nil
		}
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil //nolint:nilerr // unreadable files are skipped
		}
		suite := "rápida"
		if strings.HasPrefix(p, "web/e2e/") {
			suite = "pesada"
		}
		lines := strings.Split(string(src), "\n")
		for i, l := range lines {
			l = strings.TrimSpace(l)
			if !strings.HasPrefix(l, "//") {
				continue
			}
			if m := markerRe.FindStringSubmatch(strings.TrimSpace(strings.TrimPrefix(l, "//"))); m != nil {
				out = append(out, codeMarker{ID: m[1], File: p, Line: i + 1, Test: true, Suite: suite, Placed: aboveATSTest(lines, i+1)})
			}
		}
		return nil
	})
	return out
}

// aboveATSTest is aboveATest for TypeScript: the comment lines that follow
// end right above a test, `describe(` or `it(`.
func aboveATSTest(lines []string, line int) bool {
	i := line
	for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "//") {
		i++
	}
	return i < len(lines) && webTestLineRe.MatchString(strings.TrimSpace(lines[i]))
}
