package tables

import (
	"strconv"
	"strings"
)

// MaxSlug is the length the slug of a table is cut at
// (docs/orquestracao/mesas.md#abertura).
const MaxSlug = 30

// fallbackSlug is the slug of a title with no valid character.
const fallbackSlug = "mesa"

// plain maps the accented letters of the Latin alphabets the titles are
// written in to their base letter; any other character is not valid and
// becomes a hyphen. Written out, not computed with the Unicode tables, so the
// slug never changes with a library.
var plain = func() map[rune]rune {
	m := map[rune]rune{}
	for base, set := range map[rune]string{
		'a': "àáâãäåāăąǎ", 'c': "çćĉċč", 'd': "ďđ", 'e': "èéêëēĕėęě", 'g': "ĝğġģ", 'h': "ĥħ",
		'i': "ìíîïĩīĭįı", 'j': "ĵ", 'k': "ķ", 'l': "ĺļľŀł", 'n': "ñńņňŉ", 'o': "òóôõöøōŏőǒ",
		'r': "ŕŗř", 's': "śŝşš", 't': "ţťŧ", 'u': "ùúûüũūŭůűųǔ", 'w': "ŵ", 'y': "ýÿŷ", 'z': "źżž",
	} {
		for _, r := range set {
			m[r] = base
		}
	}
	return m
}()

// Slug makes the slug of the title of an epic: lower case, accents removed,
// everything outside [a-z0-9] becomes `-` (never repeated, never at the
// ends), cut at 30 characters; a title with no valid character is "mesa".
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if base, ok := plain[r]; ok {
			r = base
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
			continue
		}
		dash = true
	}
	s := b.String()
	if len(s) > MaxSlug {
		s = strings.TrimRight(s[:MaxSlug], "-")
	}
	if s == "" {
		return fallbackSlug
	}
	return s
}

// ID is the identifier of the table of an epic: "<n>-<slug>".
func ID(epic int, title string) string { return strconv.Itoa(epic) + "-" + Slug(title) }
