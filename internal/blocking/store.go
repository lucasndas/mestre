package blocking

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"
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
	// Msg is the same parts as messages, for whoever reads them in his language;
	// the text fields above are their pt-BR form, and all that an old row has.
	Msg Frame `json:"-"`
}

// Frame is what a notice keeps as messages (blocking_notices.msg): the frame of the
// engine around the words of the agent, which are parameters, masked and never translated.
type Frame struct {
	Summary i18n.Msg   `json:"summary"`
	Full    i18n.Msg   `json:"full"`
	Owner   i18n.Msg   `json:"owner"`
	Steps   []i18n.Msg `json:"steps,omitempty"`
	Agent   i18n.Msg   `json:"agent"`
}

// IsZero says the notice has no messages: a row written before them.
func (f Frame) IsZero() bool { return f.Full.IsZero() && f.Summary.IsZero() }

// StepPart is a step of "para destravar".
type StepPart struct {
	Text string   `json:"text"`
	Kind StepKind `json:"kind"`
}

// plainPt is a message in pt-BR, the form of the old columns.
func plainPt(m i18n.Msg) string { return strings.TrimLeft(i18n.RenderOrEmpty(i18n.PtBR, m), "\n") }

// PartsOf makes the parts of a notice, masked.
func PartsOf(n Notice, mask func(string) string) Parts {
	agent := AgentLines(n, mask)
	p := Parts{Owner: engineLine(plainPt(n.Owner)), Agent: plainPt(agent)}
	p.Msg = Frame{Owner: n.Owner, Agent: agent}
	if agent.Code == "msg.text" { // nothing was written
		p.Msg.Agent, p.Agent = i18n.Msg{}, ""
	}
	if n.Stopped != "" {
		p.Stopped = maskedLine(n.Stopped, mask)
	}
	if ls := lines(mask(n.Evidence)); len(ls) > 0 {
		p.Evidence = safeBlock(strings.Join(ls, "\n"))
	}
	for _, s := range n.Steps {
		p.Steps = append(p.Steps, StepPart{Text: maskedLine(plainPt(s.Text), mask), Kind: s.Kind})
		p.Msg.Steps = append(p.Msg.Steps, s.Text)
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
	p.Msg.Summary = i18n.Text(Line(summary, mask))
	p.Msg.Full = Full(n, mask)
	frame, err := json.Marshal(p.Msg)
	if err != nil {
		return 0, err
	}
	steps, err := json.Marshal(append([]StepPart{}, p.Steps...))
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO blocking_notices (workspace, mesa, motive, summary, full, machine, created_at, stopped, evidence, steps, owner, agent, msg)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			n.Workspace, n.Mesa, string(n.Motive), Line(summary, mask), plainPt(Full(n, mask)), n.MachineLine(), s.Clock.Now().UnixMilli(),
			p.Stopped, p.Evidence, string(steps), p.Owner, p.Agent, string(frame))
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

const savedColumns = `id, workspace, mesa, motive, summary, full, created_at, stopped, evidence, steps, owner, agent, msg`

func scanSaved(sc interface{ Scan(...any) error }) (Saved, error) {
	var v Saved
	var motive, steps, frame string
	var at int64
	if err := sc.Scan(&v.ID, &v.Workspace, &v.Mesa, &motive, &v.Summary, &v.Full, &at, &v.Stopped, &v.Evidence, &steps, &v.Owner, &v.Agent, &frame); err != nil {
		return Saved{}, err
	}
	if frame != "" {
		if err := json.Unmarshal([]byte(frame), &v.Msg); err != nil {
			return Saved{}, err
		}
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
