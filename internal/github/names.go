package github

import (
	"strconv"
	"strings"
	"unicode"
)

// MaxNameLength is the longest name of an App on GitHub.
const MaxNameLength = 34

// Roles of the Apps.
const (
	RoleGrandMaster = "grand_master"
	RoleMestre      = "mestre"
	RoleClass       = "class"
)

// DefaultPrefix is `<login>-mestre`, the prefix of the names of the Apps.
func DefaultPrefix(ownerLogin string) string {
	return slug(ownerLogin) + "-mestre"
}

// slug normalizes a text for a name: lower case, no accent, anything that is not a
// letter or a digit becomes a dash, and dashes do not repeat or hang at the ends.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range foldAccents(strings.ToLower(s)) {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// GrandMasterName and MestreName are the names of the two fixed Apps.
func GrandMasterName(prefix string) string { return prefix + "-grao-mestre" }

// MestreName is the name of the App of the table masters.
func MestreName(prefix string) string { return prefix + "-mestre" }

// ClassName is the name of the App of a class, `<prefix>-<class>`: at most 34
// characters, the name of the class cut to fit, and `-2`, `-3`… when taken is
// true for the name (the names are unique in the whole GitHub).
func ClassName(prefix, class string, taken func(string) bool) string {
	c := slug(class)
	if c == "" {
		c = "classe"
	}
	build := func(suffix string) string {
		room := MaxNameLength - len(prefix) - 1 - len(suffix)
		if room < 1 {
			room = 1
		}
		if len(c) > room {
			c = strings.TrimRight(c[:room], "-")
		}
		return prefix + "-" + c + suffix
	}
	name := build("")
	for n := 2; taken != nil && taken(name); n++ {
		name = build("-" + strconv.Itoa(n))
	}
	return name
}

// Slug is how GitHub normalizes the name of an App.
func Slug(name string) string { return slug(name) }

// folded maps the accented letters of the Latin alphabets of the languages the
// Mestre is written in to the letter without the accent.
var folded = strings.NewReplacer(
	"à", "a", "á", "a", "â", "a", "ã", "a", "ä", "a", "å", "a",
	"ç", "c", "è", "e", "é", "e", "ê", "e", "ë", "e",
	"ì", "i", "í", "i", "î", "i", "ï", "i", "ñ", "n",
	"ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "o", "ø", "o",
	"ù", "u", "ú", "u", "û", "u", "ü", "u", "ý", "y", "ÿ", "y", "ß", "ss",
)

func foldAccents(s string) string { return folded.Replace(s) }
