package testkit

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/lucasndas/mestre/internal/logging"
)

// Finding is one secret-shaped string found in a recording.
type Finding struct {
	File    string
	Line    int
	Pattern string
}

func (f Finding) String() string { return fmt.Sprintf("%s:%d: %s", f.File, f.Line, f.Pattern) }

// recordingPatterns are the formats the recordings scan looks for: GitHub
// tokens, Telegram bot tokens and PEM private keys (dubles.md).
func recordingPatterns() []logging.Pattern {
	want := map[string]bool{
		logging.PatternGitHubToken:   true,
		logging.PatternTelegramToken: true,
	}
	var ps []logging.Pattern
	for _, p := range logging.Patterns() {
		if want[p.Name] {
			ps = append(ps, p)
		}
	}
	// A PEM header alone is enough to refuse the file.
	ps = append(ps, logging.Pattern{Name: logging.PatternPrivateKey, Re: regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)})
	return ps
}

// ScanSecrets walks fsys and reports every file and line holding a GitHub
// token, a Telegram bot token or a PEM private key.
func ScanSecrets(fsys fs.FS) ([]Finding, error) {
	ps := recordingPatterns()
	var out []Finding
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, pat := range ps {
				if pat.Re.MatchString(line) {
					out = append(out, Finding{File: p, Line: i + 1, Pattern: pat.Name})
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan recordings: %w", err)
	}
	return out, nil
}
