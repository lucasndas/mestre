package blocking

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Parts are the parts of a notice that blocks as the panel shows them, all of them
// masked: the whole evidence (the Telegram cuts it at three lines), the steps of
// the engine, who acts and the block of the text of the agent. Nothing here is the
// secret the engine had: it is what Build and Full already put through the mask.
type Parts struct {
	// Stopped is what is missing, in one line ("" when the engine only knows the motive).
	Stopped string `json:"stopped"`
	// Evidence is every line of it, masked and inside no fence.
	Evidence string     `json:"evidence"`
	Steps    []StepPart `json:"steps"`
	// Owner is who acts.
	Owner string `json:"owner"`
	// Agent is the text of the agent, one safe line per field, for the block
	// "texto do agente, não verificado".
	Agent string `json:"agent"`
}

// StepPart is a step of "para destravar".
type StepPart struct {
	Text string   `json:"text"`
	Kind StepKind `json:"kind"`
}

// PartsOf makes the parts of a notice, masked.
func PartsOf(n Notice, mask func(string) string) Parts {
	p := Parts{Owner: engineLine(n.Owner), Agent: strings.TrimRight(agentBlock(n, mask), "\n")}
	if n.Stopped != "" {
		p.Stopped = maskedLine(n.Stopped, mask)
	}
	if ls := lines(mask(n.Evidence)); len(ls) > 0 {
		p.Evidence = safeBlock(strings.Join(ls, "\n"))
	}
	for _, s := range n.Steps {
		p.Steps = append(p.Steps, StepPart{Text: maskedLine(s.Text, mask), Kind: s.Kind})
	}
	return p
}

// CanDo says whether a user of the level may do the step the panel can do for it:
// registering a secret is the Owner's alone (docs/plataforma/cofre.md#uso), and the
// PIN is asked where it is done.
func CanDo(k StepKind, level users.Level) bool { return k == StepVault && level == users.Owner }

// Saved is a notice that blocks, as it was kept.
type Saved struct {
	ID        int64
	Workspace string
	Mesa      string
	Motive    Motive
	// Summary is the line the Telegram showed first.
	Summary string
	// Full is the whole text, masked, that "Ver detalhes" shows.
	Full string
	Parts
	At time.Time
}

// Store keeps the notices that block (blocking_notices), for the Telegram and the panel.
type Store struct {
	DB    *store.DB
	Clock clock.Clock
}

// ErrNoNotice is what Get answers for a number that is not a notice.
var ErrNoNotice = errors.New("blocking: no such notice")

// Save keeps the notice, masked, and returns its number.
func (s *Store) Save(ctx context.Context, n Notice, mask func(string) string) (int64, error) {
	summary := n.Stopped
	if summary == "" {
		summary = string(n.Motive)
	}
	p := PartsOf(n, mask)
	steps, err := json.Marshal(append([]StepPart{}, p.Steps...))
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO blocking_notices (workspace, mesa, motive, summary, full, machine, created_at, stopped, evidence, steps, owner, agent)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			n.Workspace, n.Mesa, string(n.Motive), Line(summary, mask), Full(n, mask), n.MachineLine(), s.Clock.Now().UnixMilli(),
			p.Stopped, p.Evidence, string(steps), p.Owner, p.Agent)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

const savedColumns = `id, workspace, mesa, motive, summary, full, created_at, stopped, evidence, steps, owner, agent`

func scanSaved(sc interface{ Scan(...any) error }) (Saved, error) {
	var v Saved
	var motive, steps string
	var at int64
	if err := sc.Scan(&v.ID, &v.Workspace, &v.Mesa, &motive, &v.Summary, &v.Full, &at, &v.Stopped, &v.Evidence, &steps, &v.Owner, &v.Agent); err != nil {
		return Saved{}, err
	}
	v.Motive, v.At = Motive(motive), time.UnixMilli(at).UTC()
	if err := json.Unmarshal([]byte(steps), &v.Steps); err != nil {
		return Saved{}, err
	}
	return v, nil
}

// Get is a notice by its number.
func (s *Store) Get(ctx context.Context, id int64) (Saved, error) {
	v, err := scanSaved(s.DB.Reader().QueryRowContext(ctx, `SELECT `+savedColumns+` FROM blocking_notices WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Saved{}, ErrNoNotice
	}
	return v, err
}

// Since lists the notices made at or after the time, newest first.
func (s *Store) Since(ctx context.Context, since time.Time) ([]Saved, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+savedColumns+` FROM blocking_notices WHERE created_at >= ? ORDER BY id DESC`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Saved
	for rows.Next() {
		v, err := scanSaved(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Sees says whether a user is one of those the notice goes to: active, acting in
// the workspace, and the Owner or an Administrator (as the Telegram sends it).
func Sees(u users.User, workspace string) bool {
	return u.State == users.Active && u.Acts(workspace) && (u.Level == users.Owner || u.Level == users.Administrator)
}
