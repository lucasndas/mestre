package guild

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/transport"
)

// MachineState is where a machine stands in the registry.
type MachineState string

// The states of a machine (docs/guilda/guilda.md#credenciais).
const (
	MachineActive  MachineState = "active"
	MachineRemoved MachineState = "removed" // removed by the Owner
	MachineLeft    MachineState = "left"    // uninstalled by itself
)

// Machine is a row of the registry of the machines of the Guild.
type Machine struct {
	ID       string
	Name     string
	Instance string
	// Priority orders the machines for the choice of the leading Tower.
	Priority int
	// Excluded machines keep no secrets and are never promoted.
	Excluded   bool
	State      MachineState
	Hostname   string
	IPs        []net.IP
	ExtraNames []string
	JoinedAt   time.Time
	RemovedAt  time.Time
}

// Names are the names the certificates of the machine carry.
func (m Machine) Names() transport.Names {
	return transport.Names{MachineID: m.ID, Hostname: m.Hostname, IPs: m.IPs, Extra: m.ExtraNames, Excluded: m.Excluded}
}

type namesDoc struct {
	Hostname string   `json:"hostname"`
	IPs      []string `json:"ips,omitempty"`
	Extra    []string `json:"extra,omitempty"`
}

func encodeNames(hostname string, ips []net.IP, extra []string) string {
	d := namesDoc{Hostname: hostname, Extra: extra}
	for _, ip := range ips {
		d.IPs = append(d.IPs, ip.String())
	}
	b, _ := json.Marshal(d) // a namesDoc always encodes
	return string(b)
}

const machineColumns = `machine_id, name, instance, priority, excluded, state, names, joined_at, removed_at`

type scanner interface{ Scan(dest ...any) error }

func scanMachine(r scanner) (Machine, error) {
	var (
		m        Machine
		excluded int
		state    string
		names    string
		joined   int64
		removed  sql.NullInt64
	)
	if err := r.Scan(&m.ID, &m.Name, &m.Instance, &m.Priority, &excluded, &state, &names, &joined, &removed); err != nil {
		return Machine{}, err
	}
	var d namesDoc
	if err := json.Unmarshal([]byte(names), &d); err != nil {
		return Machine{}, fmt.Errorf("guild: names of machine %s: %w", m.ID, err)
	}
	m.Excluded, m.State, m.Hostname, m.ExtraNames = excluded != 0, MachineState(state), d.Hostname, d.Extra
	for _, s := range d.IPs {
		if ip := net.ParseIP(s); ip != nil {
			m.IPs = append(m.IPs, ip)
		}
	}
	m.JoinedAt = time.UnixMilli(joined).UTC()
	if removed.Valid {
		m.RemovedAt = time.UnixMilli(removed.Int64).UTC()
	}
	return m, nil
}

// querier is what *sql.DB and *sql.Tx share.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

var errNoMachine = errors.New("guild: no such machine")

func loadMachine(ctx context.Context, q querier, id string) (Machine, error) {
	m, err := scanMachine(q.QueryRowContext(ctx, `SELECT `+machineColumns+` FROM machines WHERE machine_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Machine{}, fmt.Errorf("%w: %s", errNoMachine, id)
	}
	return m, err
}

func listMachines(ctx context.Context, q querier) ([]Machine, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+machineColumns+` FROM machines ORDER BY joined_at, machine_id`)
	if err != nil {
		return nil, fmt.Errorf("guild: list the machines: %w", err)
	}
	defer rows.Close()
	var out []Machine
	for rows.Next() {
		m, err := scanMachine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// guildRow is the one row of the guild table.
type guildRow struct {
	GuildID  string
	CA       []byte
	Epoch    protocol.Certificate
	Timezone string
}

// ErrNoGuild: the Guild database holds no Guild yet.
var ErrNoGuild = errors.New("guild: there is no Guild in this database")

func readGuild(ctx context.Context, q querier) (guildRow, error) {
	var g guildRow
	var cert string
	err := q.QueryRowContext(ctx, `SELECT guild_id, ca_cert, epoch_certificate, timezone FROM guild WHERE id = 1`).
		Scan(&g.GuildID, &g.CA, &cert, &g.Timezone)
	if errors.Is(err, sql.ErrNoRows) {
		return guildRow{}, ErrNoGuild
	}
	if err != nil {
		return guildRow{}, fmt.Errorf("guild: read the Guild: %w", err)
	}
	if g.Epoch, err = decodeCert(cert); err != nil {
		return guildRow{}, err
	}
	return g, nil
}

// insertCredential records a certificate the CA issued.
func insertCredential(ctx context.Context, q querier, machineID string, role string, cert []byte, serial string, notAfter, issuedAt time.Time) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO machine_credentials (serial, machine_id, role, cert, not_after, issued_at) VALUES (?, ?, ?, ?, ?, ?)`,
		serial, machineID, role, cert, notAfter.UnixMilli(), issuedAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("guild: record a credential: %w", err)
	}
	return nil
}

// roleOf is the name of a certificate kind in cert.renew and in
// machine_credentials.
func roleOf(k transport.Kind) string {
	if k == transport.KindGuardian {
		return "guardiao"
	}
	return "motor"
}
