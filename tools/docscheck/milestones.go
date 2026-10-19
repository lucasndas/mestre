package main

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Sections of ordem-de-implementacao.md the milestone checks read.
const (
	milestonesHeader = "Marcos"
	deferredHeader   = "Critérios em marcos posteriores"
	operatorHeader   = "Operador do PC no M7"
	earlyHeader      = "Critérios antecipados"
	spikesHeader     = "Spikes do M1"
	auditColumn      = "Auditoria"
)

var (
	// milestoneRowRe reads a row of the table of milestones: `| M4 |`, or the
	// row `| M4+ |` of the phase that closes M4, between M4 and M5.
	milestoneRowRe = regexp.MustCompile(`^\| M([0-9]+)(\+)? \|`)
	dateRe         = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	idPattern      = `CA-[A-Z0-9]+(?:-[A-Z0-9]+)*-[0-9]{2}`
	// deferredRe finds `CA-X-10` and the range `CA-X-10 a CA-X-17`.
	deferredRe = regexp.MustCompile(`(` + idPattern + `)(?: a (` + idPattern + `))?`)
	spikeRowRe = regexp.MustCompile(`(?i)\(spike ([0-9]+)\)`)

	// Stale milestone comments (verificacao-de-docs.md, check 13). The words
	// are case-insensitive; the milestone is an upper-case M and a digit.
	staleMilestoneRes = []*regexp.Regexp{
		// the verb arrives/comes/lands, then "in M<n>" a few words later
		regexp.MustCompile(`\b(?i:arrives?|comes?|lands?|replaces?|replaced|closes?)\b[^.;:]{0,40}?\b(?i:in|at|with)\s+(?:the\s+)?M([0-9])\b`),
		// the same verbs, then the milestone between parentheses
		regexp.MustCompile(`\b(?i:arrives?|comes?|lands?|replaces?|replaced|closes?|chegam?|vem|vêm)\b[^.;:]{0,60}?\(M([0-9])\)`),
		// "until M<n>"
		regexp.MustCompile(`\b(?i:until|till)\s+(?:the\s+)?M([0-9])\b`),
		// "waits for M<n>", "pending M<n>", "deferred to M<n>"
		regexp.MustCompile(`\b(?i:waits?\s+for|waiting\s+for|pending|deferred\s+(?:to|until)|postponed\s+(?:to|until)|left\s+for)\s+(?:the\s+)?M([0-9])\b`),
		// "chega no M<n>", "vem/vêm com o M<n>", "fica para o M<n>"
		regexp.MustCompile(`\b(?i:chegam?|vem|vêm|ficam?|esperam?|aguardam?)\b[^.;:]{0,40}?\b(?i:no|com o|para o|até o|do)\s+M([0-9])\b`),
		// "até o M<n>", "até M<n>"
		regexp.MustCompile(`\b(?i:até)\s+(?:o\s+)?M([0-9])\b`),
	}
	// "waits for spike <n>", "pending spike <n>", "aguarda o spike <n>"
	staleSpikeRe = regexp.MustCompile(`\b(?i:waits?\s+for|waiting\s+for|until|pending|espera(?:ndo)?|aguarda(?:ndo)?)\s+(?:(?i:the|o)\s+)?(?i:spike)\s+([0-9]+)\b`)
)

// milestone is a row of the table of milestones. M<n>+ is the phase between
// M<n> and M<n+1>: it has no documents in the map, only an audit.
type milestone struct {
	n     int
	plus  bool
	audit string // AAAA-MM-DD, or "" while the milestone is not audited
	line  int
}

// rank orders the milestones: M4 comes before M4+, and M4+ before M5.
func rank(n int, plus bool) int {
	if plus {
		return 2*n + 1
	}
	return 2 * n
}

func (m *milestone) rank() int { return rank(m.n, m.plus) }

// name is the milestone as the table writes it: M4 or M4+.
func (m *milestone) name() string {
	if m.plus {
		return fmt.Sprintf("M%d+", m.n)
	}
	return fmt.Sprintf("M%d", m.n)
}

func splitRow(line string) []string {
	cells := strings.Split(strings.TrimSpace(line), "|")
	if len(cells) < 2 {
		return nil
	}
	cells = cells[1 : len(cells)-1]
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// loadMilestones reads the table of milestones with its audit column and
// validates the dates. The milestones come by rank, so M4+ falls between M4
// and M5. It returns nil when the table cannot be read.
func (c *checker) loadMilestones(od *doc) map[int]*milestone {
	start, end, ok := od.section(milestonesHeader)
	if !ok {
		c.errf(orderPath, 1, ckAudit, "seção %q não encontrada", milestonesHeader)
		return nil
	}
	col := -1
	out := map[int]*milestone{}
	for i := start; i < end; i++ {
		line := od.lines[i]
		if strings.HasPrefix(line, "| Marco ") {
			for k, h := range splitRow(line) {
				if h == auditColumn {
					col = k
				}
			}
			if col < 0 {
				c.errf(orderPath, i+1, ckAudit, "a tabela de marcos precisa da coluna %q (data AAAA-MM-DD ou vazia)", auditColumn)
				return nil
			}
			continue
		}
		m := milestoneRowRe.FindStringSubmatch(line)
		if m == nil || col < 0 {
			continue
		}
		cells := splitRow(line)
		if col >= len(cells) {
			c.errf(orderPath, i+1, ckAudit, "linha do M%s%s sem a coluna %q", m[1], m[2], auditColumn)
			continue
		}
		n, _ := strconv.Atoi(m[1])
		ms := &milestone{n: n, plus: m[2] != "", audit: cells[col], line: i + 1}
		out[ms.rank()] = ms
		if ms.audit == "" {
			continue
		}
		d, err := time.Parse(time.DateOnly, ms.audit)
		switch {
		case !dateRe.MatchString(ms.audit) || err != nil:
			c.errf(orderPath, ms.line, ckAudit, "%s: auditoria %q inválida; use AAAA-MM-DD ou deixe vazia", ms.name(), ms.audit)
		case d.Format(time.DateOnly) > c.today.UTC().Format(time.DateOnly):
			c.errf(orderPath, ms.line, ckAudit, "%s: auditoria %s está no futuro (hoje é %s em UTC)", ms.name(), ms.audit, c.today.UTC().Format(time.DateOnly))
		}
	}
	if col < 0 {
		c.errf(orderPath, start, ckAudit, "tabela de marcos não encontrada em %q", milestonesHeader)
		return nil
	}
	return out
}

// criterion is a criterion of a feature document.
type criterion struct {
	file  string
	line  int
	suite string
}

// criteriaIndex lists the criteria of every document by identifier.
func (c *checker) criteriaIndex() map[string]criterion {
	out := map[string]criterion{}
	for _, f := range c.files {
		d := c.docs[f]
		start, end, ok := d.section("Critérios de aceite")
		if !ok || !strings.HasPrefix(f, docsDir+"/") {
			continue
		}
		for i := start; i < end; i++ {
			id := criterionIDRe.FindStringSubmatch(d.lines[i])
			if id == nil {
				continue
			}
			key := "CA-" + id[1] + "-" + id[2]
			if _, dup := out[key]; dup {
				continue
			}
			suite := ""
			if m := criterionSuiteRe.FindStringSubmatch(d.lines[i]); m != nil {
				suite = m[1]
			}
			out[key] = criterion{file: f, line: i + 1, suite: suite}
		}
	}
	return out
}

// listedCriteria are the criteria the order document names under the given
// headings, ranges ("CA-COFRE-10 a CA-COFRE-17") expanded.
func (c *checker) listedCriteria(od *doc, headers ...string) map[string]bool {
	out := map[string]bool{}
	for _, h := range headers {
		start, end, ok := od.body(h)
		if !ok {
			continue
		}
		for i := start; i < end; i++ {
			maps.Copy(out, idsIn(od.lines[i]))
		}
	}
	return out
}

// deferredTargets reads, from the order document, the criterion that each
// table puts off and the rank of the milestone where it closes: a criterion is
// exempt from its marker only until that milestone is audited.
//
// In "Critérios em marcos posteriores" the cell is a list of entries
// separated by ";" outside parentheses; the criteria written before the
// `→ M<n>` of an entry (a range "CA-X-10 a CA-X-17" counts every item) close
// at that milestone (the latest, in "→ M5 e M6"; "→ Guardião" is the M7). What
// is inside parentheses is explanation: a criterion cited there is not
// deferred. In "Operador do PC no M7" every criterion of the table is for M7.
func (c *checker) deferredTargets(od *doc, stones map[int]*milestone) map[string]int {
	out := map[string]int{}
	put := func(ids map[string]bool, target int) {
		if stones[target] == nil {
			c.errf(orderPath, 1, ckCoverage, "adiamento para um marco que não está na tabela de marcos (rank %d): %v", target, slices.Sorted(maps.Keys(ids)))
			return
		}
		for id := range ids {
			out[id] = max(out[id], target)
		}
	}
	if start, end, ok := od.body(operatorHeader); ok {
		for i := start; i < end; i++ {
			if cells := splitRow(od.lines[i]); len(cells) >= 2 && strings.HasPrefix(od.lines[i], "| [") {
				put(idsIn(outsideParens(cells[1])), rank(7, false))
			}
		}
	}
	if start, end, ok := od.body(deferredHeader); ok {
		for i := start; i < end; i++ {
			cells := splitRow(od.lines[i])
			if len(cells) < 2 || !strings.HasPrefix(od.lines[i], "| [") {
				continue
			}
			for _, entry := range strings.Split(outsideParens(cells[1]), ";") {
				arrow := strings.Index(entry, "→")
				if arrow < 0 {
					continue
				}
				target := -1
				for _, m := range targetRe.FindAllStringSubmatch(entry[arrow:], -1) {
					n, _ := strconv.Atoi(m[1])
					target = max(target, rank(n, m[2] != ""))
				}
				if strings.Contains(entry[arrow:], "Guardião") {
					target = max(target, rank(7, false))
				}
				if target >= 0 {
					put(idsIn(entry[:arrow]), target)
				}
			}
		}
	}
	return out
}

// targetRe finds the milestone after an arrow: M5, M4+.
var targetRe = regexp.MustCompile(`\bM([0-9]+)(\+)?`)

// outsideParens drops what is inside parentheses (nested ones too), keeping
// ";" and the rest of the text of the cell.
func outsideParens(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '(':
			depth++
		case r == ')':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// idsIn lists the criteria named in the text, ranges expanded.
func idsIn(text string) map[string]bool {
	out := map[string]bool{}
	for _, m := range deferredRe.FindAllStringSubmatch(text, -1) {
		out[m[1]] = true
		if m[2] == "" {
			continue
		}
		out[m[2]] = true
		pre1, n1 := splitID(m[1])
		pre2, n2 := splitID(m[2])
		if pre1 != pre2 {
			continue
		}
		for k := n1; k <= n2; k++ {
			out[fmt.Sprintf("%s-%02d", pre1, k)] = true
		}
	}
	return out
}

// splitID cuts CA-X-07 into "CA-X" and 7.
func splitID(id string) (string, int) {
	i := strings.LastIndex(id, "-")
	n, _ := strconv.Atoi(id[i+1:])
	return id[:i], n
}

// doneSpikes are the numbered spikes the spikes table marks as done.
func (c *checker) doneSpikes(od *doc) map[int]bool {
	out := map[int]bool{}
	start, end, ok := od.body(spikesHeader)
	if !ok {
		return out
	}
	for i := start; i < end; i++ {
		if m := spikeRowRe.FindStringSubmatch(od.lines[i]); m != nil && strings.Contains(od.lines[i], "**feito**") {
			n, _ := strconv.Atoi(m[1])
			out[n] = true
		}
	}
	return out
}

// Checks 11 to 13: audit dates of the milestones, coverage of the criteria by
// the markers in the tests, and milestone comments that fell behind.
func (c *checker) checkMilestones() {
	od := c.docs[orderPath]
	if od == nil {
		return
	}
	stones := c.loadMilestones(od)
	if stones == nil {
		return
	}
	scan := scanGo(c.fsys)
	crits := c.criteriaIndex()

	// The documents belong to M<n>, never to M4+: their rank is even.
	milestoneOf := func(id string) (int, bool) {
		cr, ok := crits[id]
		if !ok {
			return 0, false
		}
		p, ok := c.place[cr.file]
		return rank(p.milestone, false), ok
	}
	audited := func(r int) bool { s := stones[r]; return s != nil && s.audit != "" }
	nums := make([]int, 0, len(stones))
	for r := range stones {
		nums = append(nums, r)
	}
	sort.Ints(nums)

	// Check 11: audits in order (M4+ between M4 and M5), and no code of a
	// milestone before the audit of the one before it.
	firstMarker := map[int]codeMarker{}
	early := c.listedCriteria(od, earlyHeader)
	for _, mk := range scan.markers {
		if n, ok := milestoneOf(mk.ID); ok && !early[mk.ID] {
			if _, seen := firstMarker[n]; !seen {
				firstMarker[n] = mk
			}
		}
	}
	for _, m := range nums {
		if audited(m) {
			for _, k := range nums {
				if k < m && !audited(k) {
					c.errf(orderPath, stones[m].line, ckAudit, "%s tem auditoria e %s não: os marcos são auditados em ordem", stones[m].name(), stones[k].name())
					break
				}
			}
			continue
		}
		for _, n := range nums {
			if mk, ok := firstMarker[n]; ok && n > m {
				c.errf(orderPath, stones[m].line, ckAudit, "%s não tem data de auditoria e há código do %s (%s:%d, %s): audite o %s e registre a data antes", stones[m].name(), stones[n].name(), mk.File, mk.Line, mk.ID, stones[m].name())
				break
			}
		}
	}

	// Check 12: every criterion of an audited milestone has its test.
	tested := map[string]bool{}
	for _, mk := range scan.markers {
		cr, known := crits[mk.ID]
		if !known {
			c.errf(mk.File, mk.Line, ckCoverage, "o marcador %s não nomeia nenhum critério de aceite dos documentos", mk.ID)
			continue
		}
		if !mk.Test {
			continue
		}
		switch {
		case !mk.Placed:
			c.errf(mk.File, mk.Line, ckCoverage, "o marcador %s não está logo acima de um `func Test…` ou `t.Run(`: junte-o ao comentário do teste", mk.ID)
		case cr.suite != "" && mk.Suite != cr.suite:
			c.errf(mk.File, mk.Line, ckCoverage, "o marcador %s está num teste da suíte %s e o critério é *%s*: use `// Part of %s` ou mude o critério (%s:%d)", mk.ID, mk.Suite, cr.suite, mk.ID, rel(cr.file), cr.line)
		default:
			tested[mk.ID] = true
		}
	}
	deferred := c.deferredTargets(od, stones)
	ids := make([]string, 0, len(crits))
	for id := range crits {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		cr := crits[id]
		// A document outside the map (a reference one) counts from M0.
		p := c.place[cr.file]
		if !audited(rank(p.milestone, false)) || cr.suite == "pesada" || tested[id] {
			continue
		}
		if target, ok := deferred[id]; ok && !audited(target) {
			continue // put off, and the milestone where it closes is still open
		}
		c.errf(cr.file, cr.line, ckCoverage, "%s (M%d, *%s*) não tem teste marcado: ponha `// %s` acima do teste (`Part of` não basta), ou ponha o critério em %q de %s com o marco em que fecha, que ainda não foi auditado", id, p.milestone, cr.suite, id, deferredHeader, rel(orderPath))
	}

	// Check 3, in code: the anchor of a document in a comment or a config file.
	for _, r := range append(scan.refs, scanConfigRefs(c.fsys)...) {
		d := c.load(r.Doc)
		switch {
		case d == nil:
			c.errf(r.File, r.Line, ckLink, "%s não existe", r.Doc)
		case !d.anchorSet()[r.Anchor]:
			c.errf(r.File, r.Line, ckLink, "o título #%s não existe em %s", r.Anchor, r.Doc)
		}
	}

	// Check 13: comments that wait for a milestone or a spike already closed.
	done := c.doneSpikes(od)
	for _, g := range scan.comments {
		for _, re := range staleMilestoneRes {
			for _, loc := range re.FindAllStringSubmatchIndex(g.Text, -1) {
				n, _ := strconv.Atoi(g.Text[loc[2]:loc[3]])
				// "M4+" is the phase after M4, not M4 itself, when the table
				// has that row; without it the "+" is only punctuation.
				r, end := rank(n, false), loc[3]
				if strings.HasPrefix(g.Text[loc[3]:], "+") && stones[rank(n, true)] != nil {
					r, end = rank(n, true), loc[3]+1
				}
				if audited(r) {
					c.errf(g.File, g.lineAt(loc[0]), ckStale, "%q: o %s já foi auditado (%s); atualize o comentário", g.Text[loc[0]:end], stones[r].name(), stones[r].audit)
				}
			}
		}
		for _, loc := range staleSpikeRe.FindAllStringSubmatchIndex(g.Text, -1) {
			if n, _ := strconv.Atoi(g.Text[loc[2]:loc[3]]); done[n] {
				c.errf(g.File, g.lineAt(loc[0]), ckStale, "%q: o spike %d está feito na tabela de spikes; atualize o comentário", g.Text[loc[0]:loc[1]], n)
			}
		}
	}
}
