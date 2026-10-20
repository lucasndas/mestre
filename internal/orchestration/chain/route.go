package chain

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/providers"
)

// hop is a level of the path of a request with the character that holds it.
type hop struct {
	level Level
	char  characters.Character
}

// path is the chain of a request, lowest level first: the superior of who
// asked, the superior of that one, … up to the people. It is read from the
// registry each time, so a creator that ended or was replaced is already
// out of it (docs/orquestracao/cadeia-de-aprovacao.md#superior).
func (s *Service) path(ctx context.Context, r Request) ([]hop, error) {
	var out []hop
	add := func(c characters.Character) hop {
		h := hop{char: c, level: Level{Agent: c.ID, Name: c.Name}}
		switch c.Role {
		case characters.RoleTableMaster:
			h.level.Kind = LevelMesa
		case characters.RoleGrandMaster:
			h.level.Kind = LevelGrandMaster
		default:
			h.level.Kind = LevelAgent
		}
		out = append(out, h)
		return h
	}
	people := hop{level: Level{Kind: LevelPeople}}
	if r.Requester == "" {
		// A request with no requester (a PR, an extra cost, a proposal) is
		// born at the Grão-Mestre or above.
		gm, ok, err := s.o.Characters.GrandMaster(ctx, r.Workspace)
		if err != nil {
			return nil, err
		}
		if ok {
			add(gm)
		}
		return append(out, people), nil
	}
	cur := r.Requester
	for range 16 {
		sup, err := s.o.Characters.Superior(ctx, cur)
		if err != nil {
			return nil, err
		}
		if sup.People {
			return append(out, people), nil
		}
		c, err := s.o.Characters.Get(ctx, sup.ID)
		if err != nil {
			return nil, err
		}
		add(c)
		cur = c.ID
	}
	return append(out, people), nil
}

// available says whether a character can take a request now: stopped,
// restarting (starting or interrupted) and stuck characters cannot
// (docs/orquestracao/cadeia-de-aprovacao.md#subida).
func available(st characters.State) bool {
	switch st {
	case characters.Stopped, characters.Stopping, characters.Interrupted, characters.Starting, characters.Stuck:
		return false
	}
	return true
}

// verdict is whether a level can decide a request, and why not.
type verdict struct {
	ok  bool
	why string
	// cited says the reason is one the next level and the people should read
	// (a condition of the table, a guardrail).
	cited bool
}

func yes() verdict { return verdict{ok: true} }

func no(cited bool, format string, a ...any) verdict {
	return verdict{why: fmt.Sprintf(format, a...), cited: cited}
}

// can says whether the level, held by h, can decide r under the settings: the
// ceiling of the level, the conditions of the table and the guardrails of the
// engine that do not need the rules of the approver
// (docs/orquestracao/cadeia-de-aprovacao.md).
func (s *Service) can(ctx context.Context, h hop, r Request, st Settings) verdict {
	if h.level.Kind == LevelPeople {
		return yes()
	}
	c := h.char
	switch {
	case c.ID == r.Requester:
		return no(true, "%s é quem pediu: ninguém decide em causa própria", c.Name)
	case c.ID == r.Designer && r.Designer != "":
		return no(true, "%s desenhou a missão: não decide os pontos de aprovação dela", c.Name)
	case c.ID == r.Proposer && r.Proposer != "":
		return no(true, "%s fez a proposta que o pedido serve: ninguém decide em causa própria", c.Name)
	case !available(c.State):
		return no(false, "%s está parado (%s)", c.Name, c.State)
	}
	if b, err := s.st.Breaker(ctx, c.ID); err == nil && b.Paused {
		return no(false, "%s está pausado pelo disjuntor", c.Name)
	}
	risk := r.Risk.Risk
	perm := r.Type == "" || r.Type == classes.RequestClassPermission
	switch h.level.Kind {
	case LevelAgent:
		if !perm || risk != classes.RiskLow {
			return no(false, "o superior agente decide só o baixo de permissão de classe")
		}
		return yes()
	case LevelMesa:
		return s.mesaCan(ctx, c, r, st, perm)
	case LevelGrandMaster:
		// What the policy sends to the people whatever the conditions find (a
		// type that is not decided alone, a rule of quorum): the request is
		// born with them.
		rules, _ := s.rulesOf(r.Workspace)
		if why := rules.ForcesPeople(r); why != "" {
			return no(true, "%s", why)
		}
		switch risk {
		case classes.RiskLow, classes.RiskMedium:
			return yes()
		case classes.RiskHigh:
			if r.Risk.NoDelegation || s.o.Delegations == nil {
				return no(false, "alto só com delegação do Dono")
			}
			ok, err := s.o.Delegations.Covers(ctx, r)
			if err != nil || !ok {
				return no(false, "alto sem delegação do Dono para este tipo e escopo")
			}
			return yes()
		}
		return no(false, "risco %s não é do Grão-Mestre", risk)
	}
	return no(false, "nível desconhecido")
}

// mesaCan is the Mestre da mesa: baixo of its own table and the medio of the
// groups the Owner lets it decide, whose conditions the engine checks.
func (s *Service) mesaCan(ctx context.Context, c characters.Character, r Request, st Settings, perm bool) verdict {
	switch {
	case r.Cross:
		return no(true, "o pedido cruza mesas")
	case c.Table != r.Table:
		return no(true, "o pedido não é de um personagem da mesa de %s", c.Table)
	}
	if s.subCovers(ctx, c, r) {
		return yes()
	}
	if !perm {
		return no(false, "o Mestre da mesa decide só permissão de classe")
	}
	switch r.Risk.Risk {
	case classes.RiskLow:
		return yes()
	case classes.RiskMedium:
	default:
		return no(false, "o Mestre da mesa decide até o medio")
	}
	if s.o.Supervision != nil {
		if paused, err := s.o.Supervision.AutonomyPaused(ctx, r.Workspace, r.Table); err != nil || paused {
			return no(true, "a autonomia da mesa está em pausa")
		}
	}
	var tool *providers.Tool
	if r.Event != nil {
		tool = r.Event.Tool
	}
	if why := MesaGroupsHold(ctx, s.o.Classes, st, r.Requester, tool, r.Risk.Groups); why != "" {
		return no(true, "%s", why)
	}
	return yes()
}

// subCovers says a subdelegation of the Grão-Mestre lets this Mestre da mesa
// decide the request, above its own ceiling: the same safeguards hold (what the
// policy sends to the people, a type no delegation opens) and nothing else is
// widened.
func (s *Service) subCovers(ctx context.Context, c characters.Character, r Request) bool {
	if s.o.SubDelegations == nil || r.Risk.NoDelegation {
		return false
	}
	if rules, _ := s.rulesOf(r.Workspace); rules.ForcesPeople(r) != "" {
		return false
	}
	if s.o.Supervision != nil {
		if paused, err := s.o.Supervision.AutonomyPaused(ctx, r.Workspace, r.Table); err != nil || paused {
			return false // a paused table decides nothing above its own baixo
		}
	}
	ok, err := s.o.SubDelegations.SubCovers(ctx, r, c.ID)
	return err == nil && ok
}

// MesaGroupsHold says whether the Mestre da mesa may decide a medio whose
// origins belong to groups: every group is one of cadeia.mesa_decide now and
// its condition passes for the call (a network call only to the domains of the
// class and of the ceiling of whoever makes it). It returns why not, or "". The
// temporary table rules of the Mestre da mesa use it again at each use
// (docs/orquestracao/regras-temporarias-de-mesa.md).
func MesaGroupsHold(ctx context.Context, cls Classes, st Settings, requester string, tool *providers.Tool, groups []string) string {
	if len(groups) == 0 {
		return "o motor não deu o grupo do medio: sobe"
	}
	for _, g := range groups {
		switch {
		case g == classes.GroupNone:
			return "uma parte do pedido não tem grupo (o motor não a analisa)"
		case !st.MesaDecide(g):
			return fmt.Sprintf("o grupo %s não está em cadeia.mesa_decide", g)
		}
		if why := groupCondition(ctx, cls, g, requester, tool); why != "" {
			return why
		}
	}
	return ""
}

// groupCondition is the condition of a group the engine checks; "" is met.
// The common conditions (nothing that crosses tables, nothing of its own
// request, no mission point it designed) are checked by mesaCan and can.
func groupCondition(ctx context.Context, cls Classes, group, requester string, tool *providers.Tool) string {
	if group != classes.GroupNetwork {
		return ""
	}
	if tool == nil {
		return "rede: sem a ação para conferir os domínios"
	}
	hosts, unreadable := classes.ActionHosts(tool)
	if unreadable || len(hosts) == 0 {
		return "rede: o motor não lê o domínio do pedido"
	}
	domains, err := cls.Domains(ctx, requester)
	if err != nil {
		return "rede: os domínios da classe não foram lidos"
	}
	for _, h := range hosts {
		if !classes.DomainListed(domains, h) {
			return fmt.Sprintf("rede: %s não está nos domínios da classe nem do teto", h)
		}
	}
	return ""
}

// initial is the first level of the path that can decide the request, with
// why the ones below it were skipped.
func (s *Service) initial(ctx context.Context, r Request, st Settings) (Level, []string, error) {
	p, err := s.path(ctx, r)
	if err != nil {
		return Level{}, nil, err
	}
	l, why := s.firstFrom(ctx, p, 0, r, st)
	return l, why, nil
}

func (s *Service) firstFrom(ctx context.Context, p []hop, from int, r Request, st Settings) (Level, []string) {
	var why []string
	for _, h := range p[from:] {
		v := s.can(ctx, h, r, st)
		if v.ok {
			return h.level, why
		}
		if v.cited {
			why = append(why, v.why)
		}
	}
	return Level{Kind: LevelPeople}, why
}

// next is the first level above the current one that can decide the request.
func (s *Service) next(ctx context.Context, r Request, st Settings) (Level, []string, error) {
	p, err := s.path(ctx, r)
	if err != nil {
		return Level{}, nil, err
	}
	from := len(p) // the people: nothing above
	for i, h := range p {
		if h.level.same(r.Level) {
			from = i + 1
			break
		}
	}
	if from == len(p) && r.Level.Kind != LevelPeople {
		// The holder left the path (replaced, ended): the first hop of a kind
		// above its own.
		for i, h := range p {
			if h.level.Kind.rank() > r.Level.Kind.rank() {
				from = i
				break
			}
		}
	}
	if from >= len(p) {
		return Level{Kind: LevelPeople}, nil, nil
	}
	l, why := s.firstFrom(ctx, p, from, r, st)
	return l, why, nil
}

// stillHere says whether the level of the request is still a place in the
// path that can decide it (the holder was replaced, stopped or paused).
func (s *Service) stillHere(ctx context.Context, r Request, st Settings) (bool, error) {
	if r.Level.Kind == LevelPeople {
		return true, nil
	}
	p, err := s.path(ctx, r)
	if err != nil {
		if errors.Is(err, characters.ErrNoCharacter) {
			return false, nil
		}
		return false, err
	}
	for _, h := range p {
		if h.level.same(r.Level) {
			return s.can(ctx, h, r, st).ok, nil
		}
	}
	return false, nil
}

// joinWhy is the reasons a request skipped the levels below, each in one line:
// some name a domain or a part of the command the agent wrote.
func joinWhy(why []string) string {
	lines := make([]string, len(why))
	for i, w := range why {
		lines[i] = oneLine(w)
	}
	return strings.Join(lines, "; ")
}
