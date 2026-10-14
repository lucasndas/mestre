package conformance

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"

	"github.com/lucasndas/mestre/internal/providers"
)

// Codes are the random codes of one run: the project's in AGENTS.md (P-…),
// the Mestre's in the layer of the class (M-…), the test skill's (S-…) and
// the probe skill's of Claude Code (D-…), which lives only in .agents/skills/.
type Codes struct {
	Project, Mestre, Skill, Probe string
}

// codeAlphabet has no letters that read like digits.
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// NewCodes draws the codes of a run.
func NewCodes() (Codes, error) {
	draw := func(prefix string) (string, error) {
		var b [6]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", fmt.Errorf("conformance codes: %w", err)
		}
		for i := range b {
			b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
		}
		return prefix + "-" + string(b[:]), nil
	}
	var c Codes
	var err error
	for _, f := range []struct {
		p   string
		dst *string
	}{{"P", &c.Project}, {"M", &c.Mestre}, {"S", &c.Skill}, {"D", &c.Probe}} {
		if *f.dst, err = draw(f.p); err != nil {
			return Codes{}, err
		}
	}
	return c, nil
}

// The test words: the project says AZUL, the Mestre VERDE, and the Mestre
// wins the conflict.
const (
	WordProject = "AZUL"
	WordMestre  = "VERDE"
)

// ShellNames are the names a model of each provider gives its factory shell
// tool (docs/provedores/adaptador.md#ferramentas-nome-nativo--tipo-toolkind):
// the name in the hook payload and the one the model sees, where they
// differ. Citing one is the sign that the factory prompt goes on after the
// Mestre layer (step 2).
var ShellNames = map[providers.ID][]string{
	providers.ClaudeCode:  {"Bash"},
	providers.Codex:       {"shell", "exec_command", "local_shell", "Bash"},
	providers.Antigravity: {"run_command"},
}

// Check is the verdict of a verifier.
type Check struct {
	Pass   bool
	Reason string
}

func pass() Check { return Check{Pass: true} }

func fail(f string, a ...any) Check { return Check{Reason: fmt.Sprintf(f, a...)} }

func (c Check) String() string {
	if c.Pass {
		return "pass"
	}
	return "fail: " + c.Reason
}

// plain drops the markdown marks a model puts around a line.
func plain(answer string) string { return strings.NewReplacer("*", "", "`", "").Replace(answer) }

// hasWord reports whether word is in text as a whole word, in any case.
func hasWord(text, word string) bool {
	return regexp.MustCompile(`(?i)(^|[^\pL\pN_])` + regexp.QuoteMeta(word) + `($|[^\pL\pN_])`).MatchString(text)
}

func hasCode(text, code string) bool {
	return code != "" && strings.Contains(strings.ToUpper(text), code)
}

func missing(text string, cs ...string) []string {
	var out []string
	for _, c := range cs {
		if !hasCode(text, c) {
			out = append(out, c[:2]+"…")
		}
	}
	return out
}

// wordLine finds the answer of the line "PALAVRA: <word>", at the start of
// a line.
var wordLine = regexp.MustCompile(`(?im)^\s*PALAVRA\s*[:=]\s*([\pL]+)`)

// CheckInstructions is the verifier of step 2 (and of step 10): the answer
// has the Mestre code and the project code, the test word is VERDE and not
// AZUL, and it cites the factory shell tool of the provider. With the line
// "PALAVRA: …" of the question, the word is that line's; without it, the
// answer must hold VERDE and not AZUL.
func CheckInstructions(answer string, c Codes, p providers.ID) Check {
	text := plain(answer)
	if m := missing(text, c.Mestre, c.Project); len(m) > 0 {
		return fail("a resposta não tem %s", strings.Join(m, " nem "))
	}
	// The last line of the word is the answer's: a summary of the thought
	// of the model, shown above it, may quote the question.
	if all := wordLine.FindAllStringSubmatch(text, -1); all != nil {
		w := all[len(all)-1]
		if !strings.EqualFold(w[1], WordMestre) {
			return fail("a palavra-teste respondida é %s, e a do Mestre é %s", strings.ToUpper(w[1]), WordMestre)
		}
	} else {
		switch {
		case !hasWord(text, WordMestre):
			return fail("a resposta não tem a palavra-teste %s", WordMestre)
		case hasWord(text, WordProject):
			return fail("a resposta tem a palavra-teste do projeto, %s", WordProject)
		}
	}
	names := ShellNames[p]
	for _, n := range names {
		if hasWord(text, n) {
			return pass()
		}
	}
	return fail("a resposta não cita a ferramenta de shell de fábrica (%s)", strings.Join(names, ", "))
}

// CheckSkill is the verifier of step 3: the answer has the code the body of
// the skill gives.
func CheckSkill(answer string, c Codes) Check {
	if !hasCode(plain(answer), c.Skill) {
		return fail("a resposta não tem o código da skill (S-…)")
	}
	return pass()
}

// CheckProbe is step 9 (Claude Code): true when the answer has the code of
// the skill that lives only in .agents/skills/.
func CheckProbe(answer string, c Codes) bool { return hasCode(plain(answer), c.Probe) }
