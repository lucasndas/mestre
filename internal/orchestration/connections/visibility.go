package connections

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
)

// ScrollView is a scroll an agent can see, with what it is connected to among
// the things that agent can see.
type ScrollView struct {
	ID, Name    string
	Stack       string
	Connections []string
}

// Edge is a connection the agent can see, by the names of its ends.
type Edge struct{ From, To string }

// View is what `mestre list` shows an agent: its own name, the characters
// visible to it and the scrolls visible to it with their connections.
type View struct {
	Self       characters.Character
	Characters []characters.Character
	Scrolls    []ScrollView
	// Edges are the connections of the agent with characters and the ones of
	// the scrolls it sees.
	Edges []Edge
}

// Visible lists the characters an agent can talk to, by name
// (docs/orquestracao/conexoes.md#visibilidade): the ones connected to it; for
// the Grand Master also every table master of the workspace; for a table
// master also the Grand Master and the whole party of its table. Nothing
// crosses tables except the table master and the Grand Master.
func (s *Service) Visible(ctx context.Context, x characters.Character) ([]characters.Character, error) {
	seen := map[string]bool{x.ID: true}
	var out []characters.Character
	keep := func(c characters.Character) {
		if !seen[c.ID] && c.State.Alive() && c.Workspace == x.Workspace {
			seen[c.ID] = true
			out = append(out, c)
		}
	}
	cs, err := s.store.Of(ctx, Char(x.ID))
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		if other := c.Other(Char(x.ID)); other.Kind == KindCharacter {
			ch, err := s.o.Registry.Get(ctx, other.ID)
			switch {
			case errors.Is(err, characters.ErrNoCharacter):
				continue
			case err != nil:
				return nil, err
			}
			keep(ch)
		}
	}
	switch x.Role {
	case characters.RoleGrandMaster:
		all, err := s.o.Registry.AliveOf(ctx, x.Workspace)
		if err != nil {
			return nil, err
		}
		for _, c := range all {
			if c.Role == characters.RoleTableMaster {
				keep(c)
			}
		}
	case characters.RoleTableMaster:
		if gm, ok, err := s.o.Registry.GrandMaster(ctx, x.Workspace); err != nil {
			return nil, err
		} else if ok {
			keep(gm)
		}
		all, err := s.o.Registry.AliveOf(ctx, x.Workspace)
		if err != nil {
			return nil, err
		}
		for _, c := range all {
			if c.Table == x.Table && c.Role == characters.RoleCharacter {
				keep(c)
			}
		}
	}
	slices.SortFunc(out, func(a, b characters.Character) int {
		return strings.Compare(nameKeyOf(a.Name), nameKeyOf(b.Name))
	})
	return out, nil
}

func nameKeyOf(name string) string { return strings.ToLower(name) }

// View computes, at the moment of the command, what an agent sees.
func (s *Service) View(ctx context.Context, agentID string) (View, error) {
	x, err := s.o.Registry.Get(ctx, agentID)
	if err != nil {
		return View{}, err
	}
	v := View{Self: x}
	if v.Characters, err = s.Visible(ctx, x); err != nil {
		return View{}, err
	}
	g, err := s.scrollGraph(ctx, x)
	if err != nil {
		return View{}, err
	}
	names := map[string]string{x.ID: x.Name}
	for _, c := range v.Characters {
		names[c.ID] = c.Name
	}
	for _, id := range g.visible() {
		sc, ok, err := s.o.Scrolls.Get(ctx, Scroll(id))
		if err != nil {
			return View{}, err
		}
		if !ok {
			continue
		}
		sv := ScrollView{ID: id, Name: sc.Name, Stack: sc.Stack}
		for _, other := range g.adj[id] {
			if o, ok, err := s.o.Scrolls.Get(ctx, Scroll(other)); err != nil {
				return View{}, err
			} else if ok {
				sv.Connections = append(sv.Connections, o.Name)
			}
		}
		for _, cid := range g.chars[id] {
			if n, ok := names[cid]; ok {
				sv.Connections = append(sv.Connections, n)
			}
		}
		slices.SortFunc(sv.Connections, func(a, b string) int { return strings.Compare(nameKeyOf(a), nameKeyOf(b)) })
		v.Scrolls = append(v.Scrolls, sv)
	}
	slices.SortFunc(v.Scrolls, func(a, b ScrollView) int { return strings.Compare(nameKeyOf(a.Name), nameKeyOf(b.Name)) })
	own, err := s.store.Of(ctx, Char(x.ID))
	if err != nil {
		return View{}, err
	}
	for _, c := range own {
		if other := c.Other(Char(x.ID)); other.Kind == KindCharacter {
			if n, ok := names[other.ID]; ok {
				v.Edges = append(v.Edges, Edge{From: x.Name, To: n})
			}
		}
	}
	seen := map[[2]string]bool{}
	for _, sc := range v.Scrolls {
		for _, to := range sc.Connections {
			k := [2]string{sc.Name, to}
			if seen[[2]string{to, sc.Name}] || seen[k] {
				continue
			}
			seen[k] = true
			v.Edges = append(v.Edges, Edge{From: sc.Name, To: to})
		}
	}
	slices.SortFunc(v.Edges, func(a, b Edge) int {
		return strings.Compare(nameKeyOf(a.From)+"\x00"+nameKeyOf(a.To), nameKeyOf(b.From)+"\x00"+nameKeyOf(b.To))
	})
	return v, nil
}

// CanSeeScroll says whether an agent may read and write a scroll: the ones
// connected to it, and the ones that can be reached from them through
// connections between scrolls.
func (s *Service) CanSeeScroll(ctx context.Context, agentID, scroll string) (bool, error) {
	x, err := s.o.Registry.Get(ctx, agentID)
	if err != nil {
		return false, err
	}
	g, err := s.scrollGraph(ctx, x)
	if err != nil {
		return false, err
	}
	return g.reached[scroll], nil
}

// scrollGraph is the scrolls of the table of an agent, with the connections
// between them (a tome counted as its scrolls) and which of them the agent
// reaches.
type scrollGraph struct {
	// adj are the scrolls each scroll is connected to.
	adj map[string][]string
	// chars are the characters connected to each scroll, tomes expanded.
	chars   map[string][]string
	reached map[string]bool
}

func (g *scrollGraph) visible() []string {
	out := make([]string, 0, len(g.reached))
	for id := range g.reached {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

func (s *Service) scrollGraph(ctx context.Context, x characters.Character) (*scrollGraph, error) {
	g := &scrollGraph{adj: map[string][]string{}, chars: map[string][]string{}, reached: map[string]bool{}}
	if s.o.Scrolls == nil || x.Table == "" {
		return g, nil
	}
	cs, err := s.store.OfTable(ctx, x.Workspace, x.Table)
	if err != nil {
		return nil, err
	}
	members := map[string][]string{}
	expand := func(r Ref) ([]string, error) {
		switch r.Kind {
		case KindScroll:
			return []string{r.ID}, nil
		case KindTome:
			if m, ok := members[r.ID]; ok {
				return m, nil
			}
			m, err := s.o.Scrolls.Members(ctx, r.ID)
			members[r.ID] = m
			return m, err
		}
		return nil, nil
	}
	var queue []string
	reach := func(id string) {
		if !g.reached[id] {
			g.reached[id] = true
			queue = append(queue, id)
		}
	}
	for _, c := range cs {
		a, b := c.A, c.B
		switch {
		case a.Kind == KindCharacter && b.Kind == KindCharacter:
		case a.Kind == KindCharacter || b.Kind == KindCharacter:
			who, what := a, b
			if b.Kind == KindCharacter {
				who, what = b, a
			}
			ids, err := expand(what)
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				g.chars[id] = append(g.chars[id], who.ID)
				if who.ID == x.ID {
					reach(id)
				}
			}
		default:
			as, err := expand(a)
			if err != nil {
				return nil, err
			}
			bs, err := expand(b)
			if err != nil {
				return nil, err
			}
			for _, sa := range as {
				for _, sb := range bs {
					if sa != sb {
						g.adj[sa] = append(g.adj[sa], sb)
						g.adj[sb] = append(g.adj[sb], sa)
					}
				}
			}
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, n := range g.adj[id] {
			reach(n)
		}
	}
	for id, ns := range g.adj {
		slices.Sort(ns)
		g.adj[id] = slices.Compact(ns)
	}
	return g, nil
}

// Target finds the character an agent addresses by name (`ask`, `check`,
// `wait`, `ask --raw`). Only a visible character can be addressed. When the
// name belongs to a character of the agent's table that is not visible to it,
// the answer is a NotConnectedError (exit 3, not_connected); when there is no
// such character for the agent, ErrNotFound (exit 4, not_visible). The Grand
// Master, which has no table, names the table of its target.
func (s *Service) Target(ctx context.Context, agentID, table, name string) (characters.Character, error) {
	x, err := s.o.Registry.Get(ctx, agentID)
	if err != nil {
		return characters.Character{}, err
	}
	vis, err := s.Visible(ctx, x)
	if err != nil {
		return characters.Character{}, err
	}
	var found []characters.Character
	for _, c := range vis {
		if strings.EqualFold(c.Name, name) && (table == "" || c.Table == table) {
			found = append(found, c)
		}
	}
	switch {
	case len(found) == 1:
		return found[0], nil
	case len(found) > 1:
		return characters.Character{}, ErrTableRequired
	}
	if x.Role == characters.RoleGrandMaster && table == "" {
		return characters.Character{}, notFound(name)
	}
	tbl := x.Table
	if x.Role == characters.RoleGrandMaster {
		tbl = table
	}
	if c, err := s.o.Registry.ByName(ctx, x.Workspace, tbl, name); err == nil && c.ID != x.ID {
		return characters.Character{}, &NotConnectedError{Name: c.Name}
	}
	return characters.Character{}, notFound(name)
}
