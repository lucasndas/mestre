package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/glossary"
)

// Issue is one finding, printed as <file>:<line>: <check>: <message>.
type Issue struct {
	File    string
	Line    int
	Check   string
	Msg     string
	Warning bool
}

func (i Issue) String() string {
	s := fmt.Sprintf("%s:%d: %s: %s", i.File, i.Line, i.Check, i.Msg)
	if i.Warning {
		return "aviso: " + s
	}
	return s
}

// Check names (docs/qualidade/verificacao-de-docs.md).
const (
	ckIndex     = "índice"
	ckIndexItem = "item de índice"
	ckLink      = "link"
	ckSize      = "tamanho"
	ckHeader    = "cabeçalho"
	ckSections  = "seções"
	ckVerify    = "verificar na implementação"
	ckCriteria  = "critérios"
	ckName      = "nome"
	ckGlossary  = "glossário"
	ckOrder     = "ordem"
)

// Limits and fixed paths.
const (
	maxLines       = 200
	warnLines      = 150
	docsDir        = "docs"
	glossaryPath   = "docs/produto/glossario.md"
	orderPath      = "docs/desenvolvimento/ordem-de-implementacao.md"
	orderMapHeader = "Mapa de documentos por marco e etapa"
	adrDir         = "docs/decisoes"
	opsDir         = "docs/operacao"
)

var (
	featureSections  = []string{"Objetivo", "Comportamento", "Regras e erros", "Critérios de aceite", "Depende de"}
	featureOptional  = []string{"Verificar na implementação", "A confirmar", "Fontes"}
	adrSections      = []string{"Status", "Contexto", "Opções consideradas", "Decisão", "Consequências"}
	adrOptional      = []string{"Fontes"}
	components       = []string{"Motor", "Guardião", "CLI", "Painel", "Todos", "Projeto"}
	reportNames      = []string{"SUMMARY.md", "IMPLEMENTATION_NOTES.md", "CHANGES.md", "PROGRESS.md", "resumo.md", "relatorio.md", "progresso.md", "mudancas.md"}
	indexHeader      = "| Documento | O que cobre | Quando ler |"
	rootIndexHeader  = "| Área | O que cobre | Quando ler |"
	headerRe         = regexp.MustCompile(`^> \*\*Assunto:\*\* (.+) · \*\*Componente:\*\* (.+) · \*\*Atualizado:\*\* (\S+)$`)
	nameRe           = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	adrNameRe        = regexp.MustCompile(`^[0-9]{4}-`)
	criterionRe      = regexp.MustCompile(`^- \*\*CA-([A-Z0-9]+(?:-[A-Z0-9]+)*)-([0-9]{2})\*\* · \*(rápida|completa|pesada)\* — \*\*Dado\*\* .+, \*\*quando\*\* .+, \*\*então\*\* .+\.$`)
	criterionIDRe    = regexp.MustCompile(`^- \*\*CA-([A-Z0-9]+(?:-[A-Z0-9]+)*)-([0-9]{2})\*\*`)
	criterionSuiteRe = regexp.MustCompile(`^- \*\*CA-[A-Z0-9-]+\*\* · \*(rápida|completa|pesada)\*`)
	mvpRe            = regexp.MustCompile(`\(MVP-(?:1[0-5]|[1-9])(?:, MVP-(?:1[0-5]|[1-9]))*\)`)
	verifyMarkers    = []string{"**Padrão:**", "**Se falhar:**", "**Onde:**"}
)

// Result holds every error and warning.
type Result struct {
	Errors   []Issue
	Warnings []Issue
}

type checker struct {
	fsys  fs.FS
	today time.Time
	docs  map[string]*doc // every parsed markdown file by path
	files []string        // the checked files: docs/**.md, AGENTS.md, README.md
	dirs  []string        // docs and its subfolders
	res   Result
}

func (c *checker) errf(file string, line int, check, format string, args ...any) {
	c.res.Errors = append(c.res.Errors, Issue{File: file, Line: line, Check: check, Msg: fmt.Sprintf(format, args...)})
}

func (c *checker) warnf(file string, line int, check, format string, args ...any) {
	c.res.Warnings = append(c.res.Warnings, Issue{File: file, Line: line, Check: check, Msg: fmt.Sprintf(format, args...), Warning: true})
}

// Check runs every check of verificacao-de-docs.md over fsys (the
// repository root). today comes from the injectable clock.
func Check(fsys fs.FS, today time.Time) Result {
	c := &checker{fsys: fsys, today: today, docs: map[string]*doc{}}
	c.collect()
	c.checkNames()
	for _, f := range c.files {
		d := c.docs[f]
		c.checkLinks(d)
		if strings.HasPrefix(f, docsDir+"/") {
			c.checkSize(d)
			c.checkHeader(d)
			c.checkSections(d)
			c.checkVerify(d)
		}
	}
	c.checkIndexes()
	c.checkCriteria()
	c.checkGlossary()
	c.checkOrder()
	sortIssues(c.res.Errors)
	sortIssues(c.res.Warnings)
	return c.res
}

func sortIssues(is []Issue) {
	sort.SliceStable(is, func(i, j int) bool {
		if is[i].File != is[j].File {
			return is[i].File < is[j].File
		}
		return is[i].Line < is[j].Line
	})
}

func (c *checker) collect() {
	_ = fs.WalkDir(c.fsys, docsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // a missing docs folder is reported by the index check
		}
		if d.IsDir() {
			c.dirs = append(c.dirs, p)
		} else if strings.HasSuffix(p, ".md") {
			c.files = append(c.files, p)
		}
		return nil
	})
	for _, f := range []string{"AGENTS.md", "README.md"} {
		if exists(c.fsys, f) {
			c.files = append(c.files, f)
		}
	}
	sort.Strings(c.files)
	for _, f := range c.files {
		c.load(f)
	}
}

func (c *checker) load(p string) *doc {
	if d, ok := c.docs[p]; ok {
		return d
	}
	b, err := fs.ReadFile(c.fsys, p)
	if err != nil {
		return nil
	}
	d := parseDoc(p, b)
	c.docs[p] = d
	return d
}

// Check 8: file and folder names.
func (c *checker) checkNames() {
	_ = fs.WalkDir(c.fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		name := d.Name()
		if d.IsDir() && (name == ".git" || name == "node_modules") {
			return fs.SkipDir
		}
		if !d.IsDir() && slices.ContainsFunc(reportNames, func(r string) bool { return strings.EqualFold(r, name) }) {
			c.errf(p, 1, ckName, "nome de relatório proibido (histórico fica no git)")
		}
		if p == docsDir || !strings.HasPrefix(p, docsDir+"/") {
			return nil
		}
		switch {
		case d.IsDir() && !nameRe.MatchString(name):
			c.errf(p, 1, ckName, "pasta fora do padrão: só minúsculas sem acento, dígitos e hífen")
		case !d.IsDir() && name != "INDEX.md" && !nameRe.MatchString(strings.TrimSuffix(name, ".md")):
			c.errf(p, 1, ckName, "arquivo fora do padrão: só minúsculas sem acento, dígitos e hífen, terminando em .md")
		case !d.IsDir() && !strings.HasSuffix(name, ".md"):
			c.errf(p, 1, ckName, "arquivo fora do padrão: termina em .md")
		case !d.IsDir() && path.Dir(p) == adrDir && name != "INDEX.md" && !adrNameRe.MatchString(name):
			c.errf(p, 1, ckName, "ADR começa com quatro dígitos")
		}
		return nil
	})
}

// Check 3: links and anchors. Missing files linked from an index table row
// are reported once, by the index-item check.
func (c *checker) checkLinks(d *doc) {
	isIndex := path.Base(d.path) == "INDEX.md"
	for _, l := range d.links {
		r := resolve(d, l.target)
		if r.file == "" && r.ok {
			continue // external link: not accessed
		}
		if !r.ok {
			c.errf(d.path, l.line, ckLink, "%s sai do repositório", l.target)
			continue
		}
		if !exists(c.fsys, r.file) {
			if !isIndex || !strings.HasPrefix(strings.TrimSpace(d.lines[l.line-1]), "|") {
				c.errf(d.path, l.line, ckLink, "%s não existe", l.target)
			}
			continue
		}
		if r.anchor == "" || !strings.HasSuffix(r.file, ".md") {
			continue
		}
		if t := c.load(r.file); t != nil && !t.anchorSet()[r.anchor] {
			c.errf(d.path, l.line, ckLink, "%s: o título #%s não existe em %s", l.target, r.anchor, r.file)
		}
	}
}

// Check 4: size.
func (c *checker) checkSize(d *doc) {
	switch n := d.lineCount(); {
	case n > maxLines:
		c.errf(d.path, maxLines+1, ckSize, "%d linhas, limite %d: divida o documento", n, maxLines)
	case n > warnLines:
		c.warnf(d.path, warnLines+1, ckSize, "%d linhas, acima de %d: considere dividir", n, warnLines)
	}
}

// Check 5: header.
func (c *checker) checkHeader(d *doc) {
	line := func(i int) string {
		if i <= len(d.lines) {
			return d.lines[i-1]
		}
		return ""
	}
	if !strings.HasPrefix(line(1), "# ") || len(strings.TrimSpace(line(1))) < 3 {
		c.errf(d.path, 1, ckHeader, "linha 1 deve ser `# <Título>`")
	}
	if line(2) != "" {
		c.errf(d.path, 2, ckHeader, "linha 2 deve ser vazia")
	}
	m := headerRe.FindStringSubmatch(line(3))
	if m == nil {
		c.errf(d.path, 3, ckHeader, "linha 3 deve ser `> **Assunto:** <texto> · **Componente:** <valor> · **Atualizado:** <AAAA-MM-DD>`")
		return
	}
	if !slices.Contains(components, m[2]) {
		c.errf(d.path, 3, ckHeader, "componente %q inválido; use %s", m[2], strings.Join(components, ", "))
	}
	date, err := time.Parse(time.DateOnly, m[3])
	switch {
	case err != nil:
		c.errf(d.path, 3, ckHeader, "data %q inválida; use AAAA-MM-DD", m[3])
	case date.Format(time.DateOnly) > c.today.Format(time.DateOnly):
		c.errf(d.path, 3, ckHeader, "data %s está no futuro (hoje é %s)", m[3], c.today.Format(time.DateOnly))
	}
}

// Check 6: sections of features, ADRs and indexes.
func (c *checker) checkSections(d *doc) {
	dir, base := path.Dir(d.path), path.Base(d.path)
	switch {
	case base == "INDEX.md":
		c.checkIndexBody(d)
	case dir == opsDir:
		// operation guides only need the header
	case dir == adrDir:
		c.checkOrderOf(d, adrSections, adrOptional, "ADR")
	case d.hasSection("Objetivo"):
		c.checkOrderOf(d, featureSections, featureOptional, "documento de funcionalidade")
	}
}

func (c *checker) checkOrderOf(d *doc, required, optional []string, kind string) {
	hs := d.h2()
	names := make([]string, len(hs))
	for i, h := range hs {
		names[i] = h.text
	}
	bad := -1
	i := 0
	for ; i < len(required); i++ {
		if i >= len(names) || names[i] != required[i] {
			bad = i
			break
		}
	}
	if bad < 0 {
		rest := names[len(required):]
		next := 0
		for j, n := range rest {
			k := slices.Index(optional[next:], n)
			if k < 0 {
				bad = len(required) + j
				break
			}
			next += k + 1
		}
	}
	if bad < 0 {
		return
	}
	line := 1
	if bad < len(hs) {
		line = hs[bad].line
	}
	want := strings.Join(required, ", ")
	for _, o := range optional {
		want += " [, " + o + "]"
	}
	c.errf(d.path, line, ckSections, "%s com seções de nível 2 fora da ordem esperada: %s (encontrado: %s)", kind, want, strings.Join(names, ", "))
}

func (c *checker) checkIndexBody(d *doc) {
	root := d.path == docsDir+"/INDEX.md"
	want := indexHeader
	if root {
		want = rootIndexHeader
	}
	inTable, sawTable := false, false
	for i := 3; i < len(d.lines); i++ {
		l := strings.TrimSpace(d.lines[i])
		switch {
		case l == "":
			if inTable {
				inTable = false
			}
		case strings.HasPrefix(l, "|"):
			if sawTable && !inTable {
				c.errf(d.path, i+1, ckSections, "índice com mais de uma tabela")
				return
			}
			if !sawTable && l != want {
				c.errf(d.path, i+1, ckSections, "índice deve ter a tabela com as colunas %s", want)
				return
			}
			inTable, sawTable = true, true
		case root && !sawTable:
			// the root index may orient the reader before the table
		default:
			c.errf(d.path, i+1, ckSections, "índice só tem o cabeçalho e a tabela %s", want)
			return
		}
	}
	if !sawTable {
		c.errf(d.path, 1, ckSections, "índice sem a tabela %s", want)
	}
}

// Check 6 (items): every item of "Verificar na implementação".
func (c *checker) checkVerify(d *doc) {
	start, end, ok := d.section("Verificar na implementação")
	if !ok {
		return
	}
	type item struct {
		line int
		text string
	}
	var items []item
	for i := start; i < end; i++ {
		l := d.lines[i]
		switch {
		case strings.HasPrefix(l, "- "):
			items = append(items, item{i + 1, l})
		case strings.TrimSpace(l) != "" && len(items) > 0:
			items[len(items)-1].text += " " + strings.TrimSpace(l)
		}
	}
	for _, it := range items {
		var missing []string
		if !strings.HasPrefix(it.text, "- **Verificar na implementação:**") {
			missing = append(missing, "**Verificar na implementação:** no início")
		}
		for _, m := range verifyMarkers {
			if !strings.Contains(it.text, m) {
				missing = append(missing, m)
			}
		}
		if len(missing) > 0 {
			c.errf(d.path, it.line, ckVerify, "item incompleto, falta %s", strings.Join(missing, ", "))
		}
	}
}

// Check 1 and 2: every document in its folder index, every index item exists.
func (c *checker) checkIndexes() {
	if len(c.dirs) == 0 {
		c.errf(docsDir, 1, ckIndex, "pasta docs/ não encontrada")
		return
	}
	listed := map[string]map[string]bool{} // index path -> resolved targets
	for _, dir := range c.dirs {
		idx := dir + "/INDEX.md"
		d := c.docs[idx]
		if d == nil {
			c.errf(dir, 1, ckIndex, "pasta sem INDEX.md")
			continue
		}
		listed[idx] = map[string]bool{}
		for _, l := range d.links {
			r := resolve(d, l.target)
			if r.file != "" {
				listed[idx][r.file] = true
			}
		}
		for i, line := range d.lines {
			t := strings.TrimSpace(line)
			if !strings.HasPrefix(t, "|") || d.inCode[i] || strings.HasPrefix(t, "|---") || strings.HasPrefix(t, "| Documento ") || strings.HasPrefix(t, "| Área ") {
				continue
			}
			links := d.linksIn(i, i+1)
			if len(links) == 0 {
				c.errf(idx, i+1, ckIndexItem, "linha do índice sem link")
			}
			for _, l := range links {
				if r := resolve(d, l.target); r.file != "" && !exists(c.fsys, r.file) {
					c.errf(idx, i+1, ckIndexItem, "%s não existe", l.target)
				}
			}
		}
	}
	for _, f := range c.files {
		if !strings.HasPrefix(f, docsDir+"/") || f == docsDir+"/INDEX.md" {
			continue
		}
		dir := path.Dir(f)
		if path.Base(f) == "INDEX.md" { // a subfolder is listed in the parent index
			parent := path.Dir(dir) + "/INDEX.md"
			if l, ok := listed[parent]; ok && !l[f] {
				c.errf(f, 1, ckIndex, "pasta %s não aparece em %s", dir, parent)
			}
			continue
		}
		if l, ok := listed[dir+"/INDEX.md"]; ok && !l[f] {
			c.errf(f, 1, ckIndex, "%s não aparece em %s/INDEX.md", f, dir)
		}
	}
}

// Check 7: acceptance criteria.
func (c *checker) checkCriteria() {
	owner := map[string]string{} // sigla -> first document
	for _, f := range c.files {
		d := c.docs[f]
		start, end, ok := d.section("Critérios de aceite")
		if !ok || !strings.HasPrefix(f, docsDir+"/") {
			continue
		}
		docSigla := ""
		nums := map[string]int{}
		for i := start; i < end; i++ {
			l := d.lines[i]
			if !strings.HasPrefix(l, "- ") {
				continue
			}
			ln := i + 1
			id := criterionIDRe.FindStringSubmatch(l)
			if id == nil {
				c.errf(f, ln, ckCriteria, "item sem identificador `**CA-<SIGLA>-<NN>**`")
				continue
			}
			sigla, nn := id[1], id[2]
			switch {
			case !criterionSuiteRe.MatchString(l):
				c.errf(f, ln, ckCriteria, "CA-%s-%s sem a suíte (*rápida*, *completa* ou *pesada*)", sigla, nn)
			case !criterionRe.MatchString(l):
				c.errf(f, ln, ckCriteria, "CA-%s-%s fora do formato `- **CA-<SIGLA>-<NN>** · *<suíte>* — **Dado** …, **quando** …, **então** ….`", sigla, nn)
			case strings.Contains(l, "· *pesada*") && !mvpRe.MatchString(l):
				c.errf(f, ln, ckCriteria, "CA-%s-%s é *pesada* e não cita um critério global (MVP-1 a MVP-15)", sigla, nn)
			}
			if docSigla == "" {
				docSigla = sigla
				if prev, ok := owner[sigla]; ok && prev != f {
					c.errf(f, ln, ckCriteria, "sigla %s já é usada em %s", sigla, prev)
				} else {
					owner[sigla] = f
				}
			} else if sigla != docSigla {
				c.errf(f, ln, ckCriteria, "sigla %s diferente de %s, a deste documento", sigla, docSigla)
			}
			if prev, ok := nums[nn]; ok {
				c.errf(f, ln, ckCriteria, "CA-%s-%s repetido (linha %d)", sigla, nn, prev)
			}
			nums[nn] = ln
		}
	}
}

// Check 9: the glossary is readable by the help reader.
func (c *checker) checkGlossary() {
	d := c.docs[glossaryPath]
	if d == nil {
		return
	}
	_, problems := glossary.Parse(bytes.NewReader([]byte(strings.Join(d.lines, "\n"))))
	for _, p := range problems {
		c.errf(glossaryPath, p.Line, ckGlossary, "%s", p.Msg)
	}
}
