package i18n

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // the zone of the Guild is proved on any runner, with no zoneinfo of the system
)

// bundle is a small set of catalogs: the rendering is tested apart from the
// texts of the engine.
var bundle = map[string]map[string]string{
	PtBR: {
		"table.characters_one":   "{{count}} personagem",
		"table.characters_other": "{{count}} personagens",
		"table.level":            "Nível: {{level}}",
		"level.owner":            "Senhor da Guilda",
		"note.free":              "Nota: {{text}}",
		"decree.until":           "Até {{until}}",
	},
	En: {
		"table.characters_one":   "{{count}} character",
		"table.characters_other": "{{count}} characters",
		"table.level":            "Level: {{level}}",
		"level.owner":            "Guildmaster",
		"note.free":              "Note: {{text}}",
		"decree.until":           "Until {{until}}",
	},
}

func TestMatch(t *testing.T) {
	t.Parallel()
	for tag, want := range map[string]string{
		"en": En, "en-US": En, "EN-gb": En, "en_US": En, "pt-BR": PtBR, "pt": PtBR, "fr": PtBR, "": PtBR, "english": En,
	} {
		if got := Match(tag); got != want {
			t.Errorf("Match(%q) = %q, want %q", tag, got, want)
		}
	}
	if got := FromAcceptLanguage("en-US,en;q=0.9,pt;q=0.8"); got != En {
		t.Errorf("Accept-Language en-US = %q", got)
	}
	if got := FromAcceptLanguage("pt-BR,pt;q=0.9,en;q=0.8"); got != PtBR {
		t.Errorf("Accept-Language pt-BR = %q", got)
	}
}

// CA-MSG-03
func TestRenderPluralNestedMessageAndLiteralText(t *testing.T) {
	t.Parallel()
	type tc struct {
		lang  string
		count int
		want  string
	}
	for _, c := range []tc{
		{PtBR, 0, "0 personagem"}, {PtBR, 1, "1 personagem"}, {PtBR, 2, "2 personagens"},
		{En, 0, "0 characters"}, {En, 1, "1 character"}, {En, 2, "2 characters"},
		{"fr", 2, "2 personagens"}, // an unknown language is pt-BR
		{"", 1, "1 personagem"},
	} {
		if got := render(bundle, c.lang, M("table.characters", "count", c.count)); got != c.want {
			t.Errorf("%s count %d: got %q, want %q", c.lang, c.count, got, c.want)
		}
	}
	// a parameter that is another message is translated first, in the same language
	level := M("level.owner")
	if got := render(bundle, PtBR, M("table.level", "level", level)); got != "Nível: Senhor da Guilda" {
		t.Errorf("pt-BR nested = %q", got)
	}
	if got := render(bundle, En, M("table.level", "level", level)); got != "Level: Guildmaster" {
		t.Errorf("en nested = %q", got)
	}
	// a message that came through JSON (a map with a code) is a message too
	viaJSON := map[string]any{"code": "level.owner", "params": map[string]any{}}
	if got := render(bundle, En, M("table.level", "level", viaJSON)); got != "Level: Guildmaster" {
		t.Errorf("en nested through JSON = %q", got)
	}
	// a number that went through JSON is still a count
	if got := render(bundle, En, M("table.characters", "count", float64(1))); got != "1 character" {
		t.Errorf("float count = %q", got)
	}
	// {{x}} inside a text parameter is not interpreted, and neither is what it names
	got := render(bundle, En, M("note.free", "text", "{{level}} and {{text}}", "level", "X"))
	if got != "Note: {{level}} and {{text}}" {
		t.Errorf("literal text = %q", got)
	}
	// a text in the form of a date stays a text, even after JSON; an instant has its own mark
	const date = "2026-01-01T00:00:00Z"
	if got := render(bundle, En, M("note.free", "text", date)); got != "Note: "+date {
		t.Errorf("a text in the form of a date = %q", got)
	}
	raw, err := json.Marshal(M("note.free", "text", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	if err != nil || !strings.Contains(string(raw), `{"instant":"2026-01-01T00:00:00Z"}`) {
		t.Errorf("an instant in JSON = %s, %v", raw, err)
	}
	var back Msg
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if got := render(bundle, En, back); got != "Note: 2026-01-01 00:00 UTC" {
		t.Errorf("an instant through JSON = %q", got)
	}
	// a code with no text is the code, never a sentence of another language
	if got := render(bundle, En, M("nothing.here")); got != "nothing.here" {
		t.Errorf("unknown code = %q", got)
	}
}

// CA-MSG-10
func TestRenderInstantInTheZoneOfTheGuild(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatalf("America/Sao_Paulo is not known (time/tzdata is imported): %v", err)
	}
	at := time.Date(2026, 10, 26, 2, 59, 0, 0, time.UTC).In(loc)
	m := M("decree.until", "until", at)
	if got, want := render(bundle, PtBR, m), "Até 25/10/2026 23:59 (America/Sao_Paulo, UTC-03:00)"; got != want {
		t.Errorf("pt-BR = %q, want %q", got, want)
	}
	if got, want := render(bundle, En, m), "Until 2026-10-25 23:59 (America/Sao_Paulo, UTC-03:00)"; got != want {
		t.Errorf("en = %q, want %q", got, want)
	}
	utc := M("decree.until", "until", time.Date(2026, 10, 26, 2, 59, 7, 0, time.UTC))
	if got, want := render(bundle, En, utc), "Until 2026-10-26 02:59:07 UTC"; got != want {
		t.Errorf("utc = %q, want %q", got, want)
	}
}

func TestPlainParamsForAProgram(t *testing.T) {
	t.Parallel()
	load()
	m := M("level.required", "need", M("level.owner"), "n", 3, "at", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	p := PlainParams(En, m)
	if p["need"] != "Guildmaster" || p["n"] != 3 || p["at"] != "2026-01-02T03:04:05Z" {
		t.Errorf("PlainParams = %#v", p)
	}
}

func TestMsgStringIsForTheLogAndNeverTranslated(t *testing.T) {
	t.Parallel()
	if got := M("a.b", "y", 2, "x", "one").String(); got != "a.b x=one y=2" {
		t.Errorf("String = %q", got)
	}
}
