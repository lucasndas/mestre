package main

import (
	"go/ast"
	"go/token"
	"regexp"
	"strings"
)

// ptWords are words of the Portuguese of the product that give a sentence away without an accent
// ("Mesa pausada."): a literal that starts with a capital letter, has two words or more and one of
// them is a sentence (SQL is not).
var sqlStart = regexp.MustCompile("^[`\"]\\s*(SELECT|INSERT|UPDATE|DELETE|CREATE|ALTER|DROP|WITH|PRAGMA)\\b")
var capStart = regexp.MustCompile("^[`\"][A-Z]")
var ptWords = regexp.MustCompile(`\b(Mesa|mesa|pausada|pausado|campanha|Campanha|Guilda|Senhor|Cidadela|Torre|decreto|Decreto|travado|travada)\b`)

// ptTextPkgs are the packages whose texts for a person come from the catalogs
// (docs/plataforma/mensagens-por-codigo.md): a sentence in Portuguese written in
// the code would reach everybody in Portuguese.
var ptTextPkgs = []string{"internal/telegram/", "internal/panel/", "internal/notices/", "internal/blocking/", "internal/approvals/"}

const ptLetters = "áàâãéêíóôõúçÁÀÂÃÉÊÍÓÔÕÚÇ"

// checkPortugueseText (pt-text) forbids a string literal with a letter that carries a
// Portuguese accent in non-test code of the packages above. A text for a person goes
// in the catalogs; the few literals that must stay (the words of the glossary in a
// text for an agent, a header the Grão-Mestre reads) carry a `mestre:allow pt-text`.
func checkPortugueseText(s *source, add func(Issue)) {
	in := false
	for _, p := range ptTextPkgs {
		in = in || strings.HasPrefix(s.path, p)
	}
	if !in {
		return
	}
	ast.Inspect(s.file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		sentence := strings.Contains(lit.Value, " ") && capStart.MatchString(lit.Value) && !sqlStart.MatchString(lit.Value) && ptWords.MatchString(lit.Value)
		if !strings.ContainsAny(lit.Value, ptLetters) && !sentence {
			return true
		}
		add(Issue{s.path, s.line(lit.Pos()), rulePtText, "a literal with Portuguese text: a text for a person comes from the catalogs (internal/i18n/locales), in both languages"})
		return true
	})
}
