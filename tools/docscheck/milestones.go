package main

import (
	"fmt"
	"regexp"
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
	milestoneRowRe = regexp.MustCompile(`^\| M([0-9]+) \|`)
	dateRe         = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	idPattern      = `CA-[A-Z0-9]+(?:-[A-Z0-9]+)*-[0-9]{2}`
	// deferredRe finds `CA-X-10` and the range `CA-X-10 a CA-X-17`.
	deferredRe = regexp.MustCompile(`(` + idPattern + `)(?: a (` + idPattern + `))?`)
	spikeRowRe = regexp.MustCompile(`(?i)\(spike ([0-9]+)\)`)

	// Stale milestone comments (verificacao-de-docs.md, check 13). The words
	// are case-insensitive; the milestone is an upper-case M and a digit.
	staleMilestoneRes = []*regexp.Regexp{
		// the verb arrives/comes/lands, then "in M<n>" a few words later
		regexp.MustCompile(`\b(?i:arrives?|comes?|lands?)\b[^.;:]{0,40}?\b(?i:in|at|with)\s+(?:the\s+)?M([0-9])\b`),
		// "until M<n>"
		regexp.MustCompile(`\b(?i:until|till)\s+(?:the\s+)?M([0-9])\b`),
		// "waits for M<n>", "pending M<n>", "deferred to M<n>"
		regexp.MustCompile(`\b(?i:waits?\s+for|waiting\s+for|pending|deferred\s+(?:to|until)|postponed\s+(?:to|until)|left\s+for)\s+(?:the\s+)?M([0-9])\b`),
		// "chega no M<n>", "vem com o M<n>", "fica para o M<n>"
		regexp.MustCompile(`\b(?i:chegam?|vem|ficam?|esperam?|aguardam?)\b[^.;:]{0,40}?\b(?i:no|com o|para o|até o|do)\s+M([0-9])\b`),
		// "até o M<n>", "até M<n>"
		regexp.MustCompile(`\b(?i:até)\s+(?:o\s+)?M([0-9])\b`),
	}
	// "waits for spike <n>", "pending spike <n>", "aguarda o spike <n>"
	staleSpikeRe = regexp.MustCompile(`\b(?i:waits?\s+for|waiting\s+for|until|pending|espera(?:ndo)?|aguarda(?:ndo)?)\s+(?:(?i:the|o)\s+)?(?i:spike)\s+([0-9]+)\b`)
)

// milestone is a row of the table of milestones.
type milestone struct {
	n     int
	audit string // AAAA-MM-DD, or "" while the milestone is not audited
	line  int
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
// validates the dates. It returns nil when the table cannot be read.
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
			c.errf(orderPath, i+1, ckAudit, "linha do M%s sem a coluna %q", m[1], auditColumn)
			continue
		}
		n, _ := strconv.Atoi(m[1])
		ms := &milestone{n: n, audit: cells[col], line: i + 1}
		out[n] = ms
		if ms.audit == "" {
			continue
		}
		d, err := time.Parse(time.DateOnly, ms.audit)
		switch {
		case !dateRe.MatchString(ms.audit) || err != nil:
			c.errf(orderPath, ms.line, ckAudit, "M%d: auditoria %q inválida; use AAAA-MM-DD ou deixe vazia", n, ms.audit)
		case d.Format(time.DateOnly) > c.today.UTC().Format(time.DateOnly):
			c.errf(orderPath, ms.line, ckAudit, "M%d: auditoria %s está no futuro (hoje é %s em UTC)", n, ms.audit, c.today.UTC().Format(time.DateOnly))
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
			for _, m := range deferredRe.FindAllStringSubmatch(od.lines[i], -1) {
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

	milestoneOf := func(id string) (int, bool) {
		cr, ok := crits[id]
		if !ok {
			return 0, false
		}
		p, ok := c.place[cr.file]
		return p.milestone, ok
	}
	audited := func(n int) bool { s := stones[n]; return s != nil && s.audit != "" }
	nums := make([]int, 0, len(stones))
	for n := range stones {
		nums = append(nums, n)
	}
	sort.Ints(nums)

	// Check 11: audits in order, and no code of a milestone before the
	// audit of the one before it.
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
					c.errf(orderPath, stones[m].line, ckAudit, "M%d tem auditoria e M%d não: os marcos são auditados em ordem", m, k)
					break
				}
			}
			continue
		}
		for _, n := range nums {
			if mk, ok := firstMarker[n]; ok && n > m {
				c.errf(orderPath, stones[m].line, ckAudit, "M%d não tem data de auditoria e há código do M%d (%s:%d, %s): audite o M%d e registre a data antes", m, n, mk.File, mk.Line, mk.ID, m)
				break
			}
		}
	}

	// Check 12: every criterion of an audited milestone has its test.
	tested := map[string]bool{}
	for _, mk := range scan.markers {
		if mk.Test {
			tested[mk.ID] = true
		}
		if _, ok := crits[mk.ID]; !ok {
			c.errf(mk.File, mk.Line, ckCoverage, "o marcador %s não nomeia nenhum critério de aceite dos documentos", mk.ID)
		}
	}
	deferred := c.listedCriteria(od, deferredHeader, operatorHeader)
	ids := make([]string, 0, len(crits))
	for id := range crits {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		cr := crits[id]
		p, ok := c.place[cr.file]
		if !ok || !audited(p.milestone) || cr.suite == "pesada" || deferred[id] || tested[id] {
			continue
		}
		c.errf(cr.file, cr.line, ckCoverage, "%s (M%d, *%s*) não tem teste marcado: ponha `// %s` acima do teste, ou liste o critério em %q de %s", id, p.milestone, cr.suite, id, deferredHeader, rel(orderPath))
	}

	// Check 13: comments that wait for a milestone or a spike already closed.
	done := c.doneSpikes(od)
	for _, g := range scan.comments {
		for _, re := range staleMilestoneRes {
			for _, loc := range re.FindAllStringSubmatchIndex(g.Text, -1) {
				n, _ := strconv.Atoi(g.Text[loc[2]:loc[3]])
				if audited(n) {
					c.errf(g.File, g.lineAt(loc[0]), ckStale, "%q: o M%d já foi auditado (%s); atualize o comentário", g.Text[loc[0]:loc[1]], n, stones[n].audit)
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
