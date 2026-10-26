// Package secretscreen is the filter of secrets that every channel a user writes
// through applies before a text reaches the Grão-Mestre
// (docs/telegram/seguranca-do-canal.md#segredos): the Telegram and the panel read the
// same text the same way. A phrase that gives a password holds the whole message; a token
// of a known format is replaced in place and the rest goes on. Both look at the
// normalized text, so a zero-width space or a full-width letter does not hide a secret.
package secretscreen

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/mestre-ia/mestre/internal/logging"
)

// Removed replaces a piece of a known format.
const Removed = "[segredo removido]"

// Kind says how the engine saw the secret.
type Kind string

// The kinds.
const (
	None       Kind = ""
	Passphrase Kind = "passphrase"
	Format     Kind = "format"
	// FollowUp is the message after a held phrase: held like it, but it does not start
	// another wait.
	FollowUp Kind = "followup"
)

// Held says the whole message is held (the text is not kept at all).
func (k Kind) Held() bool { return k == Passphrase || k == FollowUp }

// passphraseRes find the phrases that give a password in free text ("a senha é",
// "minha senha", "a senha do Gmail é X", "senha=X", "password is X", "pwd X").
// They run on the normalized text, where the spaces and the colon are the plain ones;
// the classes below are for what the normalization leaves.
var passphraseRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bminha[\s\p{Z}]+senha`),
	regexp.MustCompile(`(?i)\b(?:senha|password|passwd|passphrase|pwd)[^\n]{0,40}?(?:[\s\p{Z}](?:é|eh|is|será|sera)(?:[\s\p{Z}]|$)|[\s\p{Z}]*[:=\x{FF1A}\x{FF1D}])`),
	regexp.MustCompile(`(?i)\bpwd[\s\p{Z}]+\S`),
}

// Normalize is the text as the filter reads it: NFKC (full-width and lookalike letters
// become the plain ones) without the invisible format characters (zero width spaces and
// joiners, which split a word the eye still reads).
func Normalize(text string) string {
	return stripFormat(norm.NFKC.String(stripFormat(text)))
}

func stripFormat(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, text)
}

// Screen looks at a text a user sent. A passphrase holds the whole message (the text
// comes back empty: nothing of it is kept); a known format is replaced in place by Removed
// and the rest goes on. mask is the masker of the logs, with every secret of the vault in it.
func Screen(mask func(string) string, text string) (Kind, string) {
	if text == "" {
		return None, text
	}
	text = Normalize(text)
	for _, re := range passphraseRes {
		if re.MatchString(text) {
			return Passphrase, ""
		}
	}
	masked := mask(text)
	if masked == text {
		return None, text
	}
	return Format, strings.ReplaceAll(masked, logging.Redacted, Removed)
}
