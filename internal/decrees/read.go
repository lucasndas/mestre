package decrees

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/sessionzero"
	"github.com/mestre-ia/mestre/internal/users"
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
	frozen := s.locked(ctx)
	var out []View
	for _, d := range ds {
		if !u.Acts(d.Workspace) {
			continue
		}
		v := View{Decree: d, AuthorName: d.Author, Frozen: frozen}
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
	frozen := s.locked(ctx)
	var out []View
	for _, d := range ds {
		if d.Workspace != workspace || (!all && d.State != Active && d.State != Suspended) {
			continue
		}
		v := View{Decree: d, AuthorName: d.Author, Frozen: frozen && (d.State == Active || d.State == Suspended)}
		if a, err := readUser(ctx, r, d.Author); err == nil {
			v.AuthorName = a.Name
		}
		out = append(out, v)
	}
	return out, nil
}

// Use is the classes.ExceptionLedger of the exceptions the hook lets through:
// true only the first time, atomically, whoever the character is: an exception
// is spent once for the whole decree, not once per character it reaches. An
// effective use republishes the effective rules, so the exception leaves the
// rules of every character it reached.
func (s *Service) Use(ctx context.Context, agentID string, id int64) (bool, error) {
	return s.spend(ctx, agentID, id, "exceção pontual", s.clk.Now())
}

// maxUseAction bounds the action recorded in a use: it comes from an event.
const maxUseAction = 500

// spend marks the exception used and records the use, at the instant at; false
// when it was not active (already used, over).
func (s *Service) spend(ctx context.Context, agentID string, id int64, action string, at time.Time) (bool, error) {
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
		return insertUse(ctx, tx, id, at, agentID, action)
	})
	if used && err == nil {
		s.fireAsync(ctx)
	}
	return used && err == nil, err
}

func insertUse(ctx context.Context, tx queryer, number int64, at time.Time, character, action string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO decree_uses (decree, at, character, action) VALUES (?, ?, ?, ?)`, number, ms(at), character, boundText(action, maxUseAction))
	return err
}

func boundText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
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

// AppliedUse is what the hook did on the strength of a decree: the call (the
// decree must free that call), what to write of it in the list of the decree and
// the instant of the event, which can be older than the delivery (an event
// waits in the outbox of a Tower).
type AppliedUse struct {
	Tool   *providers.Tool
	Action string
	At     time.Time
}

// Applied registers that the hook let a call of the character through on the
// strength of the decree (hook.Answer.Decree): an exception is spent (it is used
// once, for the decree as a whole), a rule that stays active gets the execution
// in its list. The number an event carries is never trusted alone: it registers
// nothing, and says ErrEnded, when the decree is not one the hook frees a call
// with (an exception bound to a target goes to the engine, never to the hook;
// the call must be the exact action of an exception, and covered by what a rule
// allows), when it does not reach the character (another workspace or table), or
// when it was not active at the instant of the event. A use of a decree that was
// active then and ended since (revoked right after the hook allowed) is kept; a
// use after the end is recorded marked as such, with ErrEnded, so the caller
// warns.
func (s *Service) Applied(ctx context.Context, c characters.Character, number int64, u AppliedUse) error {
	if number <= 0 {
		return nil
	}
	d, err := getDecree(ctx, s.o.DB.Reader(), number)
	if err != nil {
		return err
	}
	now := s.clk.Now()
	at := u.At
	if at.IsZero() || at.After(now) {
		at = now
	}
	if d.Effect.Kind != EffectPermission || !reaches(d, c) {
		return ErrEnded
	}
	if d.Kind == KindException {
		if d.Impression != "" || d.Action == nil || !classes.ActionMatchesTool(*d.Action, u.Tool) {
			return ErrEnded
		}
	} else if d.Effect.Allow == nil || !classes.GrantCoversTool(d.Effect.Allow, u.Tool) {
		return ErrEnded
	}
	hist, err := s.History(ctx, number)
	if err != nil {
		return err
	}
	if !activeAt(d, hist, at) {
		return s.recordAfterEnd(ctx, number, c.ID, "[depois do fim] "+u.Action, now)
	}
	if d.Kind == KindException {
		return s.appliedException(ctx, d, c, u.Action, at)
	}
	if d.State == Active {
		if level, err := s.levelOf(ctx, s.o.DB.Reader(), d); err != nil {
			return err
		} else if level == "" {
			return ErrEnded
		}
	}
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error { return insertUse(ctx, tx, number, at, c.ID, u.Action) })
}

// appliedException spends an exception the hook let through. The ledger of the
// Citadel already spent it for a character of its own (Use), and then the use of
// that character gets the real action; a second character, or a second use of
// the same one, is refused and recorded as repeated.
func (s *Service) appliedException(ctx context.Context, d Decree, c characters.Character, action string, at time.Time) error {
	if d.State == Active {
		ok, err := s.spend(ctx, c.ID, d.Number, action, at)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		return s.recordAfterEnd(ctx, d.Number, c.ID, "[uso repetido] "+action, s.clk.Now())
	}
	// used: by this character through the ledger (its row has the placeholder)
	// or by another one
	res, err := s.execResult(ctx, `UPDATE decree_uses SET action = ? WHERE id = (SELECT id FROM decree_uses WHERE decree = ? AND character = ? AND action = 'exceção pontual' ORDER BY id LIMIT 1)`, boundText(action, maxUseAction), d.Number, c.ID)
	if err != nil {
		return err
	}
	if res > 0 {
		return nil
	}
	// the same event told again (a replay after a reconnection) is the same use
	var n int
	if err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM decree_uses WHERE decree = ? AND character = ? AND action = ?`, d.Number, c.ID, boundText(action, maxUseAction)).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if d.State == Used {
		return s.recordAfterEnd(ctx, d.Number, c.ID, "[uso repetido] "+action, s.clk.Now())
	}
	// revoked or expired after the hook allowed it, active at the instant: kept
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error { return insertUse(ctx, tx, d.Number, at, c.ID, action) })
}

func (s *Service) execResult(ctx context.Context, q string, args ...any) (int64, error) {
	var n int64
	err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return n, err
}

// recordAfterEnd keeps the use that came after the decree ended (or a repeated
// one), marked, and says ErrEnded.
func (s *Service) recordAfterEnd(ctx context.Context, number int64, character, action string, at time.Time) error {
	if err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error { return insertUse(ctx, tx, number, at, character, action) }); err != nil {
		return err
	}
	return ErrEnded
}

// activeAt says whether the decree stood in force at the instant: its state at
// that moment, from the history, was active, and its validity had not run out.
func activeAt(d Decree, hist []Change, at time.Time) bool {
	if !d.Until.IsZero() && !at.Before(d.Until) {
		return false
	}
	state := State("")
	for _, h := range hist {
		if h.At.After(at) {
			break
		}
		state = h.State
	}
	// the use of an exception is the end of it: it was in force until then
	return state == Active || (d.Kind == KindException && state == Used)
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

// FindException is the active exception of a workspace, in validity, for a request of the
// type bound to the impression of the target: the one that `gh merge` redeems. It is only a
// search; Redeem checks the target again and spends it. ok is false when there is none.
func (s *Service) FindException(ctx context.Context, workspace string, typ classes.RequestType, target json.RawMessage) (number int64, ok bool, err error) {
	imp, err := chain.Impress(target)
	if err != nil || workspace == "" {
		return 0, false, nil
	}
	ds, err := queryDecrees(ctx, s.o.DB.Reader(), `WHERE kind = ? AND state = ? AND workspace = ? AND request_type = ? AND impression = ? ORDER BY id`,
		string(KindException), string(Active), workspace, string(typ), imp)
	if err != nil {
		return 0, false, err
	}
	now := s.clk.Now()
	for _, d := range ds {
		if d.Until.IsZero() || now.Before(d.Until) {
			return d.Number, true, nil
		}
	}
	return 0, false, nil
}

// ExpireMoved expires, unused, the active exceptions of the workspace for a request of the type
// whose target is the same pull request (same repository and number) but another state of it (another
// head): the PR moved since they were made, so they can never apply again. It returns the number of the
// first one it expired (0 when none).
func (s *Service) ExpireMoved(ctx context.Context, workspace string, typ classes.RequestType, target json.RawMessage) (int64, error) {
	imp, err := chain.Impress(target)
	var cur struct {
		Repo string `json:"repo"`
		PR   int    `json:"pr"`
	}
	if err != nil || workspace == "" || json.Unmarshal(target, &cur) != nil || cur.PR == 0 {
		return 0, nil
	}
	ds, err := queryDecrees(ctx, s.o.DB.Reader(), `WHERE kind = ? AND state = ? AND workspace = ? AND request_type = ? AND impression != ? ORDER BY id`,
		string(KindException), string(Active), workspace, string(typ), imp)
	if err != nil {
		return 0, err
	}
	var first int64
	for _, d := range ds {
		var old struct {
			Repo string `json:"repo"`
			PR   int    `json:"pr"`
		}
		if json.Unmarshal(d.TargetRef, &old) != nil || old.PR != cur.PR || !chain.SameRepo(old.Repo, cur.Repo) {
			continue
		}
		n := d.Number
		if err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
			_, err := transition(ctx, tx, n, Expired, "", "o alvo mudou antes de a exceção ser usada", s.clk.Now(), Active)
			return err
		}); err != nil {
			return first, err
		}
		if first == 0 {
			first = n
		}
	}
	return first, nil
}

// Redeem spends an exception for an action that the engine executes (a merge):
// the live target is compared with the impression the exception was bound to,
// and if anything changed (a new commit in the PR) the exception expires unused
// and the merge is not made with it (ErrTargetChanged). The request being
// redeemed is required: an exception made in a workspace and bound to a target
// spends nothing for a request of another workspace or of another target
// (ErrWrongRequest), whatever the number the caller names.
func (s *Service) Redeem(ctx context.Context, number int64, req chain.Request, character, action string) error {
	d, err := getDecree(ctx, s.o.DB.Reader(), number)
	if err != nil {
		return err
	}
	now := s.clk.Now()
	if d.Kind != KindException || d.State != Active || (!d.Until.IsZero() && !now.Before(d.Until)) {
		return ErrEnded
	}
	if req.Workspace == "" || req.Workspace != d.Workspace {
		return ErrWrongRequest
	}
	if d.Impression != "" {
		imp := req.Impression
		if len(req.Target) > 0 {
			var err error
			if imp, err = chain.Impress(req.Target); err != nil {
				return ErrWrongRequest
			}
		}
		if imp != d.Impression {
			return ErrWrongRequest
		}
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
		// a decree made for one table reaches only that table, whatever it aims at
		if d.Mesa != "" && d.Mesa != r.Table {
			continue
		}
		switch d.Target.Kind {
		case TargetWorkspace:
		case TargetTable:
			if r.Table == "" || d.Mesa == "" && d.Target.Name != r.Table {
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
			// only the decree of someone who still can make it loosens: an author who is gone,
			// suspended or below an Administrator leaves the policy as it was (tightening needs no check)
			level, err := s.levelOf(ctx, s.o.DB.Reader(), d)
			if err != nil {
				return a, err
			}
			if level == "" || !level.AtLeast(classes.LevelAdmin) {
				continue
			}
			a.Loosen = append(a.Loosen, d.Effect.Setting)
		} else {
			a.Tighten = append(a.Tighten, d.Effect.Setting)
		}
	}
	return a, nil
}
