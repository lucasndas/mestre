package classes

import (
	"path"
	"strings"
	"sync"
)

// Path globs (files, git.branches): slash-separated; ** matches zero or more
// segments, and *, ? and [...] match inside one segment as path.Match does.
// A dot file is matched like any other name: "**" covers ".env".

// matchGlob reports whether the slash path p matches pattern. Both are
// relative (to the worktree root) or both absolute; the root itself is "".
//
// The analysis asks it thousands of times for the same few patterns (a long
// command that writes many files), so the segments of a pattern are split
// once, and those of the path go in a buffer on the stack.
func matchGlob(pattern, p string) bool {
	var buf [16]string
	return matchSegs(patternSegs(pattern), appendSegs(buf[:0], p))
}

// patternSegs is splitSegs of a pattern, kept: the patterns come from the
// rules and the code, a short fixed set.
func patternSegs(pattern string) []string {
	if v, ok := segCache.Load(pattern); ok {
		return v.([]string)
	}
	segs := splitSegs(pattern)
	segCache.Store(pattern, segs)
	return segs
}

var segCache sync.Map // string -> []string

func splitSegs(p string) []string {
	return appendSegs(nil, p)
}

// appendSegs appends the segments of the slash path p to dst.
func appendSegs(dst []string, p string) []string {
	p = strings.Trim(p, "/")
	if p == "" || p == "." {
		return dst
	}
	for {
		i := strings.IndexByte(p, '/')
		if i < 0 {
			return append(dst, p)
		}
		dst = append(dst, p[:i])
		p = p[i+1:]
	}
}

func matchSegs(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			for i := 0; i <= len(segs); i++ {
				if matchSegs(rest, segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], segs[0])
		if err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

// validGlob reports whether every segment of a path glob is well formed.
func validGlob(pattern string) bool {
	for _, s := range splitSegs(pattern) {
		if s == ".." {
			return false
		}
		if s == "**" {
			continue
		}
		if _, err := path.Match(s, ""); err != nil {
			return false
		}
	}
	return true
}

// coversGlob is the conservative check of the combination of layers: true
// only when every path c matches is surely matched by t. It answers false
// when it cannot tell, and the combination then drops the pattern.
func coversGlob(t, c string) bool {
	switch {
	case t == c, t == "**":
		return true
	case strings.HasSuffix(t, "/**"):
		base := strings.TrimSuffix(t, "/**")
		if strings.ContainsAny(base, "*?[") {
			return false
		}
		return c == base || strings.HasPrefix(c, base+"/")
	}
	return false
}

// Text patterns (commands, tools, cli, domains): * matches any text, spaces
// included; every other character is itself. A pattern that ends in " *"
// also matches the text without that last word ("curl *" matches "curl").

// matchText reports whether s matches the text pattern.
func matchText(pattern, s string) bool {
	if wild(pattern, s) {
		return true
	}
	if base, ok := strings.CutSuffix(pattern, " *"); ok {
		return wild(base, s)
	}
	return false
}

// wild matches * as any text; it runs in linear time (greedy with
// backtracking to the last star).
func wild(p, s string) bool {
	pi, si, star, mark := 0, 0, -1, 0
	for si < len(s) {
		switch {
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, si
			pi++
		case pi < len(p) && p[pi] == s[si]:
			pi++
			si++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

// coversText is coversGlob for text patterns.
func coversText(t, c string) bool {
	switch {
	case t == c, t == "*":
		return true
	case strings.HasSuffix(t, "*") && !strings.Contains(t[:len(t)-1], "*"):
		prefix := t[:len(t)-1]
		if strings.HasPrefix(c, prefix) {
			return true
		}
		// "curl *" also covers "curl" (matchText).
		return strings.HasSuffix(prefix, " ") && c == strings.TrimSuffix(prefix, " ")
	}
	return false
}

// matchDomain reports whether host matches a domain pattern: the exact
// name, or a glob where * matches any run of characters, dots included
// ("*.example.com" matches "a.b.example.com", not "example.com").
func matchDomain(pattern, host string) bool {
	pattern, host = strings.ToLower(pattern), strings.ToLower(strings.TrimSuffix(host, "."))
	if pattern == host {
		return true
	}
	return strings.Contains(pattern, "*") && wild(pattern, host)
}

// coversDomain is coversText for domains.
func coversDomain(t, c string) bool {
	t, c = strings.ToLower(t), strings.ToLower(c)
	if t == c || t == "*" {
		return true
	}
	if s, ok := strings.CutPrefix(t, "*"); ok && !strings.Contains(s, "*") {
		return strings.HasSuffix(c, s)
	}
	return false
}

// ValidGlob says whether the pattern is a glob the rules read (the policy file
// checks the globs of the sensitive areas with it).
func ValidGlob(pattern string) bool { return pattern != "" && validGlob(pattern) }
