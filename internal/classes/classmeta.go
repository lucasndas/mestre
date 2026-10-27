package classes

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// Files of a class package (docs/classes/classes.md#estrutura-do-pacote).
const (
	ClassFile       = "CLASS.md"
	PermissionsFile = "permissions.yaml"
	HooksFile       = "hooks/hooks.yaml"
	HooksDir        = "hooks"
	SkillsDir       = "skills"
	SkillFile       = "SKILL.md"
)

// MaxNameLen is the longest name of a class.
const MaxNameLen = 40

// RecommendedClassChars is the size of the CLASS.md over which `class check`
// warns (docs/sessao-zero/personagem.md#estrutura-do-mestremd).
const RecommendedClassChars = 3000

var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidName reports whether s is the slug of a class or of an App.
func ValidName(s string) bool { return len(s) <= MaxNameLen && slugRe.MatchString(s) }

// Problem is one thing wrong with a class, or worth saying about it.
type Problem struct {
	// Where is the file or the field ("permissions.yaml", "hooks.yaml
	// post_tool[0]", "CLASS.md app").
	Where string
	Msg   string
}

func (p Problem) String() string {
	if p.Where == "" {
		return p.Msg
	}
	return p.Where + ": " + p.Msg
}

// Meta is the frontmatter of CLASS.md.
type Meta struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	App         string         `yaml:"app"`
	Skills      []string       `yaml:"skills"`
	Provider    string         `yaml:"provider"`
	Model       string         `yaml:"model"`
	Requires    map[string]any `yaml:"requires"`
	// DoneRequires is what the done that ends a step of this class needs
	// (docs/orquestracao/prova-e-devolucao.md#o-done-que-a-classe-exige).
	DoneRequires *DoneRequires `yaml:"done_requires"`
}

// DoneRequires is the done_requires of the frontmatter: Proof is "all" (each
// criterion of the quest needs a proof in the step) or "none" (the default);
// Verify makes the engine run the verification of the workspace before the
// step ends.
type DoneRequires struct {
	Proof  string `yaml:"proof"`
	Verify bool   `yaml:"verify"`
}

// Proof values of done_requires.
const (
	ProofAll  = "all"
	ProofNone = "none"
)

// Done is the done_requires of the class, with the defaults.
func (m Meta) Done() DoneRequires {
	if m.DoneRequires == nil {
		return DoneRequires{Proof: ProofNone}
	}
	d := *m.DoneRequires
	if d.Proof == "" {
		d.Proof = ProofNone
	}
	return d
}

// AppKind is how a class is linked to a GitHub App.
type AppKind string

// The three forms of the link.
const (
	AppNew      AppKind = "novo"
	AppInherit  AppKind = "herdar"
	AppExisting AppKind = "existente"
)

// AppOptions is the explanation of the three forms, shown when a class is
// created without --app.
const AppOptions = "`novo`, `herdar:<classe>` ou `<nome-do-app>`"

// ErrAppRequired is a class created or saved without the App link: every
// class is born linked to an App.
var ErrAppRequired = errors.New("app_required: toda classe nasce vinculada a um App; " +
	"informe `novo` (o Grão-Mestre prepara o App e o Senhor da Guilda o cria e instala pelo link), " +
	"`herdar:<classe>` (usa o App da classe de origem) ou `<nome-do-app>` (um App que já existe)")

// AppLink is a parsed `app` field.
type AppLink struct {
	Kind AppKind
	// Class is the origin class of a herdar; Slug the App of an existing one.
	Class, Slug string
}

func (a AppLink) String() string {
	switch a.Kind {
	case AppNew:
		return string(AppNew)
	case AppInherit:
		return "herdar:" + a.Class
	}
	return a.Slug
}

// ParseAppLink reads the `app` field. An empty one is ErrAppRequired.
func ParseAppLink(s string) (AppLink, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return AppLink{}, ErrAppRequired
	case s == string(AppNew):
		return AppLink{Kind: AppNew}, nil
	case strings.HasPrefix(s, "herdar:"):
		c := strings.TrimPrefix(s, "herdar:")
		if !ValidName(c) {
			return AppLink{}, fmt.Errorf("`herdar:` pede o nome de uma classe (slug `[a-z0-9-]`), recebeu %q", c)
		}
		return AppLink{Kind: AppInherit, Class: c}, nil
	case s == "herdar":
		return AppLink{}, fmt.Errorf("`herdar` pede a classe de origem: `herdar:<classe>`")
	case ValidName(s):
		return AppLink{Kind: AppExisting, Slug: s}, nil
	}
	return AppLink{}, fmt.Errorf("`app` aceita %s; recebeu %q", AppOptions, s)
}

// splitFrontmatter separates the YAML between the first two `---` lines
// from the body.
func splitFrontmatter(data []byte) (front []byte, body string, err error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimPrefix(text, "\ufeff")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return nil, "", errors.New("o arquivo precisa começar com o frontmatter entre linhas `---`")
	}
	var fm strings.Builder
	lines := strings.SplitAfter(rest, "\n")
	for i, l := range lines {
		if strings.TrimRight(l, "\n") == "---" {
			return []byte(fm.String()), strings.Join(lines[i+1:], ""), nil
		}
		fm.WriteString(l)
	}
	return nil, "", errors.New("o frontmatter não termina em uma linha `---`")
}

// parseMeta reads the frontmatter strictly: no unknown field, one document.
func parseMeta(front []byte) (Meta, error) {
	dec := yaml.NewDecoder(bytes.NewReader(front))
	dec.KnownFields(true)
	var m Meta
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return m, errors.New("frontmatter vazio")
		}
		return m, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return m, errors.New("o frontmatter tem mais de um documento YAML")
	}
	return m, nil
}

// chars is the length of the text in characters (what the Mestre layer
// counts), not bytes.
func chars(s string) int { return utf8.RuneCountInString(s) }
