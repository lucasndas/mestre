package sessionzero

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/safefile"
)

// MestreDir is the folder of the Mestre in a project
// (docs/sessao-zero/workspace.md#estrutura-de-mestre).
const MestreDir = ".mestre"

// ModelVersion is the version of the models of the Citadel in use
// (.mestre/modelos/VERSAO): the MESTRE.md, the skills and the hook files.
const ModelVersion = "1"

// ExecutionDirs are the folders of .mestre/ that never go into git, whatever
// the answer about versioning.
var ExecutionDirs = []string{"agents", "anexos", "run"}

// dirs of the structure, relative to .mestre/. mesas/<mesa>/ and
// classes/<classe>/ are made by whoever creates the table or the class.
var structureDirs = []string{"regras", "regras/instrucoes", "classes", "mesas", "modelos", "agents", "anexos", "run"}

// The extra instructions of the users start as a comment: the file is theirs.
const (
	grandMasterInstructions = "<!-- Instruções extras para o Grão-Mestre deste workspace. Escreva abaixo; o Mestre não altera este arquivo. -->\n"
	tableMasterInstructions = "<!-- Instruções extras para os Mestres de mesa deste workspace. Escreva abaixo; o Mestre não altera este arquivo. -->\n"
)

// seedFile is a file of the structure with the content it is born with and
// how an existing one is checked.
type seedFile struct {
	rel     string
	content string
	check   func([]byte) error
}

func seeds() []seedFile {
	return []seedFile{
		{"regras/teto.yaml", classes.DefaultCeilingYAML, func(b []byte) error { _, err := classes.ParseCeiling(b); return err }},
		{"regras/politica-de-aprovacao.yaml", DefaultPolicyYAML, checkPolicy},
		{"regras/instrucoes/grao-mestre.md", grandMasterInstructions, nil},
		{"regras/instrucoes/mestre-da-mesa.md", tableMasterInstructions, nil},
		{"modelos/VERSAO", ModelVersion + "\n", nil},
	}
}

// checkPolicy is the check the session zero can make of an existing policy
// file: a YAML document with the version. The full check is the approval
// flow's (docs/aprovacoes/arquivo-de-politica.md).
func checkPolicy(b []byte) error {
	var doc struct {
		Versao int `yaml:"versao"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return err
	}
	if doc.Versao == 0 {
		return errors.New("falta `versao` no começo do arquivo")
	}
	return nil
}

// Problem is something the engine found and did not touch: a file that is
// not valid, or a path it refuses to follow.
type Problem struct {
	// Path is relative to the folder it was found in.
	Path   string
	Reason string
}

func (p Problem) String() string { return p.Path + ": " + p.Reason }

// StructureReport is what EnsureStructure did.
type StructureReport struct {
	// Created are the folders and files made, relative to the main clone.
	Created []string
	// Invalid are the existing files that are not valid and the paths the
	// engine did not follow: they stay as they are.
	Invalid []Problem
}

// EnsureStructure makes .mestre/ in the main clone with what is missing:
// the folders and the files born with the default content. What exists is
// never overwritten: an existing file is only checked, and an invalid one is
// reported (docs/sessao-zero/workspace.md#regras-e-erros). Everything goes
// through safefile: an agent, or the project, may have put a link there.
func EnsureStructure(root string) StructureReport {
	var rep StructureReport
	base := filepath.Join(root, MestreDir)
	for _, d := range structureDirs {
		dir := filepath.Join(base, filepath.FromSlash(d))
		_, statErr := os.Lstat(dir) // only to tell the report what was made
		missing := errors.Is(statErr, fs.ErrNotExist)
		if err := safefile.MkdirAll(dir); err != nil {
			rep.Invalid = append(rep.Invalid, Problem{Path: path.Join(MestreDir, d), Reason: "não é uma pasta comum (" + err.Error() + ")"})
			continue
		}
		if missing {
			rep.Created = append(rep.Created, path.Join(MestreDir, d)+"/")
		}
	}
	for _, f := range seeds() {
		rel := path.Join(MestreDir, f.rel)
		full := filepath.Join(root, filepath.FromSlash(rel))
		cur, _, err := safefile.Read(full, 1<<20)
		switch {
		case err == nil:
			// a file an earlier version wrote with the group able to write it
			if terr := tightenRule(f.rel, full); terr != nil {
				rep.Invalid = append(rep.Invalid, Problem{Path: rel, Reason: "o grupo do agente pode alterar o arquivo e a permissão não foi corrigida: " + terr.Error()})
			}
			if f.check != nil {
				if cerr := f.check(cur); cerr != nil {
					rep.Invalid = append(rep.Invalid, Problem{Path: rel, Reason: "inválido: " + cerr.Error()})
				}
			}
		case errors.Is(err, fs.ErrNotExist):
			if werr := safefile.Write(full, []byte(f.content), safefile.Options{Mode: modeOf(f.rel)}); werr != nil {
				rep.Invalid = append(rep.Invalid, Problem{Path: rel, Reason: "não foi criado: " + werr.Error()})
				continue
			}
			rep.Created = append(rep.Created, rel)
		default:
			rep.Invalid = append(rep.Invalid, Problem{Path: rel, Reason: "não foi lido: " + err.Error()})
		}
	}
	return rep
}

// modeOf is the mode a seed file is born with: the files of regras/ are read by
// the agents and never written by them (docs/sessao-zero/workspace.md).
func modeOf(rel string) fs.FileMode {
	if strings.HasPrefix(rel, "regras/") {
		return safefile.RulesMode
	}
	return 0 // the default of safefile
}

// tightenRule takes the write of the group off a seed file of regras/.
func tightenRule(rel, full string) error {
	if !strings.HasPrefix(rel, "regras/") {
		return nil
	}
	return safefile.Tighten(full)
}
