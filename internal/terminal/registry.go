package terminal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mestre-ia/mestre/internal/store"
)

// Status values this layer writes. The rest of the values of the column
// belong to the character states (docs/orquestracao/personagens.md); this
// layer only creates the row as "starting" and ends it as "interrupted" or
// "stopped".
const (
	StatusStarting    = "starting"
	StatusInterrupted = "interrupted"
	StatusStopped     = "stopped"
)

// Record is a row of the terminals table (maquina.db,
// docs/arquitetura/persistencia.md#registro-de-terminais).
type Record struct {
	ID                string
	CharacterID       string
	MachineID         string
	Dir               string
	Class             string
	Status            string
	ProviderSessionID string
	SocketPath        string
	Secret            []byte
	PTYVersion        string
	// PTY is the mestre-pty process; PTY.PID is 0 until it was recorded.
	PTY ProcID
	// LogonSID is the logon SID of the terminal (Windows), told by the
	// launcher that made the token; empty elsewhere.
	LogonSID   string
	StartedAt  time.Time
	LastSeenAt time.Time
}

// Live reports whether the terminal is expected to be running.
func (r Record) Live() bool { return r.Status != StatusInterrupted && r.Status != StatusStopped }

const recordColumns = `id, character_id, machine_id, dir, class, status, provider_session_id,
	socket_path, handshake_secret, pty_version, pty_pid, pty_started, started_at, last_seen_at, logon_sid`

// registry reads and writes the terminals table.
type registry struct{ db *store.DB }

func (r registry) insert(ctx context.Context, rec Record) error {
	_, err := r.db.Writer().ExecContext(ctx, `INSERT INTO terminals (`+recordColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, ?, ?, NULL)`,
		rec.ID, rec.CharacterID, rec.MachineID, rec.Dir, nullable(rec.Class), rec.Status, nullable(rec.ProviderSessionID),
		rec.SocketPath, rec.Secret, rec.PTYVersion, rec.StartedAt.UnixMilli(), rec.LastSeenAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("insert terminal %s: %w", rec.ID, err)
	}
	return nil
}

func (r registry) setProcess(ctx context.Context, id string, p ProcID) error {
	_, err := r.db.Writer().ExecContext(ctx, `UPDATE terminals SET pty_pid = ?, pty_started = ? WHERE id = ?`, p.PID, p.Start, id)
	if err != nil {
		return fmt.Errorf("record mestre-pty of terminal %s: %w", id, err)
	}
	return nil
}

func (r registry) setLogon(ctx context.Context, id, sid string) error {
	_, err := r.db.Writer().ExecContext(ctx, `UPDATE terminals SET logon_sid = ? WHERE id = ?`, sid, id)
	if err != nil {
		return fmt.Errorf("record the logon SID of terminal %s: %w", id, err)
	}
	return nil
}

func (r registry) setStatus(ctx context.Context, id, status string, seen time.Time) error {
	_, err := r.db.Writer().ExecContext(ctx, `UPDATE terminals SET status = ?, last_seen_at = ? WHERE id = ?`, status, seen.UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("set status of terminal %s: %w", id, err)
	}
	return nil
}

func (r registry) touch(ctx context.Context, id string, seen time.Time) error {
	_, err := r.db.Writer().ExecContext(ctx, `UPDATE terminals SET last_seen_at = ? WHERE id = ?`, seen.UnixMilli(), id)
	return err
}

func (r registry) delete(ctx context.Context, id string) error {
	_, err := r.db.Writer().ExecContext(ctx, `DELETE FROM terminals WHERE id = ?`, id)
	return err
}

// ErrNoTerminal means the registry has no such terminal.
var ErrNoTerminal = errors.New("no such terminal")

func (r registry) get(ctx context.Context, id string) (Record, error) {
	rows, err := r.db.Reader().QueryContext(ctx, `SELECT `+recordColumns+` FROM terminals WHERE id = ?`, id)
	if err != nil {
		return Record{}, err
	}
	recs, err := scanRecords(rows)
	if err != nil {
		return Record{}, err
	}
	if len(recs) == 0 {
		return Record{}, ErrNoTerminal
	}
	return recs[0], nil
}

func (r registry) list(ctx context.Context) ([]Record, error) {
	rows, err := r.db.Reader().QueryContext(ctx, `SELECT `+recordColumns+` FROM terminals ORDER BY started_at, id`)
	if err != nil {
		return nil, err
	}
	return scanRecords(rows)
}

func scanRecords(rows *sql.Rows) ([]Record, error) {
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var (
			rec                        Record
			class, providerSess, logon sql.NullString
			pid                        sql.NullInt64
			started                    sql.NullString
			startedAt, seenAt          int64
		)
		if err := rows.Scan(&rec.ID, &rec.CharacterID, &rec.MachineID, &rec.Dir, &class, &rec.Status, &providerSess,
			&rec.SocketPath, &rec.Secret, &rec.PTYVersion, &pid, &started, &startedAt, &seenAt, &logon); err != nil {
			return nil, fmt.Errorf("scan terminal: %w", err)
		}
		rec.Class, rec.ProviderSessionID, rec.LogonSID = class.String, providerSess.String, logon.String
		rec.PTY = ProcID{PID: int(pid.Int64), Start: started.String}
		rec.StartedAt, rec.LastSeenAt = time.UnixMilli(startedAt).UTC(), time.UnixMilli(seenAt).UTC()
		out = append(out, rec)
	}
	return out, rows.Err()
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
