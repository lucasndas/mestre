// Package i18n renders the messages of the engine: a message is a stable code
// and its parameters (Msg), the text of each language lives in the catalogs
// (locales/<language>/<area>.json, in the JSON format of i18next) and Render
// puts the two together in the language of whoever reads.
// See docs/plataforma/mensagens-por-codigo.md.
package i18n

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The languages of the catalogs.
const (
	PtBR = "pt-BR"
	En   = "en"
)

// Languages are the languages of the catalogs, the default first.
var Languages = []string{PtBR, En}

//go:embed locales
var localesFS embed.FS

// Msg is a message for a person: a stable code ("decree.scope_required") and
// the parameters its text needs. A parameter is a text, an integer, a time.Time
// or another Msg (translated first, in the same language). A text is never
// interpreted: "{{x}}" inside it comes out as is.
type Msg struct {
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
}

// M makes a message from a code and pairs of name and value.
func M(code string, kv ...any) Msg {
	m := Msg{Code: code}
	if len(kv) > 0 {
		m.Params = make(map[string]any, len(kv)/2)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		k, _ := kv[i].(string)
		m.Params[k] = kv[i+1]
	}
	return m
}

// String is the form of the message for a log: the code and the parameters,
// never translated. The caller masks the parameters that can hold a secret.
func (m Msg) String() string {
	if len(m.Params) == 0 {
		return m.Code
	}
	keys := make([]string, 0, len(m.Params))
	for k := range m.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(m.Code)
	for _, k := range keys {
		fmt.Fprintf(&b, " %s=%v", k, m.Params[k])
	}
	return b.String()
}

// Match is the language of the catalogs for a language tag: a tag that starts
// with "en" is en, anything else (empty, unknown) is pt-BR.
func Match(tag string) string {
	t := strings.ToLower(strings.TrimSpace(tag))
	if t == "en" || strings.HasPrefix(t, "en-") || strings.HasPrefix(t, "en_") {
		return En
	}
	return PtBR
}

// FromAcceptLanguage picks the language of an Accept-Language header: the first
// language it lists decides; empty or unreadable is pt-BR.
func FromAcceptLanguage(h string) string {
	first, _, _ := strings.Cut(h, ",")
	first, _, _ = strings.Cut(first, ";")
	return Match(first)
}

var (
	loadOnce sync.Once
	catalogs map[string]map[string]string // language -> code -> text
)

func load() {
	loadOnce.Do(func() {
		catalogs = map[string]map[string]string{}
		for _, lang := range Languages {
			catalogs[lang] = map[string]string{}
			entries, err := fs.ReadDir(localesFS, path.Join("locales", lang))
			if err != nil {
				panic("i18n: " + err.Error())
			}
			for _, e := range entries {
				m, err := ReadFile(lang, e.Name())
				if err != nil {
					panic("i18n: " + err.Error())
				}
				for k, v := range m {
					catalogs[lang][k] = v
				}
			}
		}
	})
}

// ReadFile reads one catalog file of a language ("decree.json").
func ReadFile(lang, name string) (map[string]string, error) {
	b, err := localesFS.ReadFile(path.Join("locales", lang, name))
	if err != nil {
		return nil, err
	}
	return parseFlat(name, b)
}

// Files lists the files of the catalogs of a language, sorted.
func Files(lang string) []string {
	entries, err := fs.ReadDir(localesFS, path.Join("locales", lang))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// Catalog is every text of a language: code (with the plural suffix, if any)
// to text. The map is a copy.
func Catalog(lang string) map[string]string {
	load()
	out := make(map[string]string, len(catalogs[lang]))
	for k, v := range catalogs[lang] {
		out[k] = v
	}
	return out
}

// Has says whether a language has a text for a code (or for one of its plural forms).
func Has(lang, code string) bool {
	load()
	c := catalogs[lang]
	if _, ok := c[code]; ok {
		return true
	}
	_, ok := c[code+"_one"]
	return ok
}

// Render turns a message into the text of a language (a tag: "en", "en-US",
// "pt-BR"; anything unknown is pt-BR). A code with no text comes out as the
// code, never as a sentence of another language.
func Render(lang string, m Msg) string {
	load()
	return render(catalogs, lang, m)
}

// render is Render over any set of catalogs (language to code to text).
func render(cats map[string]map[string]string, lang string, m Msg) string {
	l := Match(lang)
	text, ok := lookup(cats[l], l, m)
	if !ok {
		return m.Code
	}
	return interpolate(cats, l, text, m.Params)
}

func lookup(c map[string]string, lang string, m Msg) (string, bool) {
	if n, ok := count(m.Params); ok {
		if t, ok := c[m.Code+"_"+pluralForm(lang, n)]; ok {
			return t, true
		}
	}
	t, ok := c[m.Code]
	return t, ok
}

func count(p map[string]any) (int64, bool) {
	v, ok := p["count"]
	if !ok {
		return 0, false
	}
	return asInt(v)
}

func asInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		return int64(n), true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		return int64(n), true
	case float64: // a number that went through JSON
		if n == float64(int64(n)) {
			return int64(n), true
		}
	}
	return 0, false
}

// pluralForm follows Intl.PluralRules: in pt-BR 0 and 1 are "one"; in en only 1.
func pluralForm(lang string, n int64) string {
	if n == 1 || (lang == PtBR && n == 0) {
		return "one"
	}
	return "other"
}

// interpolate replaces {{name}} once: what a parameter holds is never scanned again.
func interpolate(cats map[string]map[string]string, lang, text string, params map[string]any) string {
	if !strings.Contains(text, "{{") {
		return text
	}
	var b strings.Builder
	for {
		i := strings.Index(text, "{{")
		if i < 0 {
			break
		}
		j := strings.Index(text[i:], "}}")
		if j < 0 {
			break
		}
		b.WriteString(text[:i])
		name := strings.TrimSpace(text[i+2 : i+j])
		if v, ok := params[name]; ok {
			b.WriteString(format(cats, lang, v))
		} else {
			b.WriteString(text[i : i+j+2])
		}
		text = text[i+j+2:]
	}
	b.WriteString(text)
	return b.String()
}

// instant is the shape of an instant in RFC 3339 with a zone: a time.Time that went through JSON.
var instant = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$`)

func format(cats map[string]map[string]string, lang string, v any) string {
	switch x := v.(type) {
	case string:
		if instant.MatchString(x) { // an instant that went through JSON
			if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
				return Moment(lang, t)
			}
		}
		return x
	case Msg:
		return render(cats, lang, x)
	case *Msg:
		if x != nil {
			return render(cats, lang, *x)
		}
		return ""
	case time.Time:
		return Moment(lang, x)
	case *time.Time:
		if x != nil {
			return Moment(lang, *x)
		}
		return ""
	case map[string]any: // a Msg that went through JSON
		if code, ok := x["code"].(string); ok {
			p, _ := x["params"].(map[string]any)
			return render(cats, lang, Msg{Code: code, Params: p})
		}
	}
	if n, ok := asInt(v); ok {
		return strconv.FormatInt(n, 10)
	}
	if l, ok := v.([]string); ok {
		return strings.Join(l, ", ")
	}
	if l, ok := v.([]any); ok {
		parts := make([]string, len(l))
		for i, e := range l {
			parts[i] = format(cats, lang, e)
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprint(v)
}

// Moment is an instant in the language of the reader, in the zone the time
// carries (the caller puts it in the time zone of the Guild): "25/10/2026 23:59
// (America/Sao_Paulo, UTC-03:00)" in pt-BR, "2026-10-25 23:59 (...)" in en. The
// seconds show only when there are any. In UTC it says so.
func Moment(lang string, t time.Time) string {
	layout := "2006-01-02 15:04"
	if Match(lang) == PtBR {
		layout = "02/01/2006 15:04"
	}
	if t.Second() != 0 || t.Nanosecond() != 0 {
		layout += ":05"
	}
	loc := t.Location()
	switch {
	case loc == time.UTC || loc.String() == "UTC":
		return t.UTC().Format(layout) + " UTC"
	case loc.String() == "" || loc.String() == "Local": // a zone that came as an offset only
		if _, off := t.Zone(); off == 0 {
			return t.UTC().Format(layout) + " UTC"
		}
		return t.Format(layout) + " (UTC" + t.Format("-07:00") + ")"
	}
	return t.Format(layout) + " (" + loc.String() + ", UTC" + t.Format("-07:00") + ")"
}

// PlainParams are the parameters of a message for a reader that is a program (the
// JSON of the CLI): a message inside becomes its text in the language, an
// instant becomes RFC 3339, the rest stays as it is. The result is a copy.
func PlainParams(lang string, m Msg) map[string]any {
	load()
	out := make(map[string]any, len(m.Params))
	for k, v := range m.Params {
		switch x := v.(type) {
		case Msg, *Msg:
			out[k] = format(catalogs, Match(lang), x)
		case time.Time:
			out[k] = x.Format(time.RFC3339)
		default:
			out[k] = v
		}
	}
	return out
}

// Text is a message that is only a text, shown as it is in every language: what an
// agent or a person wrote. The text is never interpreted.
func Text(s string) Msg { return Msg{Code: "msg.text", Params: map[string]any{"text": s}} }

// Error is an error that carries a message for a person: whoever shows it
// translates the message, and the log gets the code and the parameters.
type Error struct{ M Msg }

// Error is the form of the error for a log: never translated.
func (e *Error) Error() string { return e.M.String() }

// Msg is the message of the error.
func (e *Error) Msg() Msg { return e.M }

// Err makes an error from a code and pairs of name and value.
func Err(code string, kv ...any) error { return &Error{M: M(code, kv...)} }

// Messager is an error that can say itself as a message (users.LevelError, i18n.Error).
type Messager interface{ Msg() Msg }

// OfError is the message of an error for a person: the message the error carries,
// or else its text as a detail (the text of a program is not translated).
func OfError(err error) Msg {
	if err == nil {
		return Msg{}
	}
	var m Messager
	if errors.As(err, &m) {
		return m.Msg()
	}
	return M("api.detail", "detail", err.Error())
}

// IsZero says the message is empty (no code).
func (m Msg) IsZero() bool { return m.Code == "" }

// RenderOrEmpty is Render, and "" for an empty message.
func RenderOrEmpty(lang string, m Msg) string {
	if m.IsZero() {
		return ""
	}
	return Render(lang, m)
}

// Concat joins messages, in order, with nothing between them; empty messages are
// left out. It is for a text made of optional parts, each with its own line breaks.
func Concat(parts ...Msg) Msg {
	var kept []Msg
	for _, p := range parts {
		if !p.IsZero() {
			kept = append(kept, p)
		}
	}
	switch len(kept) {
	case 0:
		return Text("")
	case 1:
		return kept[0]
	}
	return M("msg.join", "a", kept[0], "b", Concat(kept[1:]...))
}

// Join is Concat with a separator between the messages.
func Join(sep Msg, parts ...Msg) Msg {
	var out []Msg
	for _, p := range parts {
		if p.IsZero() {
			continue
		}
		if len(out) > 0 {
			out = append(out, sep)
		}
		out = append(out, p)
	}
	return Concat(out...)
}
