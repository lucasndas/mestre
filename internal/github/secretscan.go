package github

import (
	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/logging"
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

// ScanSecrets looks, in the lines a change adds, for a secret of a known format
// (docs/cli/coordenacao-github.md, "Chave no diff"). The value is never kept.
// The formats and the exceptions are the ones of logging.SecretKind, which the
// hook uses on what an agent is about to write.
func ScanSecrets(lines []gitx.AddedLine) []SecretFinding {
	var out []SecretFinding
	for _, l := range lines {
		if kind := logging.SecretKind(l.Text); kind != "" {
			out = append(out, SecretFinding{Path: l.Path, Line: l.Line, Kind: kind})
		}
	}
	return out
}
