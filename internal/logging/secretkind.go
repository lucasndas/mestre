package logging

import (
	"regexp"
	"strings"
)

// KnownSecret is the kind of a line that holds a value the vault opened.
const KnownSecret = "known_secret"

var (
	// the formats of the mask, but the key and value one, which the generic
	// rule below does with its own exceptions
	kindPatterns = func() []Pattern {
		var out []Pattern
		for _, p := range Patterns() {
			if p.Name != PatternKeyValue {
				out = append(out, p)
			}
		}
		return out
	}()
	awsKey = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)
	// token, secret or password set to a literal of 24 characters or more
	assigned = regexp.MustCompile(`(?i)(?:token|secret|password)[A-Za-z0-9_]*\s*[:=]\s*["']([^"'\r\n]{24,})["']`)
	// what is not a secret: the reading of a variable of the environment (the
	// value of the assignment is the read itself, not a literal that happens
	// to be on a line that reads one)
	envRead = regexp.MustCompile(`process\.env|os\.environ|os\.Getenv|\$\{[A-Za-z_][A-Za-z0-9_]*\}`)
)

// SecretKind says what kind of secret a line of text carries, by its format
// ("github_token", "private_key", "secret_assignment"…), or "" when it has
// none. It is the scan of what is about to be written or sent: where the mask
// hides every word that looks random, this one names only the known formats
// and a literal assigned to a token, secret or password, and lets a read of the
// environment and an example through. The value is never returned.
func SecretKind(text string) string {
	for _, p := range kindPatterns {
		for _, m := range p.Re.FindAllStringSubmatch(text, -1) {
			value := m[0]
			if p.Group > 0 && p.Group < len(m) {
				value = m[p.Group]
			}
			// a value that reads the environment, or says it is an example, is no secret
			if isPlaceholder(value) || p.Name == PatternAuthorization && envRead.MatchString(value) {
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

// SecretKind is the package function plus the values this masker knows: a line
// with a secret the vault opened is KnownSecret even where the secret has no
// format.
func (m *Masker) SecretKind(text string) string {
	m.mu.RLock()
	r := m.replacer
	m.mu.RUnlock()
	if r != nil && r.Replace(text) != text {
		return KnownSecret
	}
	return SecretKind(text)
}
