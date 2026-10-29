package waits

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
)

// edges is the graph "who waits for whom"
// (docs/orquestracao/esperas-e-caixa-de-entrada.md#detecção-de-impasse): the
// waits for characters, the asks someone is blocked in, and the waits for
// steps, pointing to the character responsible for the step.
func (s *Service) edges(ctx context.Context) (map[string][]string, error) {
	g := map[string][]string{}
	add := func(from, to string) {
		if to != "" && from != to && !slices.Contains(g[from], to) {
			g[from] = append(g[from], to)
		}
	}
	ws, err := s.st.ActiveWaits(ctx)
	if err != nil {
		return nil, err
	}
	for _, w := range ws {
		for _, t := range w.Pending() {
			switch w.Kind {
			case KindCharacter:
				add(w.Waiter, t.ID)
			case KindSteps:
				if s.o.Quests == nil {
					continue
				}
				who, err := s.o.Quests.Responsible(ctx, t.ID)
				if err != nil {
					return nil, err
				}
				add(w.Waiter, who)
			}
		}
	}
	asks, err := s.st.OpenAsks(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range asks {
		if a.Blocking && a.TimedOutAt.IsZero() {
			add(a.Asker, a.Target)
		}
	}
	return g, nil
}

// findCycle finds the shortest ring of waits that goes through start: the
// characters in order, starting at start, or nil.
func findCycle(g map[string][]string, start string) []string {
	parent := map[string]string{}
	queue := []string{start}
	seen := map[string]bool{start: true}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, next := range g[n] {
			if next == start {
				var path []string
				for at := n; ; at = parent[at] {
					path = append(path, at)
					if at == start {
						break
					}
				}
				slices.Reverse(path)
				return path
			}
			if !seen[next] {
				seen[next] = true
				parent[next] = n
				queue = append(queue, next)
			}
		}
	}
	return nil
}

func cycleKey(ids []string) string {
	k := slices.Clone(ids)
	slices.Sort(k)
	return strings.Join(k, ",")
}

// describe writes a cycle with the names of the characters, starting at the
// first name in alphabetical order so the same ring is always written the same
// way: "A → B → A".
func (s *Service) describe(ctx context.Context, ids []string) (text string, tables []string, workspace string) {
	type node struct{ name, table string }
	nodes := make([]node, len(ids))
	for i, id := range ids {
		nodes[i] = node{name: id}
		if c, err := s.o.Registry.Get(ctx, id); err == nil {
			nodes[i] = node{c.Name, c.Table}
			workspace = c.Workspace
		}
	}
	first := 0
	for i := range nodes {
		if strings.ToLower(nodes[i].name) < strings.ToLower(nodes[first].name) {
			first = i
		}
	}
	names := make([]string, 0, len(nodes)+1)
	for i := range nodes {
		n := nodes[(first+i)%len(nodes)]
		names = append(names, n.name)
		if !slices.Contains(tables, n.table) {
			tables = append(tables, n.table)
		}
	}
	names = append(names, names[0])
	return strings.Join(names, " → "), tables, workspace
}

// detect looks for a ring through a character that just got a new arrow, and
// warns of it once: the table master (the Grand Master, when the ring crosses
// tables) and the users, with the ring written out. Nothing is cancelled.
func (s *Service) detect(ctx context.Context, from string) {
	g, err := s.edges(ctx)
	if err != nil {
		s.log.Warn("waits: the graph of waits was not read", "err", err)
		return
	}
	ring := findCycle(g, from)
	if ring == nil {
		return
	}
	text, tables, workspace := s.describe(ctx, ring)
	fresh, err := s.st.AddCycle(ctx, Cycle{Key: cycleKey(ring), Workspace: workspace, Text: text, CreatedAt: s.clk.Now()})
	if err != nil {
		s.log.Warn("waits: a deadlock was not recorded", "err", err)
		return
	}
	if !fresh {
		return
	}
	body := "Impasse entre personagens: " + text + ". Cada um espera pelo próximo; nada foi cancelado. Troque um deles, responda por ele ou avise os usuários."
	msg := i18n.M("character.deadlock", "ring", text)
	to := characters.Recipient{Kind: characters.ToTableMaster}
	table := tables[0]
	if len(tables) > 1 {
		to, table = characters.Recipient{Kind: characters.ToGrandMaster}, ""
	}
	for _, rcpt := range []characters.Recipient{to, {Kind: characters.ToOwner}} {
		if err := s.o.Notifier.Notify(ctx, characters.Notice{Kind: characters.NoticeDeadlock, To: rcpt, Workspace: workspace, Table: table, Text: body, Msg: msg}); err != nil {
			s.log.Warn("waits: the warning of a deadlock was not given", "err", err)
		}
	}
}

// Recheck looks again for a ring through a waiter whose arrows changed without
// a new wait (a step that got its character): same warning, once per ring.
func (s *Service) Recheck(ctx context.Context, waiter string) { s.detect(ctx, waiter) }

// StepAssigned says a step got its character: the characters that wait for the
// step now wait for that one, which may close a ring.
func (s *Service) StepAssigned(ctx context.Context, stepID string) {
	ws, err := s.st.ActiveWaits(ctx)
	if err != nil {
		s.log.Warn("waits: the waits for a step were not read", "step", stepID, "err", err)
		return
	}
	for _, w := range ws {
		if w.Kind != KindSteps {
			continue
		}
		for _, t := range w.Pending() {
			if t.ID == stepID {
				s.Recheck(ctx, w.Waiter)
				break
			}
		}
	}
}

// refreshCycles ends the records of the rings that are gone, so a ring that
// comes back is warned about again.
func (s *Service) refreshCycles(ctx context.Context) {
	active, err := s.st.ActiveCycles(ctx)
	if err != nil || len(active) == 0 {
		return
	}
	g, err := s.edges(ctx)
	if err != nil {
		return
	}
	present := map[string]bool{}
	for n := range g {
		if ring := findCycle(g, n); ring != nil {
			present[cycleKey(ring)] = true
		}
	}
	for _, c := range active {
		if !present[c.Key] {
			if err := s.st.EndCycle(ctx, c.Key, s.clk.Now()); err != nil {
				s.log.Warn("waits: the end of a deadlock was not recorded", "err", err)
			}
		}
	}
}

// Cycles lists the deadlocks that last, for the canvas to highlight and for
// `mestre waits`.
func (s *Service) Cycles(ctx context.Context) ([]Cycle, error) { return s.st.ActiveCycles(ctx) }

// slotRequest reads the request that waits for a vacancy.
func slotRequest(w characters.SlotWait) (characters.CreateRequest, bool) {
	var req characters.CreateRequest
	if err := json.Unmarshal(w.Request, &req); err != nil {
		return characters.CreateRequest{}, false
	}
	return req, true
}

// Row is one line of the map of waits (`mestre waits`).
type Row struct {
	ID        string
	Waiter    string
	Target    string
	Kind      string
	Reason    string
	Since     time.Time
	TimeoutAt time.Time
}

// WaitMap is the map of waits: a row per wait and target, and the deadlocks.
type WaitMap struct {
	Rows   []Row
	Cycles []Cycle
}

// Map lists the waits of a table (all of the workspace for table "" with all
// set), the asks someone is blocked in, and the requests that wait for a
// vacancy, with the deadlocks.
func (s *Service) Map(ctx context.Context, workspace, table string, all bool) (WaitMap, error) {
	name := func(id string) string {
		if c, err := s.o.Registry.Get(ctx, id); err == nil {
			return c.Name
		}
		return id
	}
	var m WaitMap
	ws, err := s.st.ActiveWaits(ctx)
	if err != nil {
		return m, err
	}
	for _, w := range ws {
		if w.Workspace != workspace || (!all && w.Table != table) {
			continue
		}
		targets := w.Pending()
		if len(targets) == 0 {
			targets = []Target{{Name: w.Reason}}
		}
		for _, t := range targets {
			m.Rows = append(m.Rows, Row{ID: w.ID, Waiter: name(w.Waiter), Target: t.Name, Kind: string(w.Kind), Reason: w.Reason, Since: w.CreatedAt, TimeoutAt: w.TimeoutAt})
		}
	}
	asks, err := s.st.OpenAsks(ctx)
	if err != nil {
		return m, err
	}
	for _, a := range asks {
		if !a.Blocking || a.Workspace != workspace || (!all && a.Table != table) {
			continue
		}
		m.Rows = append(m.Rows, Row{ID: a.ID, Waiter: name(a.Asker), Target: name(a.Target), Kind: "ask", Reason: name(a.Target), Since: a.CreatedAt})
	}
	slots, err := s.o.Registry.ActiveSlotWaits(ctx)
	if err != nil {
		return m, err
	}
	for _, sw := range slots {
		req, ok := slotRequest(sw)
		if !ok || req.Workspace != workspace || (!all && req.Table != table) {
			continue
		}
		who := "pedido de criação"
		if req.Creator.Kind == characters.CreatedByCharacter {
			who = name(req.Creator.ID)
		}
		m.Rows = append(m.Rows, Row{ID: sw.ID, Waiter: who, Target: fmt.Sprintf("vaga (%s)", sw.Limit), Kind: string(KindSlot), Reason: "vaga", Since: sw.CreatedAt, TimeoutAt: sw.ExpiresAt})
	}
	cs, err := s.st.ActiveCycles(ctx)
	if err != nil {
		return m, err
	}
	for _, c := range cs {
		if c.Workspace == workspace {
			m.Cycles = append(m.Cycles, c)
		}
	}
	return m, nil
}
