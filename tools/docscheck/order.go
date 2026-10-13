package main

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var mapRowRe = regexp.MustCompile(`^\| M([0-9]+) \| ([0-9]+) \| (.*)\|\s*$`)

type placement struct {
	milestone, stage, line int
}

// rel shows a docs path relative to docs/.
func rel(p string) string { return strings.TrimPrefix(p, docsDir+"/") }

func (p placement) String() string {
	return "M" + strconv.Itoa(p.milestone) + ", etapa " + strconv.Itoa(p.stage)
}

// Check 10: "Depende de" only backwards, no cycles, stages coherent with the
// map of ordem-de-implementacao.md.
func (c *checker) checkOrder() {
	od := c.docs[orderPath]
	if od == nil {
		return
	}
	start, end, ok := od.section(orderMapHeader)
	if !ok {
		c.errf(orderPath, 1, ckOrder, "seção %q não encontrada", orderMapHeader)
		return
	}
	place := map[string]placement{}
	c.place = place
	for i := start; i < end; i++ {
		m := mapRowRe.FindStringSubmatch(od.lines[i])
		if m == nil {
			continue
		}
		ms, _ := strconv.Atoi(m[1])
		st, _ := strconv.Atoi(m[2])
		for _, l := range od.linksIn(i, i+1) {
			r := resolve(od, l.target)
			if r.file == "" {
				continue
			}
			if prev, dup := place[r.file]; dup {
				c.errf(orderPath, i+1, ckOrder, "%s aparece mais de uma vez no mapa (também na linha %d)", rel(r.file), prev.line)
				continue
			}
			place[r.file] = placement{ms, st, i + 1}
			if d := c.docs[r.file]; d != nil && !d.hasSection("Objetivo") {
				c.errf(orderPath, i+1, ckOrder, "%s está no mapa e não é documento de funcionalidade (sem a seção Objetivo)", rel(r.file))
			}
		}
	}

	deps := map[string][]string{}
	var features []string
	for _, f := range c.files {
		d := c.docs[f]
		if !strings.HasPrefix(f, docsDir+"/") || !d.hasSection("Objetivo") {
			continue
		}
		features = append(features, f)
		me, inMap := place[f]
		if !inMap {
			c.errf(f, 1, ckOrder, "documento de funcionalidade fora do mapa de %s", rel(orderPath))
			continue
		}
		s, e, ok := d.section("Depende de")
		if !ok {
			continue
		}
		for _, l := range d.linksIn(s, e) {
			r := resolve(d, l.target)
			dep, isFeature := place[r.file]
			if r.file == "" || r.file == f || !isFeature {
				continue // reference documents do not enter the graph
			}
			deps[f] = append(deps[f], r.file)
			switch {
			case dep.milestone > me.milestone:
				c.errf(f, l.line, ckOrder, "%s (%s) depende de %s (%s), de marco posterior", rel(f), me, rel(r.file), dep)
			case dep.milestone == me.milestone && dep.stage >= me.stage:
				c.errf(f, l.line, ckOrder, "%s (%s) depende de %s (%s): a etapa de quem depende deve ser maior", rel(f), me, rel(r.file), dep)
			}
		}
	}
	c.findCycles(features, deps)
}

func (c *checker) findCycles(nodes []string, deps map[string][]string) {
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var stack []string
	reported := map[string]bool{}
	var visit func(string)
	visit = func(u string) {
		color[u] = grey
		stack = append(stack, u)
		for _, v := range deps[u] {
			switch color[v] {
			case grey:
				i := 0
				for stack[i] != v {
					i++
				}
				cycle := append(append([]string(nil), stack[i:]...), v)
				names := make([]string, len(cycle))
				for k, p := range cycle {
					names[k] = rel(p)
				}
				key := canonicalCycle(names[:len(names)-1])
				if !reported[key] {
					reported[key] = true
					c.errf(v, 1, ckOrder, "ciclo em \"Depende de\": %s", strings.Join(names, " → "))
				}
			case white:
				visit(v)
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = black
	}
	sort.Strings(nodes)
	for _, n := range nodes {
		if color[n] == white {
			visit(n)
		}
	}
}

// canonicalCycle identifies a cycle independently of where it starts.
func canonicalCycle(names []string) string {
	min := 0
	for i := range names {
		if names[i] < names[min] {
			min = i
		}
	}
	return strings.Join(append(append([]string(nil), names[min:]...), names[:min]...), "|")
}
