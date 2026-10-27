package decrees

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/users"
)

// MaxDelegation is the longest validity of a delegation of decision and of a
// subdelegation, and the default of the first (docs/aprovacoes/delegacao.md).
const MaxDelegation = 30 * 24 * time.Hour

// DefaultReminderBefore is how long before the end of a delegation its author
// is reminded.
const DefaultReminderBefore = 3 * 24 * time.Hour

// The codes of the refusals of a subdelegation (docs/cli/grao-mestre.md#mestre-approval).
const (
	CodeLevelDenied = "level_denied"
	CodeTooWide     = "delegation_too_wide"
)

// SubRequest is what the Grand Master asks in `mestre approval delegate`: a
// part of what it decides, for the Mestre da mesa of Mesa, until Until.
type SubRequest struct {
	// By is the character that calls; only the Grand Master may.
	By characters.Character
	// Grantee is the Mestre da mesa the delegation is for.
	Grantee characters.Character
	Mesa    string
	Type    string
	Until   time.Time
	Reason  string
}

// Sub is a subdelegation as it is kept.
type Sub struct {
	ID        int64
	Workspace string
	Mesa      string
	Type      string
	Grantor   string
	Grantee   string
	// Origin is the decree of delegation it comes from; 0 is the ceiling of the
	// Grand Master.
	Origin    int64
	Reason    string
	Active    bool
	CreatedAt time.Time
	Until     time.Time
	EndedAt   time.Time
	EndedBy   string
	EndedNote string
}

// Subdelegate gives the Mestre da mesa of a table a part of what the Grand
// Master decides, only there and narrower than its own reach: the medio, inside
// the policy, or a type alto under a delegation of the Owner that covers the
// table. It never lasts longer than the origin, than 30 days or than the table
// (docs/aprovacoes/delegacao.md#subdelegação). Depth is one: only the Grand
// Master gives it.
func (s *Service) Subdelegate(ctx context.Context, in SubRequest) (Sub, error) {
	switch {
	case in.By.Role != characters.RoleGrandMaster:
		return Sub{}, refuse(CodeLevelDenied, "só o Grão-Mestre subdelega; o Mestre da mesa não passa adiante o que recebeu")
	case in.Grantee.Role != characters.RoleTableMaster || in.Grantee.Workspace != in.By.Workspace || in.Mesa == "" || in.Grantee.Table != in.Mesa:
		return Sub{}, refuse(CodeTooWide, "a subdelegação é para o Mestre da mesa %q, só na mesa dele", in.Mesa)
	case in.Type == "" || fixedTypes[in.Type] || in.Type == string(classes.RequestClassPermission):
		return Sub{}, refuse(CodeTooWide, "o tipo %q não se subdelega", in.Type)
	}
	now := s.clk.Now()
	if !in.Until.After(now) || in.Until.Sub(now) > MaxDelegation {
		return Sub{}, refuse(CodeTooWide, "a validade da subdelegação vai até 30 dias, e nunca além da origem")
	}
	if s.o.TableOpen != nil {
		open, err := s.o.TableOpen(ctx, in.By.Workspace, in.Mesa)
		if err != nil {
			return Sub{}, err
		}
		if !open {
			return Sub{}, refuse(CodeTooWide, "a mesa %q não está aberta: a subdelegação nunca dura mais que a mesa", in.Mesa)
		}
	}
	var origin int64
	if highTypes[in.Type] {
		d, ok, err := s.delegationFor(ctx, chain.Request{Workspace: in.By.Workspace, Table: in.Mesa, Type: classes.RequestType(in.Type)})
		if err != nil {
			return Sub{}, err
		}
		if !ok {
			return Sub{}, refuse(CodeTooWide, "o Grão-Mestre não tem delegação do Senhor da Guilda para %q na mesa %q: não há o que subdelegar", in.Type, in.Mesa)
		}
		if !d.Until.IsZero() && in.Until.After(d.Until) {
			return Sub{}, refuse(CodeTooWide, "a subdelegação não vai além da origem (decreto %d, até %s)", d.Number, s.Moment(ctx, d.Until))
		}
		origin = d.Number
	}
	var id int64
	err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if origin != 0 {
			// The origin must still stand when it is written.
			d, err := getDecree(ctx, tx, origin)
			if err != nil {
				return err
			}
			if d.State != Active {
				return refuse(CodeTooWide, "a delegação de origem acabou")
			}
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO subdelegations (workspace, mesa, request_type, grantor, grantee, origin, reason, state, created_at, until) VALUES (?, ?, ?, ?, ?, ?, ?, 'active', ?, ?)`,
			in.By.Workspace, in.Mesa, in.Type, in.By.ID, in.Grantee.ID, origin, in.Reason, ms(now), ms(in.Until))
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return Sub{}, err
	}
	return s.Sub(ctx, id)
}

const subColumns = `id, workspace, mesa, request_type, grantor, grantee, origin, reason, state, created_at, until, ended_at, ended_by, ended_note`

func scanSub(sc scanner) (Sub, error) {
	var x Sub
	var state string
	var created, until, ended int64
	if err := sc.Scan(&x.ID, &x.Workspace, &x.Mesa, &x.Type, &x.Grantor, &x.Grantee, &x.Origin, &x.Reason, &state, &created, &until, &ended, &x.EndedBy, &x.EndedNote); err != nil {
		return Sub{}, err
	}
	x.Active = state == "active"
	x.CreatedAt, x.Until, x.EndedAt = fromMs(created), fromMs(until), fromMs(ended)
	return x, nil
}

// Sub is one subdelegation.
func (s *Service) Sub(ctx context.Context, id int64) (Sub, error) {
	return scanSub(s.o.DB.Reader().QueryRowContext(ctx, `SELECT `+subColumns+` FROM subdelegations WHERE id = ?`, id))
}

// Subs lists the subdelegations of a workspace; live keeps the ones in force.
func (s *Service) Subs(ctx context.Context, workspace string, live bool) ([]Sub, error) {
	q := `SELECT ` + subColumns + ` FROM subdelegations WHERE workspace = ?`
	if live {
		q += ` AND state = 'active' AND until > ` + fmt.Sprint(ms(s.clk.Now()))
	}
	rows, err := s.o.DB.Reader().QueryContext(ctx, q+` ORDER BY id`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sub
	for rows.Next() {
		x, err := scanSub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// RevokeSub ends a subdelegation: the Grand Master that gave it or a person of
// Administrator or above (by is the id of the user or of the character).
func (s *Service) RevokeSub(ctx context.Context, id int64, by string, isPerson bool) error {
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		x, err := scanSub(tx.QueryRowContext(ctx, `SELECT `+subColumns+` FROM subdelegations WHERE id = ?`, id))
		if err != nil {
			return err
		}
		if !x.Active {
			return ErrEnded
		}
		if isPerson {
			u, err := readUser(ctx, tx, by)
			if err != nil || u.State != users.Active || !u.Acts(x.Workspace) || rank(classLevel(u.Level)) < rank(classes.LevelAdmin) {
				return ErrNotAllowed
			}
		} else if by != x.Grantor {
			return ErrNotAllowed
		}
		_, err = tx.ExecContext(ctx, `UPDATE subdelegations SET state = 'revoked', ended_at = ?, ended_by = ?, ended_note = 'revogada' WHERE id = ?`, ms(s.clk.Now()), by, id)
		return err
	})
}

// endSubsOf ends the subdelegations of a decree that is over.
func endSubsOf(ctx context.Context, tx queryer, origin int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE subdelegations SET state = 'revoked', ended_at = ?, ended_by = '', ended_note = 'a origem acabou' WHERE origin = ? AND state = 'active'`, ms(now), origin)
	return err
}

var _ chain.SubDelegations = (*Service)(nil)

// SubCovers is the chain.SubDelegations: an active subdelegation to this
// character, for this type, in the table of the request, inside its validity,
// whose origin still covers the request, and a table still open, lets the
// Mestre da mesa decide it.
func (s *Service) SubCovers(ctx context.Context, r chain.Request, character string) (bool, error) {
	_, ok, err := s.subFor(ctx, r, character)
	return ok, err
}

func (s *Service) subFor(ctx context.Context, r chain.Request, character string) (Sub, bool, error) {
	rows, err := s.o.DB.Reader().QueryContext(ctx, `SELECT `+subColumns+` FROM subdelegations WHERE state = 'active' AND workspace = ? AND mesa = ? AND request_type = ? AND grantee = ? AND until > ? ORDER BY id`,
		r.Workspace, r.Table, string(r.Type), character, ms(s.clk.Now()))
	if err != nil {
		return Sub{}, false, err
	}
	var cands []Sub
	for rows.Next() {
		x, err := scanSub(rows)
		if err != nil {
			_ = rows.Close()
			return Sub{}, false, err
		}
		cands = append(cands, x)
	}
	if err := rows.Close(); err != nil {
		return Sub{}, false, err
	}
	for _, x := range cands {
		// Never more than the Grão-Mestre has: the ceiling gives the medio, and
		// only a delegation of the Owner gives the alto.
		if x.Origin == 0 && r.Risk.Risk != classes.RiskLow && r.Risk.Risk != classes.RiskMedium {
			continue
		}
		if x.Origin != 0 && (r.Risk.Risk != classes.RiskHigh || !highTypes[string(r.Type)]) {
			continue
		}
		if x.Origin != 0 {
			d, ok, err := s.delegationFor(ctx, r)
			if err != nil {
				return Sub{}, false, err
			}
			if !ok || d.Number != x.Origin {
				continue
			}
		}
		if s.o.TableOpen != nil {
			if open, err := s.o.TableOpen(ctx, r.Workspace, r.Table); err != nil || !open {
				continue
			}
		}
		return x, true, nil
	}
	return Sub{}, false, nil
}

// SubUsed registers a decision of the Mestre da mesa under a subdelegation.
func (s *Service) SubUsed(ctx context.Context, r chain.Request, character string) error {
	x, ok, err := s.subFor(ctx, r, character)
	if err != nil || !ok {
		return err
	}
	_, err = s.o.DB.Writer().ExecContext(ctx, `INSERT INTO subdelegation_uses (subdelegation, at, character, action) VALUES (?, ?, ?, ?)`,
		x.ID, ms(s.clk.Now()), character, fmt.Sprintf("pedido %d (%s)", r.ID, r.Type))
	return err
}

// SubUses is the number of decisions taken under a subdelegation.
func (s *Service) SubUses(ctx context.Context, id int64) (int, error) {
	var n int
	err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM subdelegation_uses WHERE subdelegation = ?`, id).Scan(&n)
	return n, err
}
