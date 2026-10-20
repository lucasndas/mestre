package decrees

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/users"
)

// Get returns a decree.
func (s *Service) Get(ctx context.Context, number int64) (Decree, error) {
	return getDecree(ctx, s.o.DB.Reader(), number)
}

// History lists the changes of state of a decree, oldest first.
func (s *Service) History(ctx context.Context, number int64) ([]Change, error) {
	rows, err := s.o.DB.Reader().QueryContext(ctx, `SELECT at, state, by, note FROM decree_events WHERE decree = ? ORDER BY id`, number)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Change
	for rows.Next() {
		var c Change
		var at int64
		var st string
		if err := rows.Scan(&at, &st, &c.By, &c.Note); err != nil {
			return nil, err
		}
		c.At, c.State = fromMs(at), State(st)
		out = append(out, c)
	}
	return out, rows.Err()
}

// Uses lists what was done on the strength of a decree.
func (s *Service) Uses(ctx context.Context, number int64) ([]Use, error) {
	rows, err := s.o.DB.Reader().QueryContext(ctx, `SELECT at, character, action FROM decree_uses WHERE decree = ? ORDER BY id`, number)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Use
	for rows.Next() {
		var u Use
		var at int64
		if err := rows.Scan(&at, &u.Character, &u.Action); err != nil {
			return nil, err
		}
		u.At = fromMs(at)
		out = append(out, u)
	}
	return out, rows.Err()
}

// subject is the class a decree is aimed at for a character: the orchestrators
// (the Grand Master and the Masters of the tables) have the class
// `orquestrador`, the others their own.
func subject(c characters.Character) string {
	if c.Role == characters.RoleGrandMaster || c.Role == characters.RoleTableMaster {
		return classes.ReservedOrchestrator
	}
	return c.Class
}

// reaches says the decree is aimed at the character, in its scope.
func reaches(d Decree, c characters.Character) bool {
	if d.Workspace != c.Workspace || (d.Mesa != "" && d.Mesa != c.Table) {
		return false
	}
	switch d.Target.Kind {
	case TargetClass:
		return d.Target.Name == subject(c)
	case TargetCharacter:
		return d.Target.Name == c.ID || d.Target.Name == c.Name
	case TargetWorkspace:
		return true
	case TargetTable:
		return c.Table != "" && d.Target.Name == c.Table
	}
	return false
}

// Decrees are the active decrees and exceptions that reach a character, as the
// decrees that enter its effective rules (app.CharacterDecrees). Only the
// permissions reach the decision; the level that counts is the one the author
// has now, and the decree of an author who is gone or suspended counts for
// nothing. A decree past its validity is not given even before the sweep ends it.
func (s *Service) Decrees(ctx context.Context, c characters.Character) ([]classes.Decree, error) {
	now := s.clk.Now()
	ds, err := queryDecrees(ctx, s.o.DB.Reader(), `WHERE state = 'active' AND effect_kind = 'permission' AND workspace = ? AND (until = 0 OR until > ?) ORDER BY id`, c.Workspace, ms(now))
	if err != nil {
		return nil, err
	}
	var out []classes.Decree
	for _, d := range ds {
		if !reaches(d, c) {
			continue
		}
		level, err := s.levelOf(ctx, s.o.DB.Reader(), d)
		if err != nil {
			return nil, err
		}
		if level == "" {
			continue
		}
		cd := classes.Decree{ID: d.Number, Level: level, Effect: classes.Effect{Kind: classes.EffectPermission, Allow: d.Effect.Allow, Deny: d.Effect.Deny}}
		if !d.Until.IsZero() {
			until := d.Until
			cd.Until = &until
		}
		if d.Kind == KindException {
			if d.Action == nil || d.Impression != "" {
				continue // bound to a target: only the engine spends it (Redeem)
			}
			a := *d.Action
			cd.Kind, cd.Action = classes.DecreeException, &a
		}
		out = append(out, cd)
	}
	return out, nil
}

// Notes are the active decrees that change behavior and reach a character, for
// its Mestre layer.
func (s *Service) Notes(ctx context.Context, c characters.Character) ([]sessionzero.DecreeNote, error) {
	now := s.clk.Now()
	ds, err := queryDecrees(ctx, s.o.DB.Reader(), `WHERE state = 'active' AND effect_kind = 'behavior' AND workspace = ? AND (until = 0 OR until > ?) ORDER BY id`, c.Workspace, ms(now))
	if err != nil {
		return nil, err
	}
	var out []sessionzero.DecreeNote
	for _, d := range ds {
		if !reaches(d, c) {
			continue
		}
		u, err := readUser(ctx, s.o.DB.Reader(), d.Author)
		if err != nil || u.State != users.Active {
			continue
		}
		n := sessionzero.DecreeNote{Number: d.Number, Author: u.Name, Level: u.Level.Name(), Text: d.Effect.Instruction}
		if !d.Until.IsZero() {
			until := d.Until
			n.Until = &until
		}
		out = append(out, n)
	}
	return out, nil
}

// List is what /decretos shows a user: the active and suspended decrees of the
// workspaces he acts in, with the level that counts now and, for a decree that a
// higher one takes the same point from, the number of that one.
func (s *Service) List(ctx context.Context, userID string) ([]View, error) {
	r := s.o.DB.Reader()
	u, err := readUser(ctx, r, userID)
	if err != nil {
		return nil, err
	}
	if u.State != users.Active {
		return nil, nil
	}
	ds, err := queryDecrees(ctx, r, `WHERE state IN ('active', 'suspended') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var out []View
	for _, d := range ds {
		if !u.Acts(d.Workspace) {
			continue
		}
		v := View{Decree: d, AuthorName: d.Author}
		if a, err := readUser(ctx, r, d.Author); err == nil {
			v.AuthorName = a.Name
		}
		if v.Level, err = s.levelOf(ctx, r, d); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	for i := range out {
		a := &out[i]
		if a.State != Active || a.Kind != KindRule || a.Effect.Kind != EffectPermission {
			continue
		}
		for _, b := range out {
			if b.State == Active && b.Kind == KindRule && b.Number != a.Number && b.Workspace == a.Workspace && b.Target == a.Target && sameMesa(a.Mesa, b.Mesa) &&
				opposed(a.Effect, b.Effect) && rank(b.Level) > rank(a.Level) {
				a.OverriddenBy = b.Number
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, nil
}

// ListWorkspace lists the decrees of a workspace: the ones in force, or, with all,
// the revoked, expired and used ones too.
func (s *Service) ListWorkspace(ctx context.Context, workspace string, all bool) ([]View, error) {
	r := s.o.DB.Reader()
	ds, err := queryDecrees(ctx, r, `ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var out []View
	for _, d := range ds {
		if d.Workspace != workspace || (!all && d.State != Active && d.State != Suspended) {
			continue
		}
		v := View{Decree: d, AuthorName: d.Author}
		if a, err := readUser(ctx, r, d.Author); err == nil {
			v.AuthorName = a.Name
		}
		out = append(out, v)
	}
	return out, nil
}

// Use is the classes.ExceptionLedger of the exceptions the hook lets through:
// true only the first time, atomically. It records the use in the decree.
func (s *Service) Use(ctx context.Context, agentID string, id int64) (bool, error) {
	used := false
	err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		now := s.clk.Now()
		res, err := tx.ExecContext(ctx, `UPDATE decrees SET state = 'used', ended_at = ? WHERE id = ? AND kind = 'exception' AND state = 'active' AND (until = 0 OR until > ?)`, ms(now), id, ms(now))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return nil
		}
		used = true
		if err := addEvent(ctx, tx, id, Used, agentID, "usada", now); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO decree_uses (decree, at, character, action) VALUES (?, ?, ?, 'exceção pontual')`, id, ms(now), agentID)
		return err
	})
	return used && err == nil, err
}

// Executed records that the character did an action on the strength of a decree
// that stays active (a rule, a delegation): the list of what was executed
// (docs/telegram/decretos.md#registro). It records nothing for a decree that is
// over.
func (s *Service) Executed(ctx context.Context, number int64, character, action string) error {
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		d, err := getDecree(ctx, tx, number)
		if err != nil {
			return err
		}
		if d.State != Active && d.State != Used {
			return ErrEnded
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO decree_uses (decree, at, character, action) VALUES (?, ?, ?, ?)`, number, ms(s.clk.Now()), character, action)
		return err
	})
}

// Fulfilled registers, in each active decree that frees the push to the
// character, that the engine sent the branch of its table for it (`mestre pr
// open` and `mestre pr update`): the agent never has the push, and the decree
// is carried out by the engine (docs/arquitetura/precedencia.md#invariantes-arquiteturais).
func (s *Service) Fulfilled(ctx context.Context, c characters.Character, action string) error {
	ds, err := queryDecrees(ctx, s.o.DB.Reader(), `WHERE state = 'active' AND kind = 'rule' AND effect_kind = 'permission' AND workspace = ? AND (until = 0 OR until > ?) ORDER BY id`, c.Workspace, ms(s.clk.Now()))
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range ds {
		if !reaches(d, c) || d.Effect.Allow == nil {
			continue
		}
		if level, err := s.levelOf(ctx, s.o.DB.Reader(), d); err != nil || level == "" {
			continue
		}
		for _, p := range d.Effect.Allow.Commands {
			if commandCovers(p, "git push") {
				if err := s.Executed(ctx, d.Number, c.ID, action); err != nil {
					errs = append(errs, err)
				}
				break
			}
		}
	}
	return errors.Join(errs...)
}

// commandCovers says the pattern of a command takes the command ("git push *"
// takes "git push"; "git *" too).
func commandCovers(pattern, command string) bool {
	base := strings.ToLower(strings.TrimRight(strings.TrimSpace(pattern), "* "))
	return base != "" && strings.HasPrefix(command, base)
}

// Redeem spends an exception for an action that the engine executes (a merge):
// the live target is compared with the impression the exception was bound to,
// and if anything changed (a new commit in the PR) the exception expires unused
// and the merge is not made with it (ErrTargetChanged).
func (s *Service) Redeem(ctx context.Context, number int64, character, action string) error {
	d, err := getDecree(ctx, s.o.DB.Reader(), number)
	if err != nil {
		return err
	}
	now := s.clk.Now()
	if d.Kind != KindException || d.State != Active || (!d.Until.IsZero() && !now.Before(d.Until)) {
		return ErrEnded
	}
	if d.Impression != "" {
		if err := s.verify(ctx, d); err != nil {
			if err == ErrTargetChanged {
				_ = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
					_, err := transition(ctx, tx, number, Expired, "", "o alvo mudou antes de a exceção ser usada", s.clk.Now(), Active)
					return err
				})
			}
			return err
		}
	}
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE decrees SET state = 'used', ended_at = ? WHERE id = ? AND kind = 'exception' AND state = 'active'`, ms(now), number)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrEnded
		}
		if err := addEvent(ctx, tx, number, Used, character, "usada", now); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO decree_uses (decree, at, character, action) VALUES (?, ?, ?, ?)`, number, ms(now), character, action)
		return err
	})
}

// verify compares the live target with the impression of the exception.
func (s *Service) verify(ctx context.Context, d Decree) error {
	if s.o.Targets == nil {
		return ErrNoVerify
	}
	var t struct {
		Repo string `json:"repo"`
		PR   int    `json:"pr"`
		Head string `json:"head"`
	}
	if err := json.Unmarshal(d.TargetRef, &t); err != nil || t.Repo == "" || t.PR <= 0 {
		return ErrNoVerify
	}
	r := chain.Request{ID: d.Request, Workspace: d.Workspace, Table: d.Mesa, Type: classes.RequestType(d.RequestType), Target: d.TargetRef, Impression: d.Impression,
		PR: &chain.PRRef{Repo: t.Repo, Number: t.PR, SHA: t.Head}}
	cur, err := s.o.Targets.Current(ctx, r)
	if err != nil {
		return ErrNoVerify
	}
	imp, err := chain.Impress(cur)
	if err != nil {
		return ErrNoVerify
	}
	if imp != d.Impression {
		return ErrTargetChanged
	}
	return nil
}

// Covers is the chain.Delegations: a decree of delegation of the Owner, active,
// for the type of the request, in the workspace and in the scope of it (the
// table, the repository), lets the Grand Master decide a request of risk alto.
// A delegation of an author who is no longer the Owner covers nothing. It only
// answers; Used registers the use when the Grand Master approves.
func (s *Service) Covers(ctx context.Context, r chain.Request) (bool, error) {
	_, ok, err := s.delegationFor(ctx, r)
	return ok, err
}

var (
	_ chain.Delegations        = (*Service)(nil)
	_ chain.DelegationRecorder = (*Service)(nil)
	_ classes.ExceptionLedger  = (*Service)(nil)
)

// Used implements chain.DelegationRecorder: the approval of the Grand Master
// under a delegation is registered in the decree.
func (s *Service) Used(ctx context.Context, r chain.Request, by string) error {
	d, ok, err := s.delegationFor(ctx, r)
	if err != nil || !ok {
		return err
	}
	return s.Executed(ctx, d.Number, by, fmt.Sprintf("pedido %d (%s)", r.ID, r.Type))
}

// Delegation is the decree of delegation that covers a request, if one does.
func (s *Service) Delegation(ctx context.Context, r chain.Request) (Decree, bool, error) {
	return s.delegationFor(ctx, r)
}

func (s *Service) delegationFor(ctx context.Context, r chain.Request) (Decree, bool, error) {
	now := s.clk.Now()
	ds, err := queryDecrees(ctx, s.o.DB.Reader(), `WHERE state = 'active' AND effect_kind = 'delegation' AND workspace = ? AND (until = 0 OR until > ?) ORDER BY id`, r.Workspace, ms(now))
	if err != nil {
		return Decree{}, false, err
	}
	for _, d := range ds {
		if d.Effect.Setting != string(r.Type) || (d.Mesa != "" && d.Mesa != r.Table) {
			continue
		}
		if level, err := s.levelOf(ctx, s.o.DB.Reader(), d); err != nil {
			return Decree{}, false, err
		} else if level != classes.LevelOwner {
			continue
		}
		if d.Scope != "" {
			if class := scopeClass(d.Scope); class != "" {
				if class != r.Class {
					continue
				}
			} else if repo := scopeRepo(d.Scope); repo == "" || repo != requestRepo(r) {
				continue // a scope the engine does not understand covers nothing
			}
		}
		return d, true, nil
	}
	return Decree{}, false, nil
}

func scopeClass(scope string) string {
	const p = "classe:"
	if len(scope) > len(p) && scope[:len(p)] == p {
		return scope[len(p):]
	}
	return ""
}

func scopeRepo(scope string) string {
	const p = "repo:"
	if len(scope) > len(p) && scope[:len(p)] == p {
		return scope[len(p):]
	}
	return ""
}

func requestRepo(r chain.Request) string {
	var t struct {
		Repo string `json:"repo"`
	}
	if json.Unmarshal(r.Target, &t) == nil && t.Repo != "" {
		return t.Repo
	}
	if r.PR != nil {
		return r.PR.Repo
	}
	return ""
}

var _ chain.PolicyDecrees = (*Service)(nil)

// PolicyFor is the chain.PolicyDecrees: the active decrees of policy, still
// valid, that reach the request by its workspace, its table or its class. A
// decree aimed at a character reaches nothing here (it is read by name, and a
// policy is not about a name).
func (s *Service) PolicyFor(ctx context.Context, r chain.Request) (chain.PolicyAdjust, error) {
	var a chain.PolicyAdjust
	now := s.clk.Now()
	ds, err := queryDecrees(ctx, s.o.DB.Reader(), `WHERE state = 'active' AND effect_kind = 'policy' AND workspace = ? AND (until = 0 OR until > ?) ORDER BY id`, r.Workspace, ms(now))
	if err != nil {
		return a, err
	}
	for _, d := range ds {
		switch d.Target.Kind {
		case TargetWorkspace:
		case TargetTable:
			if d.Mesa != r.Table && d.Target.Name != r.Table {
				continue
			}
		case TargetClass:
			if d.Target.Name != r.Class {
				continue
			}
		default:
			continue
		}
		if d.Effect.Loosens {
			a.Loosen = append(a.Loosen, d.Effect.Setting)
		} else {
			a.Tighten = append(a.Tighten, d.Effect.Setting)
		}
	}
	return a, nil
}
