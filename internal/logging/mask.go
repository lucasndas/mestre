// Package logging is the only way the Mestre writes logs: slog in JSON, one
// record per line, with every string masked before it is written
// (docs/arquitetura/seguranca.md#mascaramento-em-logs).
package logging

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// Redacted replaces every masked secret.
const Redacted = "[segredo]"

// Pattern is one known secret format.
type Pattern struct {
	Name string
	Re   *regexp.Regexp
	// Group, when > 0, is the submatch that holds the secret; the rest of
	// the match (a key or header name) is kept.
	Group int
}

// Pattern names, used by scanners that need only some formats.
const (
	PatternGitHubToken   = "github-token"
	PatternTelegramToken = "telegram-bot-token"
	PatternSlackToken    = "slack-token"
	PatternJWT           = "jwt"
	PatternPrivateKey    = "private-key"
	PatternAuthorization = "authorization-header"
	PatternSKKey         = "sk-key"
	PatternKeyValue      = "key-value"
)

// Patterns returns the known secret formats of seguranca.md, in the order
// they are applied.
func Patterns() []Pattern {
	return []Pattern{
		{Name: PatternPrivateKey, Re: regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----(?s:.*?)(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)`)},
		{Name: PatternAuthorization, Re: regexp.MustCompile(`(?i)(authorization:[ \t]*)([^\r\n]+)`), Group: 2},
		{Name: PatternGitHubToken, Re: regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`)},
		{Name: PatternTelegramToken, Re: regexp.MustCompile(`(\b[0-9]{5,}:[A-Za-z0-9_-]{35})(?:[^A-Za-z0-9_-]|$)`), Group: 1},
		{Name: PatternSlackToken, Re: regexp.MustCompile(`\bxox[bp]-[A-Za-z0-9-]{10,}`)},
		{Name: PatternJWT, Re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)},
		{Name: PatternSKKey, Re: regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`)},
		{Name: PatternKeyValue, Re: regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])([A-Za-z0-9_]*(?:password|token|secret)=)("[^"\r\n]*"?|'[^'\r\n]*'?|[^\s&"',;]+)`), Group: 2},
	}
}

// Default threshold for high-entropy words (seguranca.md).
const (
	DefaultEntropyMinLen = 20
	minEntropyBits       = 3.5
)

// wordRe delimits the candidates of the entropy rule. "/" and "=" are not
// part of a word, so paths, branch names and KEY=value pairs are judged by
// their pieces, not as one long string.
var wordRe = regexp.MustCompile(`[A-Za-z0-9+_-]+`)

// Masker removes secrets from text. The zero value is not usable; use
// NewMasker. It is safe for concurrent use.
type Masker struct {
	patterns []Pattern
	minLen   int

	mu       sync.RWMutex
	known    map[string]struct{}
	replacer *strings.Replacer
}

// NewMasker returns a Masker with the default formats and entropy length.
func NewMasker() *Masker {
	return &Masker{patterns: Patterns(), minLen: DefaultEntropyMinLen, known: map[string]struct{}{}}
}

// Register adds a known secret value (rule 1: every vault secret is
// registered when it is read). Values shorter than 4 bytes are ignored to
// avoid masking ordinary words.
func (m *Masker) Register(secret string) {
	if len(secret) < 4 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.known[secret]; ok {
		return
	}
	m.known[secret] = struct{}{}
	vals := make([]string, 0, len(m.known))
	for v := range m.known {
		vals = append(vals, v)
	}
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	pairs := make([]string, 0, 2*len(vals))
	for _, v := range vals {
		pairs = append(pairs, v, Redacted)
	}
	m.replacer = strings.NewReplacer(pairs...)
}

// Mask returns s with known values, known formats and high-entropy words
// replaced by Redacted.
func (m *Masker) Mask(s string) string {
	if s == "" {
		return s
	}
	m.mu.RLock()
	r := m.replacer
	m.mu.RUnlock()
	if r != nil {
		s = r.Replace(s)
	}
	for _, p := range m.patterns {
		s = maskPattern(p, s)
	}
	return wordRe.ReplaceAllStringFunc(s, func(w string) string {
		if highEntropy(w, m.minLen) && !identifierLike(w) {
			return Redacted
		}
		return w
	})
}

// MaskTerminalLine masks one line of terminal history after removing escape
// sequences (rule 3).
func (m *Masker) MaskTerminalLine(line string) string { return m.Mask(StripANSI(line)) }

func maskPattern(p Pattern, s string) string {
	if p.Group == 0 {
		return p.Re.ReplaceAllString(s, Redacted)
	}
	return p.Re.ReplaceAllStringFunc(s, func(match string) string {
		sub := p.Re.FindStringSubmatchIndex(match)
		if sub == nil || sub[2*p.Group] < 0 {
			return Redacted
		}
		return match[:sub[2*p.Group]] + Redacted + match[sub[2*p.Group+1]:]
	})
}

// highEntropy reports whether w looks like a random token: at least minLen
// characters, with upper case, lower case and two or more digits, and Shannon
// entropy of at least 3.5 bits per character. The class requirement keeps
// UUIDs, hex hashes and ordinary identifiers readable in the logs.
func highEntropy(w string, minLen int) bool {
	if len(w) < minLen || w == Redacted {
		return false
	}
	var upper, lower, digits int
	freq := map[rune]int{}
	for _, r := range w {
		freq[r]++
		switch {
		case unicode.IsUpper(r):
			upper++
		case unicode.IsLower(r):
			lower++
		case unicode.IsDigit(r):
			digits++
		}
	}
	if upper == 0 || lower == 0 || digits < 2 {
		return false
	}
	n := float64(len(w))
	var bits float64
	for _, c := range freq {
		p := float64(c) / n
		bits -= p * math.Log2(p)
	}
	return bits >= minEntropyBits
}

var ansiRe = regexp.MustCompile(`\x1b(?:\[[0-9;?<>=!]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[PX^_][^\x1b]*\x1b\\|[()*+][ -~]|[0-9=>]|[@-Z\\-_]|)`)

// StripANSI removes terminal escape sequences from s: CSI, OSC, DCS, charset
// designations (ESC ( B, as in the output of `tput sgr0`), the one-character
// forms (ESC 7, ESC 8, ESC =, ESC >) and any stray ESC.
func StripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

var testNameRe = regexp.MustCompile(`^(?:Test|Benchmark|Fuzz|Example)[A-Z0-9][A-Za-z0-9]*(?:_[A-Za-z0-9]+)*$`)

var kebabWordRe = regexp.MustCompile(`^[a-z]{4,}$`)

// identifierLike reports whether w has the shape of a code identifier that a
// random token practically never has: a Go test name (TestMVP13_FourCompositions)
// or a kebab-case name with two or more lower-case words of four letters or
// more (table-lifecycle-v2Beta10, the tail of a branch name).
func identifierLike(w string) bool {
	if testNameRe.MatchString(w) {
		return true
	}
	words := 0
	for _, part := range strings.Split(w, "-") {
		if kebabWordRe.MatchString(part) {
			words++
		}
	}
	return words >= 2
}
