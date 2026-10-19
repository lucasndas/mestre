package classes

import (
	"fmt"
	"slices"
	"strings"

	"github.com/lucasndas/mestre/internal/machinesheet"
)

// Class is a class package read and checked: the frontmatter, the
// instructions, the permissions and the extra hooks, with the files it came
// from and their hash.
type Class struct {
	Scope ClassScope
	// Table is the table of a table class.
	Table string
	Name  string
	Meta  Meta
	// Body is the instructions of CLASS.md, the text the character reads.
	Body        string
	App         AppLink
	Permissions *Permissions
	Hooks       Hooks
	Requires    machinesheet.Requirements
	Files       Files
	Hash        string
}

// ExamineOptions say where the package is and what it can see.
type ExamineOptions struct {
	Scope ClassScope
	// Dir is the name of the folder the class is in; the name in CLASS.md
	// must be the same. Empty skips the comparison (a package not yet in a
	// folder; the name is then compared by the caller).
	Dir string
	// Skill says whether a skill that is not in the package exists: in the
	// project (.agents/skills/) or among the ones the Mestre generates. Nil
	// finds none, except the generated ones (mestre-…), which are accepted.
	Skill func(name string) bool
}

// Examination is what reading a package found. Class is filled as far as the
// package could be read, even with problems; a package with Problems is not
// saved and not used.
type Examination struct {
	Class    *Class
	Problems []Problem
	Warnings []Problem
}

// OK reports whether the package has no problem.
func (e Examination) OK() bool { return len(e.Problems) == 0 }

// ErrInvalidClass is a class package with problems (`invalid_class`).
var ErrInvalidClass = fmt.Errorf("invalid_class")

// InvalidError lists the problems of an invalid class package.
type InvalidError struct{ Problems []Problem }

func (e *InvalidError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		parts[i] = p.String()
	}
	return ErrInvalidClass.Error() + ": " + strings.Join(parts, "; ")
}

func (e *InvalidError) Unwrap() error { return ErrInvalidClass }

// Err is nil for a valid package, else an *InvalidError.
func (e Examination) Err() error {
	if e.OK() {
		return nil
	}
	return &InvalidError{Problems: e.Problems}
}

// Examine reads and checks a class package: the frontmatter and the name,
// the App link, the requirements, permissions.yaml, the hooks and their
// scripts, and the skills. It collects every problem instead of stopping at
// the first, because `mestre class check` lists them all. What needs the
// outside world (the ceiling, the names of the other scopes, the App
// itself) is checked by the Library.
func Examine(files Files, o ExamineOptions) Examination {
	var e Examination
	bad := func(where, format string, a ...any) {
		e.Problems = append(e.Problems, Problem{where, fmt.Sprintf(format, a...)})
	}
	warn := func(where, format string, a ...any) {
		e.Warnings = append(e.Warnings, Problem{where, fmt.Sprintf(format, a...)})
	}
	c := &Class{Scope: o.Scope, Files: files, Hash: files.Hash()}
	e.Class = c

	for _, f := range files {
		top, _, nested := strings.Cut(f.Path, "/")
		if !nested && f.Path != ClassFile && f.Path != PermissionsFile || nested && top != HooksDir && top != SkillsDir {
			warn(f.Path, "fora da estrutura do pacote; não é usado")
		}
	}

	// CLASS.md
	if data, ok := files.Get(ClassFile); !ok {
		bad(ClassFile, "não existe")
	} else if front, body, err := splitFrontmatter(data); err != nil {
		bad(ClassFile, "%v", err)
	} else if m, err := parseMeta(front); err != nil {
		bad(ClassFile+" frontmatter", "%v", err)
	} else {
		c.Meta, c.Body = m, body
		c.Name = m.Name
		checkMeta(c, o, bad)
		if n := chars(body); n > RecommendedClassChars {
			warn(ClassFile, "as instruções têm %d caracteres; acima de %d o Mestre as entrega por arquivo e o personagem pode lê-las tarde", n, RecommendedClassChars)
		}
	}

	// permissions.yaml
	if data, ok := files.Get(PermissionsFile); !ok {
		bad(PermissionsFile, "não existe")
	} else if p, err := ParsePermissions(data); err != nil {
		bad(PermissionsFile, "%v", err)
	} else {
		c.Permissions = p
		// the done of a class that verifies needs the branch: the character commits
		if c.Meta.Done().Verify && p.Git != nil && p.Git.Commit != nil && !*p.Git.Commit {
			bad(ClassFile+" done_requires", "verify: true pede que a classe commite, e %s tem git.commit: false", PermissionsFile)
		}
	}

	// hooks
	if data, ok := files.Get(HooksFile); ok {
		h, probs, warns := parseHooks(data, o.Scope, files)
		c.Hooks = h
		e.Problems = append(e.Problems, probs...)
		e.Warnings = append(e.Warnings, warns...)
		for _, s := range unusedScripts(files, h) {
			warn(s, "nenhum hook roda este arquivo")
		}
	} else if len(files.Under(HooksDir)) > 0 {
		bad(HooksDir, "há arquivos em hooks/ mas não há %s", HooksFile)
	}

	checkSkills(c, o, files, bad, warn)
	return e
}

func checkMeta(c *Class, o ExamineOptions, bad func(string, string, ...any)) {
	m := c.Meta
	switch {
	case m.Name == "":
		bad(ClassFile+" name", "é obrigatório")
	case !ValidName(m.Name):
		bad(ClassFile+" name", "%q não é um slug (`[a-z0-9-]`, até %d caracteres)", m.Name, MaxNameLen)
	case o.Dir != "" && m.Name != o.Dir:
		bad(ClassFile+" name", "%q é diferente do nome da pasta, %q", m.Name, o.Dir)
	}
	if strings.TrimSpace(m.Description) == "" {
		bad(ClassFile+" description", "é obrigatório")
	}
	if link, err := ParseAppLink(m.App); err != nil {
		bad(ClassFile+" app", "%v", err)
	} else {
		c.App = link
		if link.Kind == AppInherit && link.Class == m.Name {
			bad(ClassFile+" app", "uma classe não herda o próprio App")
		}
	}
	if m.Provider != "" && !slices.Contains(machinesheet.KnownProviders(), m.Provider) {
		bad(ClassFile+" provider", "%q desconhecido; os aceitos são %s", m.Provider, strings.Join(machinesheet.KnownProviders(), ", "))
	}
	if d := m.DoneRequires; d != nil && d.Proof != "" && d.Proof != ProofAll && d.Proof != ProofNone {
		bad(ClassFile+" done_requires", "proof aceita `all` ou `none`; recebeu %q", d.Proof)
	}
	if r, err := machinesheet.ParseRequires(m.Requires, machinesheet.Scope{}); err != nil {
		bad(ClassFile+" requires", "%v", err)
	} else {
		c.Requires = r
	}
}

func checkSkills(c *Class, o ExamineOptions, files Files, bad func(string, string, ...any), warn func(string, string, ...any)) {
	inPackage := map[string]bool{}
	for _, f := range files.Under(SkillsDir) {
		parts := strings.Split(f.Path, "/")
		if len(parts) >= 3 && parts[2] == SkillFile && len(parts) == 3 {
			inPackage[parts[1]] = true
		}
	}
	dirs := map[string]bool{}
	for _, f := range files.Under(SkillsDir) {
		if parts := strings.Split(f.Path, "/"); len(parts) >= 3 {
			dirs[parts[1]] = true
		} else {
			bad(f.Path, "uma skill é uma pasta, skills/<nome>/")
		}
	}
	listed := map[string]bool{}
	for _, s := range c.Meta.Skills {
		listed[s] = true
		switch {
		case !ValidName(s):
			bad(ClassFile+" skills", "%q não é um nome de skill (`[a-z0-9-]`)", s)
		case inPackage[s]:
		case dirs[s]:
			bad(SkillsDir+"/"+s, "falta o %s", SkillFile)
		case strings.HasPrefix(s, "mestre-"):
			// A skill the Mestre generates (docs/cli/skills-geradas.md).
		case o.Skill != nil && o.Skill(s):
		default:
			bad(ClassFile+" skills", "a skill %q não está em skills/%s/ nem no .agents/skills/ do projeto", s, s)
		}
	}
	for _, d := range sortedKeys(dirs) {
		if !listed[d] {
			warn(SkillsDir+"/"+d, "a skill não está em `skills` do CLASS.md; não chega ao personagem")
		}
	}
}
