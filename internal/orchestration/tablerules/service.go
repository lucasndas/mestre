package tablerules

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Missions tells the missions (the quests) the rules are tied to.
type Missions interface {
	// Active is the quest a character works on now ("" for none).
	Active(ctx context.Context, character string) (string, error)
	// Over says the quest ended (completed or cancelled).
	Over(ctx context.Context, quest string) (bool, error)
}

// Tables tells whether a table ended or is ending.
type Tables interface {
	Over(ctx context.Context, workspace, table string) (bool, error)
}

// Options are the pieces of the Service. Store, Characters, Policy and
// Classes are required; the rest may be nil: the autonomy of a table is never
// paused, no rule lasts until the end of a mission, the tables never end and
// nobody is told.
type Options struct {
	Store      *Store
	Clock      clock.Clock
	Characters chain.Characters
	Policy     chain.Policy
	Classes    chain.Classes

	Supervision chain.Supervision
	Missions    Missions
	Tables      Tables
	Inbox       chain.Inbox
	Logger      *slog.Logger
}

// Service keeps and applies the temporary table rules.
type Service struct {
	o   Options
	st  *Store
	clk clock.Clock
	log *slog.Logger
}

var (
	_ chain.TempRules       = (*Service)(nil)
	_ classes.TableRuleGate = (*Service)(nil)
)

// NewService returns the Service.
func NewService(o Options) (*Service, error) {
	switch {
	case o.Store == nil || o.Store.DB == nil:
		return nil, errors.New("tablerules: the service needs the store")
	case o.Characters == nil, o.Policy == nil, o.Classes == nil:
		return nil, errors.New("tablerules: the service needs the characters, the policy and the classes")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{o: o, st: o.Store, clk: o.Clock, log: o.Logger}, nil
}

// Store gives access to the tables.
func (s *Service) Store() *Store { return s.st }

// Create makes the rule of an approval for a time or a pattern
// (chain.TempRules). The chain already checked the level, the risk and the
// validity; the rule is kept as it was given.
func (s *Service) Create(ctx context.Context, t chain.TempRule) error {
	switch {
	case t.Risk != classes.RiskLow && t.Risk != classes.RiskMedium:
		return fmt.Errorf("a temporary table rule covers baixo or medio, not %q", t.Risk)
	case t.Table == "" || t.Class == "" || t.By == "":
		return errors.New("a temporary table rule needs the table, the class and the author")
	case t.Until.IsZero() || !t.Until.After(s.clk.Now()):
		return errors.New("a temporary table rule needs a validity in the future")
	}
	r := Rule{Workspace: t.Workspace, Table: t.Table, Class: t.Class, Request: t.Request, State: Active, Level: t.Level.Kind, Author: t.By,
		Risk: t.Risk, Until: t.Until, CreatedAt: s.clk.Now()}
	if t.Pattern != nil {
		r.Pattern, r.ToolKind = t.Pattern.Pattern, t.Pattern.Kind
	} else {
		a := t.Action
		r.Action, r.ToolKind = &a, a.Kind
	}
	if t.UntilMissionEnd {
		if s.o.Missions == nil {
			return chain.ErrNoMission
		}
		q, err := s.o.Missions.Active(ctx, t.Requester)
		if err != nil {
			return err
		}
		if q == "" {
			return chain.ErrNoMission
		}
		r.Mission = q
	}
	_, err := s.st.Insert(ctx, r, "approval #"+fmt.Sprint(t.Request))
	return err
}

// Get returns a rule.
func (s *Service) Get(ctx context.Context, id int64) (Rule, error) { return s.st.Get(ctx, id) }

// List lists the rules of a table, over or not.
func (s *Service) List(ctx context.Context, workspace, table string) ([]Rule, error) {
	return s.st.OfTable(ctx, workspace, table)
}

// History lists the changes of state of a rule.
func (s *Service) History(ctx context.Context, id int64) ([]Change, error) {
	return s.st.History(ctx, id)
}

// Uses lists what a rule let through.
func (s *Service) Uses(ctx context.Context, id int64) ([]Use, error) { return s.st.Uses(ctx, id) }

// Decrees are the rules in force for the characters of a class in a table, as
// the decrees that enter their effective rules (Layers.Decrees,
// OrchestratorLayers.Decrees): kind table_rule, the level of the author, the
// validity, and the pattern as an allow of commands or tools. The engine checks
// each one again at each use (Use).
func (s *Service) Decrees(ctx context.Context, workspace, table, class string) ([]classes.Decree, error) {
	rules, err := s.st.Active(ctx, workspace, table, class)
	if err != nil {
		return nil, err
	}
	now := s.clk.Now()
	var out []classes.Decree
	for _, r := range rules {
		if !now.Before(r.Until) {
			continue
		}
		until := r.Until
		d := classes.Decree{ID: r.ID, Kind: classes.DecreeTableRule, Level: decreeLevel(r.Level), Until: &until, Risk: r.Risk, ToolKind: r.ToolKind,
			Effect: classes.Effect{Kind: classes.EffectPermission}}
		if r.Action != nil {
			a := *r.Action
			d.Action = &a
		} else if r.ToolKind == providers.KindShell {
			d.Effect.Allow = &classes.Grant{Commands: []string{r.Pattern}}
		} else {
			d.Effect.Allow = &classes.Grant{Tools: []string{r.Pattern}}
		}
		out = append(out, d)
	}
	return out, nil
}

// Use is the gate the engine asks at each use of a rule (classes.TableRuleGate).
// The rule must still be in force, the character must be of its class in its
// table, and its author must still hold the place that made it; the medio of the
// Mestre da mesa also needs the autonomy of the table not paused and the groups
// and the conditions of the table to pass for this call, now. It records what the
// rule lets through.
func (s *Service) Use(ctx context.Context, u classes.TableRuleUse) (bool, error) {
	r, err := s.st.Get(ctx, u.Rule)
	if err != nil {
		return false, err
	}
	now := s.clk.Now()
	if r.State != Active || !now.Before(r.Until) || u.Own.Risk == classes.RiskHigh || u.Own.Risk == classes.RiskFixed {
		return false, nil
	}
	c, err := s.o.Characters.Get(ctx, u.AgentID)
	if err != nil {
		return false, err
	}
	if c.Workspace != r.Workspace || c.Table != r.Table || c.Class != r.Class || (u.TableID != "" && u.TableID != r.Table) {
		return false, nil
	}
	if why, err := s.over(ctx, r); err != nil || why != "" {
		return false, err
	}
	// The rule of a superior that is an agent (baixo) covers its own
	// subordinates, not the whole class of the table.
	if r.Level == chain.LevelAgent {
		if ok, err := s.subordinateOf(ctx, c.ID, r.Author); err != nil || !ok {
			return false, err
		}
	}
	// No rule frees what the engine does not analyse nor a command that chains
	// others, the exact action as a pattern, for any level.
	if classes.TableRuleBlocker(u.Action, u.Own) != "" {
		return false, nil
	}
	// The author approves only what its own rules would not deny: checked on
	// the call that uses the rule, since a pattern reaches calls its author
	// never saw.
	if ok, err := s.authorAttenuates(ctx, r, u); err != nil || !ok {
		return false, err
	}
	if u.Own.Risk == classes.RiskMedium {
		if r.Risk != classes.RiskMedium {
			return false, nil
		}
		if r.Level == chain.LevelMesa {
			ok, err := s.mesaConditions(ctx, r, c, u)
			if err != nil || !ok {
				return false, err
			}
		}
	}
	if err := s.st.AddUse(ctx, r.ID, Use{At: now, Character: c.ID, Action: u.Action.String()}); err != nil {
		return false, err
	}
	return true, nil
}

// subordinateOf says whether the character answers to the author, directly or
// through the superiors above it.
func (s *Service) subordinateOf(ctx context.Context, character, author string) (bool, error) {
	cur := character
	for range 16 {
		sup, err := s.o.Characters.Superior(ctx, cur)
		if err != nil {
			return false, err
		}
		if sup.People || sup.ID == "" {
			return false, nil
		}
		if sup.ID == author {
			return true, nil
		}
		cur = sup.ID
	}
	return false, nil
}

// authorAttenuates says the effective rules of the author do not deny this
// call, in the environment of the character that makes it. Without the rules,
// the environment or the call, nothing is freed.
func (s *Service) authorAttenuates(ctx context.Context, r Rule, u classes.TableRuleUse) (bool, error) {
	if u.Tool == nil {
		return false, nil
	}
	rules, err := s.o.Classes.Rules(ctx, r.Author)
	if err != nil || rules == nil {
		return false, err
	}
	env, err := s.o.Classes.Env(ctx, u.AgentID)
	if err != nil {
		return false, err
	}
	ok, _ := classes.Attenuates(ctx, env, rules, providers.Event{Event: providers.EventPreTool, Tool: u.Tool})
	return ok, nil
}

// mesaConditions are the conditions of the table the Mestre da mesa's medio
// rule needs at each use (docs/orquestracao/cadeia-de-aprovacao.md#condições-da-mesa).
func (s *Service) mesaConditions(ctx context.Context, r Rule, c characters.Character, u classes.TableRuleUse) (bool, error) {
	if s.o.Supervision != nil {
		paused, err := s.o.Supervision.AutonomyPaused(ctx, r.Workspace, r.Table)
		if err != nil || paused {
			return false, err
		}
	}
	st, _, err := s.o.Policy.Chain(ctx, r.Workspace)
	if err != nil {
		return false, err
	}
	return chain.MesaGroupsHold(ctx, s.o.Classes, st.WithDefaults(), c.ID, u.Tool, u.Own.Groups) == "", nil
}

// over says why a rule that is active should be over now, "" if it should not:
// the end of the table, of the mission, or the author leaving the place that
// made it. The validity is checked by the caller.
func (s *Service) over(ctx context.Context, r Rule) (string, error) {
	if s.o.Tables != nil {
		if over, err := s.o.Tables.Over(ctx, r.Workspace, r.Table); err != nil {
			return "", err
		} else if over {
			return "a mesa foi encerrada", nil
		}
	}
	if r.Mission != "" && s.o.Missions != nil {
		if over, err := s.o.Missions.Over(ctx, r.Mission); err != nil {
			return "", err
		} else if over {
			return "a missão terminou", nil
		}
	}
	held, err := s.authorHolds(ctx, r)
	if err != nil {
		return "", err
	}
	if !held {
		return "o autor deixou de ser quem decide neste lugar", nil
	}
	return "", nil
}

// authorHolds says whether the author is still the character that holds the
// place it made the rule from.
func (s *Service) authorHolds(ctx context.Context, r Rule) (bool, error) {
	switch r.Level {
	case chain.LevelMesa:
		m, ok, err := s.o.Characters.TableMaster(ctx, r.Workspace, r.Table)
		return ok && m.ID == r.Author, err
	case chain.LevelGrandMaster:
		g, ok, err := s.o.Characters.GrandMaster(ctx, r.Workspace)
		return ok && g.ID == r.Author, err
	}
	c, err := s.o.Characters.Get(ctx, r.Author)
	if errors.Is(err, characters.ErrNoCharacter) {
		return false, nil
	}
	return err == nil && c.State.Alive(), err
}

// Sweep ends the rules that should be over: the validity ran out, the mission
// or the table ended, or the author left its place. Each ending tells the
// author.
func (s *Service) Sweep(ctx context.Context) error {
	rules, err := s.st.Live(ctx, "")
	if err != nil {
		return err
	}
	now := s.clk.Now()
	var errs []error
	for _, r := range rules {
		why := ""
		if !now.Before(r.Until) {
			why = "a validade acabou"
		} else if why, err = s.over(ctx, r); err != nil {
			errs = append(errs, err)
			continue
		}
		if why == "" {
			continue
		}
		done, err := s.st.Transition(ctx, r.ID, Expired, now, why, "", Active, Suspended)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if done {
			s.tell(ctx, r, fmt.Sprintf("A regra temporária #%d (%s) acabou: %s.", r.ID, s.describe(r), why))
		}
	}
	return errors.Join(errs...)
}

// Loop runs Sweep every so often until the context ends.
func (s *Service) Loop(ctx context.Context, every time.Duration) error {
	t := s.clk.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C():
			if err := s.Sweep(ctx); err != nil {
				s.log.Warn("tablerules: sweep", "err", err)
			}
		}
	}
}

// Revoker is who revokes: a character (the author, the Grão-Mestre of the
// workspace) or a person of the panel or of /decretos.
type Revoker struct {
	Character *characters.Character
	// Administrator is true for an Administrator or above (the panel, the
	// bot; milestones 5 and 6).
	Administrator bool
	// ID names a person.
	ID string
}

func (v Revoker) name() string {
	if v.Character != nil {
		return v.Character.ID
	}
	return "user:" + v.ID
}

// Revoke ends a rule: the author may, the Grão-Mestre of the workspace may, and
// an Administrator or above may. The next action goes back to asking, since the
// gate looks at the state at each use.
func (s *Service) Revoke(ctx context.Context, id int64, by Revoker) error {
	r, err := s.st.Get(ctx, id)
	if err != nil {
		return err
	}
	switch c := by.Character; {
	case by.Administrator:
	case c != nil && c.ID == r.Author:
	case c != nil && c.Role == characters.RoleGrandMaster && c.Workspace == r.Workspace:
	default:
		return ErrNotAllowed
	}
	done, err := s.st.Transition(ctx, id, Revoked, s.clk.Now(), "revogada", by.name(), Active, Suspended)
	switch {
	case err != nil:
		return err
	case !done:
		return ErrEnded
	}
	if by.name() != r.Author {
		s.tell(ctx, r, fmt.Sprintf("A regra temporária #%d (%s) foi revogada.", r.ID, s.describe(r)))
	}
	return nil
}

// RevokeAll revokes every rule in force of a workspace (the emergency button of
// the Owner, docs/aprovacoes/delegacao.md#revogar-tudo).
func (s *Service) RevokeAll(ctx context.Context, workspace, by string) (int, error) {
	rules, err := s.st.Live(ctx, workspace)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rules {
		done, err := s.st.Transition(ctx, r.ID, Revoked, s.clk.Now(), "revogadas todas", by, Active, Suspended)
		if err != nil {
			return n, err
		}
		if done {
			n++
			s.tell(ctx, r, fmt.Sprintf("A regra temporária #%d (%s) foi revogada com todas as outras.", r.ID, s.describe(r)))
		}
	}
	return n, nil
}

// Suspend suspends the rules in force of a workspace (/travar); Resume brings
// back the ones that are still valid.
func (s *Service) Suspend(ctx context.Context, workspace, by string) error {
	return s.move(ctx, workspace, by, Active, Suspended, "sistema travado")
}

// Resume reactivates the suspended rules when the lock ends.
func (s *Service) Resume(ctx context.Context, workspace, by string) error {
	return s.move(ctx, workspace, by, Suspended, Active, "sistema destravado")
}

func (s *Service) move(ctx context.Context, workspace, by string, from, to State, reason string) error {
	rules, err := s.st.Live(ctx, workspace)
	if err != nil {
		return err
	}
	for _, r := range rules {
		if r.State != from {
			continue
		}
		if _, err := s.st.Transition(ctx, r.ID, to, s.clk.Now(), reason, by, from); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) describe(r Rule) string {
	if r.Action != nil {
		return r.Action.String()
	}
	return r.Pattern
}

// tell tells the author, through its inbox, what happened to its rule.
func (s *Service) tell(ctx context.Context, r Rule, text string) {
	if s.o.Inbox == nil {
		return
	}
	if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: r.Workspace, Target: r.Author, Kind: inbox.KindSystem, Sender: inbox.SystemSender, Text: text,
		Refs: inbox.Refs{Table: r.Table}}); err != nil {
		s.log.Warn("tablerules: the author was not told", "rule", r.ID, "err", err)
	}
}
