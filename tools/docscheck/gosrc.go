package main

import (
	"bytes"
	"go/scanner"
	"go/token"
	"io/fs"
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
}

var (
	markerRe = regexp.MustCompile(`^(CA-[A-Z0-9]+(?:-[A-Z0-9]+)*-[0-9]{2})\b`)
	// staleGateRe keeps only the comments the stale-comment check may match.
	staleGateRe = regexp.MustCompile(`\bM[0-9]\b|(?i:spike)\s+[0-9]`)
)

// mayHitStaleGate is a cheap test that is false for most text: it can only be
// true where staleGateRe could match (an M and a number, or the word spike in
// any case). The scan asks it of a whole file before reading the comments,
// and of a comment before the regexp, which is what costs.
func mayHitStaleGate[T ~string | ~[]byte](s T) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case 'M':
			if i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
				return true
			}
		case 's', 'S':
			if i+5 <= len(s) && s[i+1]|0x20 == 'p' && s[i+2]|0x20 == 'i' && s[i+3]|0x20 == 'k' && s[i+4]|0x20 == 'e' {
				return true
			}
		}
	}
	return false
}

func (g *commentGroup) matchesStaleGate() bool {
	return mayHitStaleGate(g.Text) && staleGateRe.MatchString(g.Text)
}

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
	for i := range parts { // in path order: the same result on every run
		out.markers = append(out.markers, parts[i].markers...)
		out.comments = append(out.comments, parts[i].comments...)
	}
	return out
}

func (s *goScan) scanFile(fset *token.FileSet, p string, src []byte) {
	if !bytes.Contains(src, []byte("CA-")) && !mayHitStaleGate(src) {
		return // no comment of this file can be a marker or hit the stale gate
	}
	file := fset.AddFile(p, -1, len(src))
	var sc scanner.Scanner
	sc.Init(file, src, func(token.Position, string) {}, scanner.ScanComments)
	isTest := strings.HasSuffix(p, "_test.go")
	track := !inSpikes(p)

	var cur *commentGroup
	prev := -10
	flush := func() {
		if cur != nil && cur.matchesStaleGate() {
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
		if strings.HasPrefix(lit, "//") {
			text := strings.TrimPrefix(lit, "//")
			if t := strings.TrimSpace(text); strings.HasPrefix(t, "CA-") {
				if m := markerRe.FindStringSubmatch(t); m != nil {
					s.markers = append(s.markers, codeMarker{ID: m[1], File: p, Line: line, Test: isTest})
				}
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
			if g.matchesStaleGate() {
				s.comments = append(s.comments, *g)
			}
		}
		prev = -10
	}
	flush()
}
