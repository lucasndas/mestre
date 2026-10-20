package decrees

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lucasndas/mestre/internal/users"
)

// Reminder tells the author of a delegation that it is about to end and offers
// Renovar and Deixar vencer (docs/aprovacoes/delegacao.md#delegação-de-decisão).
type Reminder interface {
	DelegationEnding(ctx context.Context, d Decree)
}

// RevokeReport is what "Revogar todas as delegações" took away.
type RevokeReport struct {
	By             users.User
	At             time.Time
	Decrees        []Decree
	Subdelegations int
	// Rules is how many temporary table rules were revoked, per workspace.
	Rules map[string]int
}

// RevokeHooks are the pieces of the emergency button the decrees do not own:
// the temporary table rules, the open requests that leaned on a delegation, and
// the Administrators and the Grão-Mestre of each workspace to tell.
type RevokeHooks interface {
	// RevokeRules revokes the temporary table rules of a workspace.
	RevokeRules(ctx context.Context, workspace, by string) (int, error)
	// Reroute takes the open requests that waited at a level that depended on a
	// delegation to the level above.
	Reroute(ctx context.Context, workspace string) error
	// Told tells the Administrators and the Grão-Mestre of the workspace.
	Told(ctx context.Context, workspace, text string)
}

// SetDelegationTimes tells the Service where the validity a delegation gets by
// default and the reminder before its end are read for a workspace (the
// `delegacao` of its policy file). Without it the constants rule.
func (s *Service) SetDelegationTimes(f func(ctx context.Context, workspace string) (validity, reminder time.Duration)) {
	s.delegation = f
}

// delegationTimes are the validity and the reminder of a workspace: what the
// policy file says, never more than 30 days of validity.
func (s *Service) delegationTimes(ctx context.Context, workspace string) (validity, reminder time.Duration) {
	validity, reminder = MaxDelegation, s.reminderBefore()
	if s.delegation == nil {
		return validity, reminder
	}
	v, r := s.delegation(ctx, workspace)
	if v > 0 && v < validity {
		validity = v
	}
	if r > 0 {
		reminder = r
	}
	return validity, reminder
}

func (s *Service) reminderBefore() time.Duration {
	if s.o.ReminderBefore > 0 {
		return s.o.ReminderBefore
	}
	return DefaultReminderBefore
}

// remind sends the reminder of the delegations that end soon, once each.
func (s *Service) remind(ctx context.Context) error {
	now := s.clk.Now()
	var soon []Decree
	err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		ds, err := queryDecrees(ctx, tx, `WHERE state = 'active' AND effect_kind = 'delegation' AND until > ? AND until <= ? AND reminded_at = 0`, ms(now), ms(now.Add(MaxDelegation)))
		if err != nil {
			return err
		}
		for _, d := range ds {
			if !highTypes[d.Effect.Setting] {
				continue // the delegation of the Operator is short and has no reminder
			}
			if _, before := s.delegationTimes(ctx, d.Workspace); d.Until.Sub(now) > before {
				continue // not yet the time of this workspace's reminder
			}
			if _, err := tx.ExecContext(ctx, `UPDATE decrees SET reminded_at = ? WHERE id = ?`, ms(now), d.Number); err != nil {
				return err
			}
			soon = append(soon, d)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, d := range soon {
		switch {
		case s.o.Reminder != nil:
			s.o.Reminder.DelegationEnding(ctx, d)
		case s.o.Alerts != nil:
			s.o.Alerts.Tell(ctx, d.Author, fmt.Sprintf("A delegação %d (%s) acaba em %s.", d.Number, d.Summary(), s.Moment(ctx, d.Until)))
		}
	}
	return nil
}

// Renew is the click on Renovar: the same delegation for 30 more days, as a new
// proposal the author confirms with the PIN. The old one ends on its own date.
func (s *Service) Renew(ctx context.Context, number int64, userID string) (Pending, error) {
	d, err := getDecree(ctx, s.o.DB.Reader(), number)
	if err != nil {
		return Pending{}, err
	}
	if d.Author != userID || d.State != Active || d.Effect.Kind != EffectDelegation || !highTypes[d.Effect.Setting] {
		return Pending{}, ErrNotAllowed
	}
	validity, _ := s.delegationTimes(ctx, d.Workspace)
	return s.Propose(ctx, Proposal{Author: userID, Kind: KindRule, Effect: d.Effect, Target: d.Target, Workspace: d.Workspace, Mesa: d.Mesa, Scope: d.Scope,
		Until: s.clk.Now().Add(validity), Text: fmt.Sprintf("Renovar a delegação %d por mais %d dias: %s", d.Number, int(validity.Hours()/24), d.Text),
		Origin: Origin{Channel: "renewal"}})
}

// RevokeAllDelegations is the emergency button of the Owner: every delegation (of
// decision, on GitHub, of the Operator) and every subdelegation, in every
// workspace, ends in one transaction, and so do the temporary table rules. It
// only restricts, so it asks no PIN and works even locked
// (docs/aprovacoes/delegacao.md#revogar-tudo).
func (s *Service) RevokeAllDelegations(ctx context.Context, userID string) (RevokeReport, error) {
	rep := RevokeReport{Rules: map[string]int{}}
	var workspaces []string
	err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		now := s.clk.Now()
		u, err := readUser(ctx, tx, userID)
		if err != nil {
			return ErrNotAllowed
		}
		if u.State != users.Active || users.Require(u.Level, users.DelegateHigh) != nil {
			return ErrNotAllowed
		}
		rep.By, rep.At = u, now
		res, err := tx.ExecContext(ctx, `UPDATE subdelegations SET state = 'revoked', ended_at = ?, ended_by = ?, ended_note = 'revogadas todas as delegações' WHERE state = 'active'`, ms(now), u.ID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		rep.Subdelegations = int(n)
		ds, err := queryDecrees(ctx, tx, `WHERE state IN ('active', 'suspended') AND effect_kind = 'delegation'`)
		if err != nil {
			return err
		}
		for _, d := range ds {
			if ok, err := transition(ctx, tx, d.Number, Revoked, u.ID, "revogadas todas as delegações", now, Active, Suspended); err != nil {
				return err
			} else if ok {
				rep.Decrees = append(rep.Decrees, d)
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT slug FROM workspaces`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var w string
			if err := rows.Scan(&w); err != nil {
				return err
			}
			workspaces = append(workspaces, w)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO delegation_revocations (at, by_user, decrees, subdelegations) VALUES (?, ?, ?, ?)`, ms(now), u.ID, len(rep.Decrees), rep.Subdelegations)
		return err
	})
	if err != nil {
		return RevokeReport{}, err
	}
	if h := s.o.Revoked; h != nil {
		for _, w := range workspaces {
			n, err := h.RevokeRules(ctx, w, rep.By.ID)
			if err != nil {
				s.log.Warn("decrees: the temporary rules were not all revoked", "workspace", w, "err", err)
			}
			if n > 0 {
				rep.Rules[w] = n
			}
			if err := h.Reroute(ctx, w); err != nil {
				s.log.Warn("decrees: the open requests were not rerouted", "workspace", w, "err", err)
			}
			h.Told(ctx, w, fmt.Sprintf("%s revogou todas as delegações e regras temporárias (%d delegações, %d subdelegações, %d regras temporárias).", rep.By.Name, countIn(rep.Decrees, w), rep.Subdelegations, n))
		}
	}
	return rep, nil
}

func countIn(ds []Decree, workspace string) int {
	n := 0
	for _, d := range ds {
		if d.Workspace == workspace {
			n++
		}
	}
	return n
}

// SetReminder and SetRevokeHooks bind the pieces made after the service (the
// Telegram channel needs the service first). Call them before the service runs.
func (s *Service) SetReminder(r Reminder) { s.o.Reminder = r }

// SetRevokeHooks: see SetReminder.
func (s *Service) SetRevokeHooks(h RevokeHooks) { s.o.Revoked = h }

// SetTableOpen binds the question "is this table open?" (the tables are made
// after the service). Call it before the service runs.
func (s *Service) SetTableOpen(f func(ctx context.Context, workspace, table string) (bool, error)) {
	s.o.TableOpen = f
}
