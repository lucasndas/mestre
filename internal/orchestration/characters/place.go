package characters

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mestre-ia/mestre/internal/orchestration/presets"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/sessionzero"
)

// authorize says whether who asks may create what is asked
// (docs/orquestracao/personagens.md#origem-só-de-fichas-e-bases).
func (s *Service) authorize(ctx context.Context, req CreateRequest) error {
	deny := func(why string) error { return fmt.Errorf("%w: %s", ErrLevelDenied, why) }
	switch req.Creator.Kind {
	case CreatedByEngine:
		return nil
	case CreatedByUser:
		if req.Role == RoleGrandMaster {
			return deny("o Grão-Mestre nasce com a campanha")
		}
		if req.Creator.Level != LevelOwner && req.Creator.Level != LevelAdministrator {
			return deny("só o Senhor da Guilda e os Administradores criam personagens pelo canvas")
		}
		return nil
	case CreatedByCharacter:
		c, err := s.o.Registry.Get(ctx, req.Creator.ID)
		if err != nil || !c.State.Alive() {
			return deny("quem pediu não é um personagem vivo")
		}
		switch c.Role {
		case RoleGrandMaster:
			// Only table masters (`table open`) and the Operator of the PC.
			if req.Role == RoleTableMaster || req.Sheet != "" && s.isOperatorSheet(ctx, req.Sheet) {
				return nil
			}
			return deny("o Grão-Mestre só cria Mestres de mesa e o Operador do PC")
		case RoleTableMaster:
			if req.Role != RoleCharacter {
				return deny("o Mestre da mesa só cria personagens e terminais simples")
			}
			if req.Table != c.Table || c.Workspace != req.Workspace {
				return deny("o Mestre da mesa só cria na própria mesa")
			}
			return nil
		}
		return deny("um personagem comum não cria personagens")
	}
	return deny("quem pediu é desconhecido")
}

func (s *Service) isOperatorSheet(ctx context.Context, sheet string) bool {
	snap, err := s.o.Presets.Snapshot(ctx, sheet)
	return err == nil && snap.Base.Name == presets.OperatorBase
}

// limitsHold applies the limits to a request and chooses its machine. It
// returns what holds the request, or nil with p.machine set. The Grand
// Master and the table masters count nothing and are never held back.
func (s *Service) limitsHold(ctx context.Context, p *placement) (*blocked, error) {
	if old := p.req.ReplaceOf; old != "" {
		c, err := s.o.Registry.Get(ctx, old)
		if err == nil && c.State != Stopped {
			return &blocked{limit: limitReplace, holders: []string{c.Name}}, nil
		}
	}
	all, err := s.o.Registry.Alive(ctx)
	if err != nil {
		return nil, err
	}
	var counted []Character
	active := map[string]int{}
	byMachine := map[string][]string{}
	for _, c := range all {
		if c.Counts() {
			counted = append(counted, c)
			active[c.Machine]++
			byMachine[c.Machine] = append(byMachine[c.Machine], c.Name)
		}
	}
	exempt := p.req.Role != RoleCharacter
	if !exempt {
		inWorkspace := func(c Character) bool { return c.Workspace == p.req.Workspace }
		var ws, table, copies []string
		for _, c := range counted {
			if !inWorkspace(c) {
				continue
			}
			ws = append(ws, c.Name)
			if c.Table == p.req.Table {
				table = append(table, c.Name)
			}
			if p.kind == KindCharacter && c.Sheet == p.snap.Sheet.Name {
				copies = append(copies, c.Name)
			}
		}
		switch {
		case p.req.Table != "" && len(table) >= s.o.Limits.PerTable:
			return &blocked{LimitTable, s.o.Limits.PerTable, table}, nil
		case len(ws) >= s.o.Limits.PerWorkspace:
			return &blocked{LimitWorkspace, s.o.Limits.PerWorkspace, ws}, nil
		case p.kind == KindCharacter && len(copies) >= p.snap.Sheet.Copies():
			return &blocked{LimitSheet, p.snap.Sheet.Copies(), copies}, nil
		}
	}
	order := s.order(p, active)
	for _, m := range order {
		if exempt || active[m] < s.o.Limits.PerMachine {
			p.machine = m
			return nil, nil
		}
	}
	var holders []string
	for _, m := range order {
		holders = append(holders, byMachine[m]...)
	}
	return &blocked{LimitMachine, s.o.Limits.PerMachine, holders}, nil
}

// order puts the machines that fit in the order of preference: the machine
// the request prefers (the one of the character it replaces), the preferred
// machine of the sheet, the machine where the table already has a worktree,
// the one with fewer active characters, and the id
// (docs/orquestracao/personagens.md#escolha-da-máquina). The Citadel has no
// preference of its own.
func (s *Service) order(p *placement, active map[string]int) []string {
	out := slices.Clone(p.fit)
	rank := func(m string) [2]int {
		var r [2]int
		if m != p.req.Prefer {
			r[0] |= 2
		}
		if m != p.snap.Sheet.PreferredMachine {
			r[0] |= 1
		}
		if _, ok := p.table.Worktrees[m]; !ok {
			r[1] = 1
		}
		return r
	}
	slices.SortStableFunc(out, func(a, b string) int {
		ra, rb := rank(a), rank(b)
		switch {
		case ra[0] != rb[0]:
			return ra[0] - rb[0]
		case ra[1] != rb[1]:
			return ra[1] - rb[1]
		case active[a] != active[b]:
			return active[a] - active[b]
		}
		return strings.Compare(a, b)
	})
	return out
}

// insert makes the row of the character the request becomes, starting.
func (s *Service) insert(ctx context.Context, p placement) (Character, error) {
	now := s.clk.Now()
	snap := p.snap
	c := Character{
		ID: s.ids.NewString(), Name: p.name, Kind: p.kind, Role: p.req.Role, Workspace: p.req.Workspace, Table: p.req.Table,
		Machine: p.machine, Sheet: snap.Sheet.Name, Base: snap.Base.Name, Class: p.class, Account: snap.Sheet.Account,
		State: Starting, Reason: "created", Lifecycle: p.lifecyc, MaxIdle: p.maxIdle, CreatedBy: p.req.Creator,
		Quest: p.req.Quest, Step: p.req.Step, Pinned: p.req.Role == RoleGrandMaster, CreatedAt: now, StartedAt: now, LastSignalAt: now,
	}
	if p.req.Inherit != nil {
		c.CreatedBy = *p.req.Inherit
	}
	if c.CreatedBy.Kind == CreatedByEngine {
		c.CreatedBy.ID = ""
	}
	if err := s.o.Registry.Insert(ctx, c); err != nil {
		return Character{}, err
	}
	return c, nil
}

// launch has the machine start a character that was just registered, or
// that is resumed. A creation that fails leaves nothing behind.
func (s *Service) launch(ctx context.Context, c Character, p placement, resumed bool) error {
	fail := func(err error) error {
		if !resumed && c.Role == RoleGrandMaster {
			return s.grandMasterNotStarted(ctx, c, err)
		}
		if !resumed {
			if s.o.Connections != nil {
				_ = s.o.Connections.Ended(ctx, c)
			}
			if derr := s.o.Registry.Delete(ctx, c.ID); derr != nil {
				s.log.Warn("characters: a character that did not start was not removed", "id", c.ID, "err", derr)
			}
			_ = s.o.Notifier.Notify(ctx, Notice{Kind: NoticeStartFailed, To: Recipient{Kind: ToTableMaster}, Workspace: c.Workspace, Table: c.Table,
				Character: c.ID, Name: c.Name, Creator: c.CreatedBy, Text: fmt.Sprintf("%s não iniciou: %v", c.Name, err)})
		}
		return fmt.Errorf("character %s did not start: %w", c.Name, err)
	}
	table := p.table
	dir, err := s.o.Tables.Worktree(ctx, c.Workspace, c.Table, c.Machine)
	if err != nil {
		return fail(err)
	}
	c.Dir = dir
	if err := s.o.Registry.exec(ctx, c.ID, `dir = ?`, dir); err != nil {
		return fail(err)
	}
	in := BuildInput{Character: c, Snapshot: p.snap, Table: table, Dir: dir, Resumed: resumed}
	if resumed {
		in.ResumeSessionID = c.ProviderSessionID
	} else if p.snap.Sheet.Agent == presets.AgentClaudeCode {
		in.NewSessionID = uuid4()
	}
	req, err := s.o.Builder.Build(ctx, in)
	if err != nil {
		return fail(err)
	}
	term := terminalID(c, resumed)
	cmd := StartCommand{Character: c.ID, Terminal: term, Kind: c.Kind, Request: req, Resumed: resumed}
	if resumed && (c.ProviderSessionID == "" || c.Role == RoleGrandMaster || c.Role == RoleTableMaster) {
		cmd.ResumeMessage = s.resumeMessage(ctx, c)
	}
	res, err := s.o.Machines.Start(ctx, c.Machine, cmd)
	if err != nil {
		return fail(err)
	}
	if res.Terminal == "" {
		res.Terminal = term
	}
	if err := s.o.Registry.SetTerminal(ctx, c.ID, res.Terminal); err != nil {
		s.log.Warn("characters: the terminal was not recorded", "id", c.ID, "err", err)
	}
	session := res.Session
	if session == "" {
		session = in.NewSessionID
	}
	if session != "" {
		if err := s.o.Registry.SetSession(ctx, c.ID, session); err != nil {
			s.log.Warn("characters: the session was not recorded", "id", c.ID, "err", err)
		}
	}
	for _, w := range res.Warnings {
		s.log.Warn("characters: start", "id", c.ID, "warning", w)
	}
	return nil
}

// terminalID is the id of the terminal of a start: the id of the character the
// first time and `<id>-<n>` at each restart, so a socket never repeats.
func terminalID(c Character, resumed bool) string {
	if !resumed || c.TerminalID == "" {
		return c.ID
	}
	n := 1
	if i := strings.LastIndexByte(c.TerminalID, '-'); i > 0 && c.TerminalID[:i] == c.ID {
		if v, err := strconv.Atoi(c.TerminalID[i+1:]); err == nil {
			n = v
		}
	}
	return c.ID + "-" + strconv.Itoa(n+1)
}

// uuid4 is a version 4 UUID, the id of a new Claude Code session.
func uuid4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("characters: random: " + err.Error())
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Pump runs the requests that wait for a vacancy, in the order they arrived:
// the first one that fits is made; one whose time ran out fails, with a notice
// to whoever asked that says which limit held it and who occupies it. It never
// ends a character to make room.
func (s *Service) Pump(ctx context.Context) error {
	waits, err := s.o.Registry.ActiveSlotWaits(ctx)
	if err != nil {
		return err
	}
	now := s.clk.Now()
	for _, w := range waits {
		var req CreateRequest
		if err := json.Unmarshal(w.Request, &req); err != nil {
			_, _ = s.o.Registry.EndSlotWait(ctx, w.ID, SlotCancelled, now)
			continue
		}
		if req.ReplaceOf == "" && !now.Before(w.ExpiresAt) {
			if err := s.expire(ctx, w, req); err != nil {
				return err
			}
			continue
		}
		if _, _, err := s.attempt(ctx, req, w.ID); err != nil {
			s.log.Warn("characters: a request that waited for a vacancy failed", "wait", w.ID, "err", err)
		}
	}
	return nil
}

// expire fails a request whose wait for a vacancy ran out.
func (s *Service) expire(ctx context.Context, w SlotWait, req CreateRequest) error {
	done, err := s.o.Registry.EndSlotWait(ctx, w.ID, SlotExpired, s.clk.Now())
	if err != nil || !done {
		return err
	}
	max := map[string]int{LimitMachine: s.o.Limits.PerMachine, LimitTable: s.o.Limits.PerTable, LimitWorkspace: s.o.Limits.PerWorkspace}[w.Limit]
	what := req.Sheet
	if what == "" {
		what = req.Base
	}
	return s.o.Notifier.Notify(ctx, Notice{Kind: NoticeSlotTimeout, To: Recipient{Kind: ToCreator}, Workspace: req.Workspace, Table: req.Table,
		Creator: req.Creator, Name: req.Name,
		Text: fmt.Sprintf("O pedido de %q esperou %s por uma vaga e falhou: limite %s (%d) ocupado por %s.", what, s.o.SlotTimeout, w.Limit, max, w.Holders)})
}

var _ = providers.Null

// grandMasterNotStarted is what a Grand Master that could not start becomes:
// not a character that never was, but the Grand Master of the workspace,
// interrupted, with the reason. The messages of the users stay in its inbox,
// the users are told it is unavailable and why (the inbox does it as they
// write), the Owner is told now, and the next resumption of the machine starts
// it again (docs/orquestracao/grao-mestre.md#regras-e-erros).
func (s *Service) grandMasterNotStarted(ctx context.Context, c Character, cause error) error {
	// the reason is what the users read: no path of the machine, no raw error
	reason := "falha ao iniciar; o Senhor da Guilda foi avisado"
	var blocked *sessionzero.BlockedError
	if errors.As(cause, &blocked) {
		paths := make([]string, 0, len(blocked.Problems))
		for _, p := range blocked.Problems {
			paths = append(paths, p.Path)
		}
		reason = fmt.Sprintf("os arquivos do provedor %s no clone principal não foram gerados (%s)", blocked.Provider, strings.Join(paths, ", "))
	}
	s.log.Warn("characters: the Grand Master did not start", "workspace", c.Workspace, "err", cause)
	if _, _, err := s.o.Registry.Transition(ctx, c.ID, Interrupted, Change{Reason: reason, At: s.clk.Now()}); err != nil {
		return errors.Join(fmt.Errorf("character %s did not start: %w", c.Name, cause), err)
	}
	_ = s.o.Notifier.Notify(ctx, Notice{Kind: NoticeStartFailed, To: Recipient{Kind: ToOwner}, Workspace: c.Workspace,
		Character: c.ID, Name: c.Name, Creator: c.CreatedBy, Text: fmt.Sprintf("%s não iniciou: %v", c.Name, cause)})
	return fmt.Errorf("character %s did not start: %w", c.Name, cause)
}

// GrandMasterSheetProblem says why a sheet cannot be the one of a Grand Master
// (nil: it can). The Grand Master runs an agent, never on the base of the
// Operator of the PC (which only the Owner's approval activates), and its base
// must run on every machine the Owner did not exclude, because any of them may
// become the Citadel (docs/orquestracao/grao-mestre.md#onde-roda).
func GrandMasterSheetProblem(ctx context.Context, p Presets, sheet string) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrBadGrandMasterSheet, fmt.Sprintf(format, a...))
	}
	if sheet == "" {
		return bad("o Grão-Mestre nasce de uma ficha")
	}
	snap, err := p.Snapshot(ctx, sheet)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBadSheet, err)
	}
	switch {
	case snap.Sheet.Agent == presets.AgentShell:
		return bad("a ficha %s é um terminal simples, não um agente", sheet)
	case snap.Base.Name == presets.OperatorBase:
		return bad("a base %s é a do Operador do PC e só o Senhor da Guilda a ativa", presets.OperatorBase)
	}
	machines, err := p.Machines(ctx)
	if err != nil {
		return err
	}
	for _, m := range machines {
		if m.Excluded {
			continue
		}
		if ok, why := presets.RunsOn(snap.Base, m); !ok {
			return bad("%s", why)
		}
	}
	return nil
}
