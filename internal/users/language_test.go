package users_test

import (
	"testing"

	"github.com/mestre-ia/mestre/internal/users"
)

func TestMatchLanguageTakesEnglishByItsPrefixAndEverythingElseIsPortuguese(t *testing.T) {
	t.Parallel()
	for tag, want := range map[string]string{"en": "en", "EN": "en", "en-US": "en", "en_GB.UTF-8": "en", "pt": "pt-BR", "pt-BR": "pt-BR", "de": "pt-BR", "C": "pt-BR", "": "pt-BR", "e": "pt-BR"} {
		if got := users.MatchLanguage(tag); got != want {
			t.Errorf("MatchLanguage(%q) = %q, want %q", tag, got, want)
		}
	}
	if got := (users.User{Language: "en"}).LanguageFor("pt"); got != "en" {
		t.Errorf("the choice must win: %q", got)
	}
	if got := (users.User{}).LanguageFor("en-US"); got != "en" {
		t.Errorf("the hint: %q", got)
	}
}
