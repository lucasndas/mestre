package decrees

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/pkg/clock"
)

// ProposalTTL is how long a proposal waits for the click of the author.
const ProposalTTL = 30 * time.Minute

// DefaultExceptionTTL is how long an exception not used stays valid when the
// engine gives no other (the same as the approvals, docs/aprovacoes/fluxo.md).
const DefaultExceptionTTL = 24 * time.Hour

// PINs is the PIN of the users (users.Service).
type PINs interface {
	StartPIN(ctx context.Context, userID, action string) (users.PINRequest, error)
	Consume(ctx context.Context, requestID, userID, action string) error
}

// Targets reads the live target of a request: the PR and its head. It is the
// same the flow of approval uses (approvals.Targets).
type Targets interface {
	Current(ctx context.Context, r chain.Request) (json.RawMessage, error)
}

// Requests reads the approval requests (chain.Service).
type Requests interface {
	Get(ctx context.Context, id int64) (chain.Request, error)
}

// Options are the pieces of the Service.
type Options struct {
	DB    *store.DB
	Clock clock.Clock
	// PINs is required; Alerts, Targets and Requests may be nil: nobody is
	// told, an exception bound to a target is not verified (and so is not
	// redeemed), and the answers that make a decree from the pattern of a
	// request are refused.
	PINs     PINs
	Alerts   users.Alerts
	Targets  Targets
	Requests Requests
	// Locked says the Mestre is locked (/travar): nothing is confirmed, revoked
	// or reactivated meanwhile. nil: never.
	Locked func(ctx context.Context) bool
	// TableOpen says whether a table is open: a subdelegation never outlives
	// its table. nil: every table is.
	TableOpen func(ctx context.Context, workspace, table string) (bool, error)
	// Reminder tells the author that a delegation is about to end, with
	// Renovar and Deixar vencer (nil: a plain notice through Alerts).
	Reminder Reminder
	// Revoked is what the emergency button also takes away and whom it tells
	// (nil: only the decrees).
	Revoked RevokeHooks
	// ReminderBefore is how long before its end a delegation is reminded
	// (default 3 days).
	ReminderBefore time.Duration
	// ExceptionTTL is how long an exception not used stays valid.
	ExceptionTTL time.Duration
	Logger       *slog.Logger
}

// Service keeps the decrees.
type Service struct {
	o   Options
	clk clock.Clock
	ids *protocol.IDGen
	log *slog.Logger
	ttl time.Duration
	// delegation gives the validity and the reminder of a workspace (nil: the
	// constants).
	delegation func(ctx context.Context, workspace string) (validity, reminder time.Duration)
	// changed is told after a write that can change what the decrees in force
	// say (a decree made, revoked, reactivated, suspended or invalidated), so
	// that the effective rules are published again.
	changed func(ctx context.Context)
}

// SetChanged sets what is told after the decrees in force may have changed (the
// publication of the effective rules). A failure belongs to whoever is told: the
// change is made.
func (s *Service) SetChanged(f func(ctx context.Context)) { s.changed = f }

// write is WriteDurable that tells the change once it is committed.
func (s *Service) write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if err := s.o.DB.WriteDurable(ctx, fn); err != nil {
		return err
	}
	if s.changed != nil {
		s.changed(ctx)
	}
	return nil
}

// New returns the Service.
func New(o Options) (*Service, error) {
	if o.DB == nil || o.Clock == nil || o.PINs == nil {
		return nil, errors.New("decrees: the Guild database, the clock and the PINs are required")
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	ttl := o.ExceptionTTL
	if ttl <= 0 {
		ttl = DefaultExceptionTTL
	}
	return &Service{o: o, clk: o.Clock, ids: protocol.NewIDGen(o.Clock), log: log, ttl: ttl}, nil
}

func (s *Service) locked(ctx context.Context) bool { return s.o.Locked != nil && s.o.Locked(ctx) }

func proposalAction(id string) string { return "decree:" + id }
func revokeAction(n int64) string     { return fmt.Sprintf("decree_revoke:%d", n) }

// Propose is what the Grand Master sends after it read the order of a user:
// the engine checks the level and the workspaces of the author, refuses what
// it can not do and, if all is well, keeps the proposal and returns what the
// confirmation says. The proposal is not a decree: only the click of the
// author makes it one (Confirm).
func (s *Service) Propose(ctx context.Context, p Proposal) (Pending, error) {
	p = normalize(p)
	u, err := readUser(ctx, s.o.DB.Reader(), p.Author)
	if err != nil {
		return Pending{}, refuse(CodeLevel, "o autor da ordem não é um usuário do Mestre")
	}
	now := s.clk.Now()
	why, ref := check(p, u, now)
	if ref != nil {
		return Pending{}, ref
	}
	body, err := json.Marshal(p)
	if err != nil {
		return Pending{}, err
	}
	id := s.ids.NewString()
	if err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO decree_proposals (id, author_id, payload, state, needs_pin, created_at, expires_at) VALUES (?, ?, ?, 'open', ?, ?, ?)`,
			id, u.ID, string(body), boolInt(why != ""), ms(now), ms(now.Add(ProposalTTL)))
		return err
	}); err != nil {
		return Pending{}, err
	}
	return Pending{ID: id, Author: u.ID, PIN: why != "", Why: why, Text: confirmationText(p, s.Moment(ctx, p.Until)), Workspace: p.Workspace, Mesa: p.Mesa}, nil
}

// normalize fills what an exception implies.
func normalize(p Proposal) Proposal {
	p.Text = strings.TrimSpace(p.Text)
	// a limit is whole seconds (never later than asked), so what is shown is
	// exactly the instant it ends
	p.Until = p.Until.Truncate(time.Second)
	if p.Kind == KindException && p.Effect.Kind == "" {
		p.Effect.Kind = EffectPermission
	}
	if p.Kind == KindException {
		p.Until = time.Time{}
	}
	return p
}

func confirmationText(p Proposal, moment string) string {
	until := "permanente"
	if p.Kind == KindException {
		until = "uma vez"
	} else if !p.Until.IsZero() {
		until = "até " + moment
	}
	where := "workspace " + p.Workspace
	if p.Mesa != "" {
		where += ", mesa " + p.Mesa
	}
	if p.Scope != "" {
		where += ", escopo " + p.Scope
	}
	text := fmt.Sprintf("Entendi: %s, %s (%s). Confirma?", describeEffect(p.Effect, p.Target, p.Kind, p.Action), until, where)
	if p.Origin.Audio && p.Origin.Transcript != "" {
		text += fmt.Sprintf("\nVoz transcrita: %q", p.Origin.Transcript)
	}
	return text
}

// BeginPIN opens the request of PIN of the author for a proposal; the answer
// comes back to Confirm with the id of the request.
func (s *Service) BeginPIN(ctx context.Context, proposal, userID string) (users.PINRequest, error) {
	pr, err := s.loadProposal(ctx, s.o.DB.Reader(), proposal)
	if err != nil {
		return users.PINRequest{}, err
	}
	if pr.author != userID {
		return users.PINRequest{}, ErrNotAuthor
	}
	return s.o.PINs.StartPIN(ctx, userID, proposalAction(proposal))
}

type storedProposal struct {
	id, author, state string
	p                 Proposal
	needsPIN          bool
	expires           time.Time
}

func (s *Service) loadProposal(ctx context.Context, q queryer, id string) (storedProposal, error) {
	var sp storedProposal
	var body string
	var pin, expires int64
	err := q.QueryRowContext(ctx, `SELECT id, author_id, payload, state, needs_pin, expires_at FROM decree_proposals WHERE id = ?`, id).Scan(&sp.id, &sp.author, &body, &sp.state, &pin, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return storedProposal{}, ErrNoProposal
	}
	if err != nil {
		return storedProposal{}, err
	}
	if err := json.Unmarshal([]byte(body), &sp.p); err != nil {
		return storedProposal{}, fmt.Errorf("decrees: proposal %s: %w", id, err)
	}
	sp.needsPIN, sp.expires = pin != 0, fromMs(expires)
	return sp, nil
}

// Confirm is the click of the author on Confirmar. It is refused for anyone
// else; the level, the workspaces and every refusal are checked again against
// what is stored now; a proposal that asks the PIN needs the id of a request of
// PIN answered right (BeginPIN), and a wrong or expired one cancels it. It
// returns the decree, active, with its number.
func (s *Service) Confirm(ctx context.Context, proposal, userID, pinRequest string) (Decree, error) {
	if s.locked(ctx) {
		return Decree{}, ErrLocked
	}
	_, _, why, err := s.authorizeConfirm(ctx, s.o.DB.Reader(), proposal, userID)
	if err != nil {
		s.cancelIfRefused(ctx, proposal, err)
		return Decree{}, err
	}
	if why != "" {
		if pinRequest == "" {
			return Decree{}, ErrPINRequired
		}
		// The write of the PIN is its own transaction: it comes before ours.
		if err := s.o.PINs.Consume(ctx, pinRequest, userID, proposalAction(proposal)); err != nil {
			s.cancelProposal(ctx, proposal)
			return Decree{}, ErrPINFailed
		}
	}
	var out Decree
	var conflicts []conflict
	err = s.write(ctx, func(tx *sql.Tx) error {
		now := s.clk.Now()
		// Authorized by what is stored now, never by what was true at the proposal.
		sp, u, again, err := s.authorizeConfirm(ctx, tx, proposal, userID)
		if err != nil {
			return err
		}
		if again != "" && why == "" {
			return ErrPINRequired // the effect became stricter while it waited
		}
		d := s.build(ctx, sp.p, u, why != "", now)
		n, err := insertDecree(ctx, tx, d, "confirmado pelo autor", now)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE decree_proposals SET state = 'confirmed', decree = ? WHERE id = ? AND state = 'open'`, n, proposal)
		if err != nil {
			return err
		}
		if k, _ := res.RowsAffected(); k == 0 {
			return ErrNoProposal
		}
		if conflicts, err = s.detectConflicts(ctx, tx, n, now); err != nil {
			return err
		}
		out, err = getDecree(ctx, tx, n)
		return err
	})
	if err != nil {
		s.cancelIfRefused(ctx, proposal, err)
		return Decree{}, err
	}
	s.announce(ctx, conflicts)
	return out, nil
}

// authorizeConfirm is every check of the click: the proposal is open and
// alive, the user is its author, and the proposal still passes the checks of
// the level, of the workspaces and of the invariants for the author as stored.
// It returns why the PIN is needed ("" when it is not).
func (s *Service) authorizeConfirm(ctx context.Context, q queryer, proposal, userID string) (storedProposal, users.User, string, error) {
	sp, err := s.loadProposal(ctx, q, proposal)
	now := s.clk.Now()
	switch {
	case err != nil:
		return sp, users.User{}, "", err
	case sp.state != "open" || !now.Before(sp.expires):
		return sp, users.User{}, "", ErrNoProposal
	case sp.author != userID:
		return sp, users.User{}, "", ErrNotAuthor
	}
	u, err := readUser(ctx, q, sp.author)
	if err != nil {
		return sp, users.User{}, "", ErrNoProposal
	}
	why, ref := check(sp.p, u, now)
	if ref != nil {
		return sp, u, "", ref
	}
	return sp, u, why, nil
}

// cancelIfRefused cancels the proposal that a check refused for good.
func (s *Service) cancelIfRefused(ctx context.Context, proposal string, err error) {
	var ref *Refusal
	if errors.As(err, &ref) {
		s.cancelProposal(ctx, proposal)
	}
}

func (s *Service) cancelProposal(ctx context.Context, proposal string) {
	if err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE decree_proposals SET state = 'cancelled' WHERE id = ? AND state = 'open'`, proposal)
		return err
	}); err != nil {
		s.log.Warn("decrees: a proposal was not cancelled", "proposal", proposal, "err", err)
	}
}

// build makes the decree of a proposal.
func (s *Service) build(ctx context.Context, p Proposal, u users.User, pin bool, now time.Time) Decree {
	d := Decree{Kind: p.Kind, State: Active, Effect: p.Effect, Target: p.Target, Workspace: p.Workspace, Mesa: p.Mesa, Scope: p.Scope,
		Author: u.ID, AuthorLevel: classLevel(u.Level), Text: p.Text, Origin: p.Origin, PIN: pin, Action: p.Action, RequestType: p.RequestType,
		TargetRef: p.TargetRef, Until: p.Until}
	if p.Kind == KindException {
		d.Until = now.Add(s.ttl)
	}
	if len(p.TargetRef) > 0 {
		if imp, err := chain.Impress(p.TargetRef); err == nil {
			d.Impression = imp
		}
	}
	if d.Effect.Kind == EffectDelegation && highTypes[d.Effect.Setting] && d.Until.IsZero() {
		validity, _ := s.delegationTimes(ctx, d.Workspace)
		d.Until = now.Add(validity) // "até mudar ou 30 dias" (the file may shorten the 30)
	}
	if d.Effect.Kind == EffectDelegation && d.Effect.Setting == string(classes.RequestOperator) && d.Effect.MaxMinutes == 0 {
		d.Effect.MaxMinutes = DefaultOperatorMinutes
	}
	return d
}

// Cancel is the click of the author on Cancelar.
func (s *Service) Cancel(ctx context.Context, proposal, userID string) error {
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		sp, err := s.loadProposal(ctx, tx, proposal)
		switch {
		case err != nil:
			return err
		case sp.author != userID:
			return ErrNotAuthor
		case sp.state != "open":
			return ErrNoProposal
		}
		_, err = tx.ExecContext(ctx, `UPDATE decree_proposals SET state = 'cancelled' WHERE id = ? AND state = 'open'`, proposal)
		return err
	})
}

// conflict is a pair of decrees of the same level that oppose each other: the
// engine suspended both.
type conflict struct{ a, b Decree }

// detectConflicts finds the active permission rules that oppose decree n on the
// same target and the same point. At different levels the higher one wins at the
// decision and both stay active; at the same level (the level of the author
// now) the engine does not choose: it suspends both and waits for a decision.
func (s *Service) detectConflicts(ctx context.Context, tx queryer, n int64, now time.Time) ([]conflict, error) {
	d, err := getDecree(ctx, tx, n)
	if err != nil {
		return nil, err
	}
	if d.Kind != KindRule || d.Effect.Kind != EffectPermission || d.State != Active {
		return nil, nil
	}
	others, err := queryDecrees(ctx, tx, `WHERE state = 'active' AND kind = 'rule' AND effect_kind = 'permission' AND id <> ? AND workspace = ? AND target_kind = ? AND target = ?`,
		n, d.Workspace, string(d.Target.Kind), d.Target.Name)
	if err != nil {
		return nil, err
	}
	mine, err := s.levelOf(ctx, tx, d)
	if err != nil {
		return nil, err
	}
	var out []conflict
	for _, o := range others {
		if !sameMesa(d.Mesa, o.Mesa) || !opposed(d.Effect, o.Effect) {
			continue
		}
		theirs, err := s.levelOf(ctx, tx, o)
		if err != nil {
			return nil, err
		}
		if rank(mine) != rank(theirs) {
			continue
		}
		note := fmt.Sprintf("conflito com o decreto %d, do mesmo nível", o.Number)
		if _, err := transition(ctx, tx, d.Number, Suspended, "", note, now, Active); err != nil {
			return nil, err
		}
		if _, err := transition(ctx, tx, o.Number, Suspended, "", fmt.Sprintf("conflito com o decreto %d, do mesmo nível", d.Number), now, Active); err != nil {
			return nil, err
		}
		for _, x := range []int64{d.Number, o.Number} {
			other := o.Number
			if x == o.Number {
				other = d.Number
			}
			if _, err := tx.ExecContext(ctx, `UPDATE decrees SET suspended_by = ? WHERE id = ?`, fmt.Sprintf("conflict:%d", other), x); err != nil {
				return nil, err
			}
		}
		out = append(out, conflict{a: d, b: o})
	}
	return out, nil
}

func sameMesa(a, b string) bool { return a == "" || b == "" || a == b }

// opposed says one decree allows what the other forbids.
func opposed(a, b Effect) bool {
	return grantsOverlap(a.Allow, b.Deny) || grantsOverlap(a.Deny, b.Allow)
}

func grantsOverlap(a, b *classes.Grant) bool {
	if a == nil || b == nil {
		return false
	}
	return listsOverlap(a.Files, b.Files) || listsOverlap(a.Commands, b.Commands) || listsOverlap(a.Tools, b.Tools) || listsOverlap(a.Domains, b.Domains)
}

func listsOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if patternsOverlap(x, y) {
				return true
			}
		}
	}
	return false
}

// patternsOverlap says two patterns may take the same thing: they are the same,
// or one is a prefix with a wildcard that the other starts with.
func patternsOverlap(x, y string) bool {
	if x == y {
		return true
	}
	px, py := strings.TrimRight(x, "*"), strings.TrimRight(y, "*")
	switch {
	case px != x && strings.HasPrefix(y, px):
		return true
	case py != y && strings.HasPrefix(x, py):
		return true
	}
	return false
}

// levelOf is the level that counts for a decree now: the author's current
// level, never the one he had when it was made. An author who is gone counts
// for nothing.
func (s *Service) levelOf(ctx context.Context, q queryer, d Decree) (classes.Level, error) {
	if d.AuthorLevel == classes.LevelGrandMaster || d.AuthorLevel == classes.LevelTableMaster {
		return d.AuthorLevel, nil
	}
	u, err := readUser(ctx, q, d.Author)
	if errors.Is(err, users.ErrNoUser) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if u.State != users.Active || !u.Acts(d.Workspace) {
		return "", nil // gone, suspended, or no longer acting in the workspace of the decree
	}
	return classLevel(u.Level), nil
}

// announce tells the two authors and the Owner of each conflict.
func (s *Service) announce(ctx context.Context, cs []conflict) {
	if s.o.Alerts == nil {
		return
	}
	for _, c := range cs {
		text := fmt.Sprintf("Os decretos %d e %d, do mesmo nível, se opõem sobre o mesmo alvo (%s): os dois ficaram suspensos. Revogue um, ajuste ou reative.", c.a.Number, c.b.Number, c.a.Target.Name)
		for _, u := range []string{c.a.Author, c.b.Author} {
			s.o.Alerts.Tell(ctx, u, text)
		}
		s.o.Alerts.OwnerAlert(ctx, text)
	}
}

// Revoke is the revocation of a decree by a user: the Owner revokes any, an
// Administrator the ones of Administrators and Approvers, an Approver only his
// own exceptions not used yet; nobody revokes one of a level above his own. A
// decree that restricted something is revoked with the PIN (pinRequest).
func (s *Service) Revoke(ctx context.Context, number int64, userID, pinRequest string) error {
	if s.locked(ctx) {
		return ErrLocked
	}
	d, needPIN, err := s.authorizeRevoke(ctx, s.o.DB.Reader(), number, userID)
	if err != nil {
		return err
	}
	if needPIN {
		if pinRequest == "" {
			return ErrPINRequired
		}
		if err := s.o.PINs.Consume(ctx, pinRequest, userID, revokeAction(number)); err != nil {
			return ErrPINFailed
		}
	}
	err = s.write(ctx, func(tx *sql.Tx) error {
		// Authorized again by what is stored now.
		_, again, err := s.authorizeRevoke(ctx, tx, number, userID)
		if err != nil {
			return err
		}
		if again && !needPIN {
			return ErrPINRequired
		}
		ok, err := transition(ctx, tx, number, Revoked, userID, "revogado", s.clk.Now(), Active, Suspended)
		if err != nil {
			return err
		}
		if !ok {
			return ErrEnded
		}
		return nil
	})
	if err != nil {
		return err
	}
	if s.o.Alerts != nil && d.Author != userID {
		s.o.Alerts.Tell(ctx, d.Author, fmt.Sprintf("O decreto %d (%s) foi revogado.", d.Number, d.Summary()))
	}
	return nil
}

// authorizeRevoke says whether the user may revoke the decree, by the level
// stored now, and whether it asks the PIN.
func (s *Service) authorizeRevoke(ctx context.Context, q queryer, number int64, userID string) (Decree, bool, error) {
	by, err := readUser(ctx, q, userID)
	if err != nil || by.State != users.Active {
		return Decree{}, false, ErrNotAllowed
	}
	d, err := getDecree(ctx, q, number)
	if err != nil {
		return Decree{}, false, err
	}
	if d.State.Ended() {
		return d, false, ErrEnded
	}
	if !by.Acts(d.Workspace) {
		return d, false, ErrNotAllowed
	}
	author, err := s.levelOf(ctx, q, d)
	if err != nil {
		return d, false, err
	}
	switch users.RevokeDecrees(by.Level) {
	case users.RevokeAny:
	case users.RevokeAdminAndApprover:
		if rank(author) > rank(classes.LevelAdmin) {
			return d, false, ErrNotAllowed
		}
	case users.RevokeOwnUnused:
		if d.Author != by.ID || d.Kind != KindException || d.State != Active {
			return d, false, ErrNotAllowed
		}
	default:
		return d, false, ErrNotAllowed
	}
	return d, restricts(d.Effect), nil
}

// NeedsPINToRevoke says the decree restricted something, so revoking it
// loosens the security and asks the PIN.
func (s *Service) NeedsPINToRevoke(ctx context.Context, number int64) (bool, error) {
	d, err := getDecree(ctx, s.o.DB.Reader(), number)
	if err != nil {
		return false, err
	}
	return restricts(d.Effect), nil
}

// BeginRevokePIN opens the request of PIN for a revocation.
func (s *Service) BeginRevokePIN(ctx context.Context, number int64, userID string) (users.PINRequest, error) {
	return s.o.PINs.StartPIN(ctx, userID, revokeAction(number))
}

// restricts says the effect forbids something (a deny, a tightened policy).
func restricts(e Effect) bool {
	switch e.Kind {
	case EffectPermission:
		return !emptyGrant(e.Deny)
	case EffectPolicy:
		return !e.Loosens
	}
	return false
}

// Reactivate brings a suspended decree back; only the Owner does it, and then
// the decree is his (docs/telegram/decretos.md#autor-suspenso-ou-removido). A
// decree that opposes another active one of the same level stays suspended.
func (s *Service) Reactivate(ctx context.Context, number int64, userID string) error {
	if s.locked(ctx) {
		return ErrLocked
	}
	var cs []conflict
	err := s.write(ctx, func(tx *sql.Tx) error {
		now := s.clk.Now()
		by, err := readUser(ctx, tx, userID)
		if err != nil || by.State != users.Active || by.Level != users.Owner {
			return ErrNotAllowed
		}
		d, err := getDecree(ctx, tx, number)
		if err != nil {
			return err
		}
		if d.State != Suspended {
			return ErrEnded
		}
		if _, err := tx.ExecContext(ctx, `UPDATE decrees SET author_id = ?, author_level = ?, suspended_by = '' WHERE id = ?`, by.ID, string(classes.LevelOwner), number); err != nil {
			return err
		}
		if _, err := transition(ctx, tx, number, Active, by.ID, "reativado pelo Dono", now, Suspended); err != nil {
			return err
		}
		cs, err = s.detectConflicts(ctx, tx, number, now)
		return err
	})
	if err != nil {
		return err
	}
	s.announce(ctx, cs)
	return nil
}

// InvalidateBy is what happens to the decrees of a user who was suspended,
// removed or lost the level (users.Approvals): his exceptions not used are
// invalidated, and his permanent rules are suspended, and the Owner receives
// the list. The Owner reactivates each one (and it becomes his) or revokes it.
func (s *Service) InvalidateBy(ctx context.Context, userID string) error {
	var rules, exceptions []Decree
	err := s.write(ctx, func(tx *sql.Tx) error {
		now := s.clk.Now()
		u, err := readUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		mine, err := queryDecrees(ctx, tx, `WHERE author_id = ? AND state IN ('active', 'suspended')`, userID)
		if err != nil {
			return err
		}
		for _, d := range mine {
			switch {
			case d.Kind == KindException && (u.State != users.Active || !u.Acts(d.Workspace) || users.Require(u.Level, users.OneOffException) != nil):
				if ok, err := transition(ctx, tx, d.Number, Revoked, "", "o autor foi suspenso, removido ou perdeu o nível", now, Active, Suspended); err != nil {
					return err
				} else if ok {
					exceptions = append(exceptions, d)
				}
			case d.Kind == KindRule && d.State == Active && (u.State != users.Active || !u.Acts(d.Workspace) || users.Require(u.Level, users.PermanentDecree) != nil):
				if ok, err := transition(ctx, tx, d.Number, Suspended, "", "o autor foi suspenso, removido ou perdeu o nível", now, Active); err != nil {
					return err
				} else if ok {
					if _, err := tx.ExecContext(ctx, `UPDATE decrees SET suspended_by = 'author' WHERE id = ?`, d.Number); err != nil {
						return err
					}
					rules = append(rules, d)
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if s.o.Alerts != nil && len(rules) > 0 {
		var lines []string
		for _, d := range rules {
			lines = append(lines, fmt.Sprintf("decreto %d: %s", d.Number, d.Summary()))
		}
		s.o.Alerts.OwnerAlert(ctx, "O autor de regras permanentes saiu ou perdeu o nível, e elas ficaram suspensas. Reative (passa a ser sua) ou revogue:\n"+strings.Join(lines, "\n"))
	}
	return nil
}

// Sweep ends the decrees whose validity ran out, and tells the author. An
// exception not used past its time expires too.
func (s *Service) Sweep(ctx context.Context) error {
	if err := s.remind(ctx); err != nil {
		return err
	}
	var gone []Decree
	err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		now := s.clk.Now()
		ds, err := queryDecrees(ctx, tx, `WHERE state IN ('active', 'suspended') AND until > 0 AND until <= ?`, ms(now))
		if err != nil {
			return err
		}
		for _, d := range ds {
			ok, err := transition(ctx, tx, d.Number, Expired, "", "a validade acabou", now, Active, Suspended)
			if err != nil {
				return err
			}
			if ok {
				gone = append(gone, d)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if s.o.Alerts != nil {
		for _, d := range gone {
			what := "O decreto"
			if d.Kind == KindException {
				what = "A exceção"
			}
			s.o.Alerts.Tell(ctx, d.Author, fmt.Sprintf("%s %d (%s) expirou.", what, d.Number, d.Summary()))
		}
	}
	return nil
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
				s.log.Warn("decrees: sweep", "err", err)
			}
		}
	}
}
