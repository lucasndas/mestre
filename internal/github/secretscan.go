package github

import (
	"regexp"
	"strings"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/logging"
)

// SecretFinding is a line of the diff that carries a secret of a known format:
// where it is and what kind, never the value.
type SecretFinding struct {
	Path string
	Line int
	Kind string
}

func (f SecretFinding) String() string { return f.Path + ":" + itoa(f.Line) + " " + f.Kind }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for ; n > 0; n /= 10 {
		i--
		b[i] = byte('0' + n%10)
	}
	return string(b[i:])
}

var (
	// the formats of the mask of the logs, but the key and value one, which the
	// generic rule below does with its own exceptions
	scanPatterns = func() []logging.Pattern {
		var out []logging.Pattern
		for _, p := range logging.Patterns() {
			if p.Name != logging.PatternKeyValue {
				out = append(out, p)
			}
		}
		return out
	}()
	awsKey = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)
	// token, secret or password set to a literal of 24 characters or more
	assigned = regexp.MustCompile(`(?i)(?:token|secret|password)[A-Za-z0-9_]*\s*[:=]\s*["']([^"'\r\n]{24,})["']`)
	// what is not a secret in a scan: the reading of a variable of the
	// environment (the value of the assignment is the read itself, not a literal
	// that happens to be on a line that reads one)
	envRead = regexp.MustCompile(`process\.env|os\.environ|os\.Getenv|\$\{[A-Za-z_][A-Za-z0-9_]*\}`)
)

// ScanSecrets looks, in the lines a change adds, for a secret of a known format
// (docs/cli/coordenacao-github.md, "Chave no diff"). The value is never kept.
func ScanSecrets(lines []gitx.AddedLine) []SecretFinding {
	var out []SecretFinding
	for _, l := range lines {
		if kind := secretKind(l.Text); kind != "" {
			out = append(out, SecretFinding{Path: l.Path, Line: l.Line, Kind: kind})
		}
	}
	return out
}

func secretKind(text string) string {
	for _, p := range scanPatterns {
		for _, m := range p.Re.FindAllStringSubmatch(text, -1) {
			value := m[0]
			if p.Group > 0 && p.Group < len(m) {
				value = m[p.Group]
			}
			// a value that reads the environment, or says it is an example, is no secret
			if isPlaceholder(value) || p.Name == logging.PatternAuthorization && envRead.MatchString(value) {
				continue
			}
			return strings.ReplaceAll(p.Name, "-", "_")
		}
	}
	if m := awsKey.FindString(text); m != "" && !isPlaceholder(m) {
		return "aws_access_key"
	}
	if m := assigned.FindStringSubmatch(text); m != nil && !envRead.MatchString(m[1]) && !isPlaceholder(m[1]) {
		return "secret_assignment"
	}
	return ""
}

func isPlaceholder(v string) bool {
	low := strings.ToLower(v)
	return strings.Contains(low, "example") || strings.Contains(low, "your_")
}
