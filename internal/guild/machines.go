package guild

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/transport"
)

// Errors of the registry.
var (
	// ErrNotActive: the machine is not an active machine of the Guild.
	ErrNotActive = errors.New("guild: the machine is not active in the Guild")
	// ErrCitadelProtected: the Citadel cannot be excluded or removed; the
	// Guild hands the Citadel to another machine first.
	ErrCitadelProtected = errors.New("guild: the Citadel cannot be excluded or removed")
	// ErrExcludedMachine: an excluded machine is never promoted.
	ErrExcludedMachine = errors.New("guild: an excluded machine is not promoted")
	// ErrTowersRemain: the Citadel cannot be uninstalled while Towers are in
	// the Guild (docs/guilda/guilda.md#saída-e-remoção).
	ErrTowersRemain = errors.New("guild: pass the Citadel to another machine before uninstalling it")
	// ErrExpiredCredential: the certificate is expired; the machine needs a
	// new entry (docs/guilda/guilda.md#credenciais).
	ErrExpiredCredential = errors.New("guild: the certificate expired; the machine needs a new entry")
	// ErrRenewRefused: the renewal came with the key already in use, or the
	// names in the registry are not allowed in a certificate.
	ErrRenewRefused = errors.New("guild: renewal refused")
)

// Machines lists the registry, oldest entry first.
func (c *Citadel) Machines(ctx context.Context) ([]Machine, error) {
	return listMachines(ctx, c.db.Reader())
}

// PendingJoin is an entry request that still waits for the Owner, as /guilda
// shows it.
type PendingJoin struct {
	ID string
	// Name is what the machine said about itself (never trusted as markup).
	Name string
	// Code is the code to compare with the installer screen ("" until the
	// machine revealed its nonce).
	Code      string
	CreatedAt time.Time
}

// PendingJoins lists the entry requests in state pending that have not expired,
// oldest first.
func (c *Citadel) PendingJoins(ctx context.Context) ([]PendingJoin, error) {
	rows, err := c.db.Reader().QueryContext(ctx,
		`SELECT `+joinColumns+` FROM join_requests WHERE state = 'pending' AND expires_at > ? ORDER BY created_at, join_id`, c.clk.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingJoin
	for rows.Next() {
		j, err := scanJoin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, PendingJoin{ID: j.ID, Name: j.Name, Code: j.Code, CreatedAt: j.CreatedAt})
	}
	return out, rows.Err()
}

// Machine reads one machine.
func (c *Citadel) Machine(ctx context.Context, id string) (Machine, error) {
	return loadMachine(ctx, c.db.Reader(), id)
}

// Config is the configuration of the Guild as guild.config carries it: the
// active machines, the revoked certificates and the time zone.
func (c *Citadel) Config(ctx context.Context) (protocol.GuildConfig, error) {
	return ConfigOf(ctx, c.db, c.creds)
}

// ConfigOf is the configuration of the Guild in db with the revoked list of
// creds, for a Citadel that does not run its Citadel (the CA) yet.
func ConfigOf(ctx context.Context, db *store.DB, creds *Credentials) (protocol.GuildConfig, error) {
	g, err := readGuild(ctx, db.Reader())
	if err != nil {
		return protocol.GuildConfig{}, err
	}
	return configFrom(ctx, db.Reader(), g, creds)
}

func (c *Citadel) configFrom(ctx context.Context, q querier, g guildRow) (protocol.GuildConfig, error) {
	return configFrom(ctx, q, g, c.creds)
}

func configFrom(ctx context.Context, q querier, g guildRow, creds *Credentials) (protocol.GuildConfig, error) {
	ms, err := listMachines(ctx, q)
	if err != nil {
		return protocol.GuildConfig{}, err
	}
	cfg := protocol.GuildConfig{Machines: []protocol.GuildConfigMachine{}, Revoked: creds.Revoked(), Retired: creds.Retired(), Timezone: g.Timezone}
	for _, m := range ms {
		if m.State != MachineActive {
			continue
		}
		e := protocol.GuildConfigMachine{MachineID: m.ID, Name: m.Name, Instance: m.Instance, Port: m.Port, Priority: m.Priority, Excluded: m.Excluded}
		for _, ip := range m.IPs {
			e.Addresses = append(e.Addresses, ip.String())
		}
		cfg.Machines = append(cfg.Machines, e)
	}
	return cfg, nil
}

// CitadelID is the machine the highest certificate of epoch names.
func (c *Citadel) CitadelID(ctx context.Context) (string, error) {
	g, err := readGuild(ctx, c.db.Reader())
	if err != nil {
		return "", err
	}
	return g.Epoch.CitadelMachineID, nil
}

// activeNotCitadel loads an active machine that is not the Citadel.
func (c *Citadel) activeNotCitadel(ctx context.Context, id string) (Machine, error) {
	m, err := loadMachine(ctx, c.db.Reader(), id)
	if err != nil {
		return Machine{}, err
	}
	if m.State != MachineActive {
		return Machine{}, fmt.Errorf("%w: %s is %s", ErrNotActive, id, m.State)
	}
	citadel, err := c.CitadelID(ctx)
	if err != nil {
		return Machine{}, err
	}
	if id == citadel {
		return Machine{}, ErrCitadelProtected
	}
	return m, nil
}

// Like every write of the Owner, SetExcluded, Remove, SetPriority and SetTimezone go
// through the gate (Admit): while the Citadel drains for a switch they are refused with
// ErrSuspended, so none lands after the last LSN the new Citadel is given.
//
// SetExcluded excludes a machine from the secrets, or undoes it (the Owner,
// with the PIN, in the panel or by /guilda). The Citadel cannot be excluded.
// The registry changes and the hook fires; sending vault.drop (or the vault
// again) and guild.config belongs to the replication. The certificates do
// not change: only the acting Citadel's carries mestre.local.
func (c *Citadel) SetExcluded(ctx context.Context, machineID string, excluded bool) error {
	leave, ok := c.enter()
	if !ok {
		return ErrSuspended
	}
	defer leave()
	m, err := c.activeNotCitadel(ctx, machineID)
	if err != nil {
		return err
	}
	if m.Excluded == excluded {
		return nil
	}
	if _, err := c.db.Writer().ExecContext(ctx, `UPDATE machines SET excluded = ? WHERE machine_id = ?`, boolInt(excluded), machineID); err != nil {
		return fmt.Errorf("guild: exclude a machine: %w", err)
	}
	m.Excluded = excluded
	c.cfg.Logger.Info("guild: exclusion changed", "machine", machineID, "excluded", excluded)
	if h := c.cfg.Hooks.ExclusionChanged; h != nil {
		h(m)
	}
	return nil
}

// Promotable says whether a machine may take the place of the Citadel: it is
// active and not excluded (docs/guilda/cidadela-e-torres.md#promoção).
func (c *Citadel) Promotable(ctx context.Context, machineID string) error {
	m, err := loadMachine(ctx, c.db.Reader(), machineID)
	if err != nil {
		return err
	}
	if m.State != MachineActive {
		return fmt.Errorf("%w: %s is %s", ErrNotActive, machineID, m.State)
	}
	if m.Excluded {
		return ErrExcludedMachine
	}
	return nil
}

// UpdateNames records the names a machine reports (its host name and its
// IPs), from the machine sheet. They are checked first, as for an entry: one
// label that is not "mestre", private, loopback or link-local addresses, at
// most transport.MaxIPs; an error wraps transport.ErrBadName. The extra names
// are the Owner's, for the Guild (SetExtraNames), never a machine's. The
// certificates follow when the machine next renews; Renewer asks at once
// when they change. The engine port the machine gave at the entry stays.
func (c *Citadel) UpdateNames(ctx context.Context, machineID, hostname string, ips []net.IP) error {
	host, err := transport.CheckHostname(hostname)
	if err == nil {
		err = transport.CheckIPs(ips)
	}
	if err != nil {
		return fmt.Errorf("guild: the names of %s: %w", machineID, err)
	}
	res, err := c.db.Writer().ExecContext(ctx,
		`UPDATE machines SET names = json_set(?, '$.port', coalesce(json_extract(names, '$.port'), 0)) WHERE machine_id = ? AND state = 'active'`,
		encodeNames(host, ips), machineID)
	if err != nil {
		return fmt.Errorf("guild: update the names of a machine: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s", ErrNotActive, machineID)
	}
	return nil
}

// RemoveResult says how a removal went.
type RemoveResult struct {
	// Notified is true when the machine was online and took guild.leave, so
	// it erased its share of the Guild. A machine removed while offline keeps
	// the encrypted copy of the vault on its disk: the panel recommends
	// replacing the secrets (docs/guilda/guilda.md#saída-e-remoção).
	Notified bool
}

// Remove takes a machine out of the Guild (the Owner, with the PIN). If it is
// online it is told first to erase what it holds (guild.leave); then its
// certificates are revoked, its connections fall with 4002 and every new one
// is refused. Its characters stop being served by the Guild; the history of
// the machine stays in the database.
func (c *Citadel) Remove(ctx context.Context, machineID, by string) (RemoveResult, error) {
	leave, ok := c.enter()
	if !ok {
		return RemoveResult{}, ErrSuspended
	}
	defer leave()
	m, err := c.activeNotCitadel(ctx, machineID)
	if err != nil {
		return RemoveResult{}, err
	}
	var res RemoveResult
	if c.cfg.Peers != nil {
		if err := c.cfg.Peers.Leave(ctx, machineID, "removed"); err != nil {
			c.cfg.Logger.Info("guild: the machine did not take guild.leave", "machine", machineID, "err", err)
		} else {
			res.Notified = true
		}
	}
	if err := c.retire(ctx, m, MachineRemoved); err != nil {
		return res, err
	}
	if c.cfg.Peers != nil {
		c.cfg.Peers.CloseRevoked()
	}
	c.cfg.Logger.Info("guild: a machine was removed", "machine", machineID, "by", by, "notified", res.Notified)
	if h := c.cfg.Hooks.Removed; h != nil {
		h(m, string(MachineRemoved))
	}
	return res, nil
}

// retire revokes every certificate of a machine and marks it removed or left,
// in one transaction, then updates the revocation list the TLS reads. The
// messages that waited for the machine are dropped.
func (c *Citadel) retire(ctx context.Context, m Machine, state MachineState) error {
	now := c.clk.Now().UnixMilli()
	tx, err := c.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("guild: retire a machine: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE machine_credentials SET revoked_at = ? WHERE machine_id = ? AND revoked_at IS NULL`, now, m.ID); err != nil {
		return fmt.Errorf("guild: revoke the credentials: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE machines SET state = ?, removed_at = ? WHERE machine_id = ?`, string(state), now, m.ID); err != nil {
		return fmt.Errorf("guild: retire a machine: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM outbox WHERE peer = ?`, m.ID); err != nil {
		return fmt.Errorf("guild: drop the messages for a machine: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT serial FROM machine_credentials WHERE machine_id = ?`, m.ID)
	if err != nil {
		return fmt.Errorf("guild: read the credentials: %w", err)
	}
	var serials []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return err
		}
		serials = append(serials, s)
	}
	rows.Close()
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("guild: retire a machine: %w", err)
	}
	c.creds.Add(serials...)
	c.creds.Retire(m.ID)
	return nil
}

// CanUninstall says whether the Citadel may be uninstalled: not while other
// machines are in the Guild (docs/guilda/guilda.md#saída-e-remoção). Alone,
// it may, and the Guild ends.
func (c *Citadel) CanUninstall(ctx context.Context) error {
	citadel, err := c.CitadelID(ctx)
	if err != nil {
		return err
	}
	var others int
	if err := c.db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM machines WHERE state = 'active' AND machine_id <> ?`, citadel).Scan(&others); err != nil {
		return fmt.Errorf("guild: count the machines: %w", err)
	}
	if others > 0 {
		return fmt.Errorf("%w (%d other machines)", ErrTowersRemain, others)
	}
	return nil
}

// Renew issues the next certificate of the engine or of the Guardian of a
// machine, for the key in csrDER and the names the registry holds: the
// identity stays, the key changes, and the IPs follow the machine. The
// certificate it replaces must still be valid; an expired one needs a new
// entry.
func (c *Citadel) Renew(ctx context.Context, machineID string, kind transport.Kind, csrDER []byte) (*x509.Certificate, error) {
	tx, err := c.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("guild: renew: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	m, err := loadMachine(ctx, tx, machineID)
	if err != nil {
		return nil, err
	}
	if m.State != MachineActive {
		return nil, fmt.Errorf("%w: %s is %s", ErrNotActive, machineID, m.State)
	}
	var der []byte
	err = tx.QueryRowContext(ctx,
		`SELECT cert FROM machine_credentials WHERE machine_id = ? AND role = ? AND revoked_at IS NULL ORDER BY issued_at DESC, rowid DESC LIMIT 1`,
		machineID, roleOf(kind)).Scan(&der)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s has no %s credential", ErrNotActive, machineID, roleOf(kind))
	}
	if err != nil {
		return nil, fmt.Errorf("guild: renew: %w", err)
	}
	old, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("guild: renew: the certificate kept: %w", err)
	}
	cert, err := c.cfg.CA.Reissue(old, csrDER, m.Names())
	if err != nil {
		var invalid x509.CertificateInvalidError
		switch {
		case errors.As(err, &invalid) && invalid.Reason == x509.Expired:
			return nil, fmt.Errorf("%w: %v", ErrExpiredCredential, err)
		case errors.Is(err, transport.ErrBadName), errors.Is(err, transport.ErrSameKey):
			return nil, fmt.Errorf("%w: %v", ErrRenewRefused, err)
		}
		return nil, fmt.Errorf("guild: renew: %w", err)
	}
	if err := insertCredential(ctx, tx, machineID, roleOf(kind), cert.Raw, transport.Serial(cert), cert.NotAfter, c.clk.Now()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("guild: renew: %w", err)
	}
	return cert, nil
}

// HandleCertRenew is the handler of cert.renew (docs/conectividade/mensagens.md):
// the engine renews its own certificate on its connection, the Guardian its
// own on its connection; the answer is cert.issued with the PEM certificate.
func (c *Citadel) HandleCertRenew(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
	p, ok := m.Payload.(*protocol.CertRenew)
	if !ok {
		return nil, protocol.NewError(protocol.CodeInvalidPayload, "cert.renew without payload")
	}
	var kind transport.Kind
	switch m.Peer.Role {
	case protocol.RoleMachine:
		kind = transport.KindMachine
	case protocol.RoleGuardian:
		kind = transport.KindGuardian
	default:
		return nil, protocol.NewError(protocol.CodeForbidden, "only a machine renews its certificates")
	}
	if p.Role != roleOf(kind) {
		return nil, protocol.NewError(protocol.CodeForbidden, "a %s connection renews the %s certificate, not %q", m.Peer.Role, roleOf(kind), p.Role)
	}
	block, _ := pem.Decode([]byte(p.CSR))
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, protocol.NewError(protocol.CodeInvalidPayload, "csr is not a PEM CERTIFICATE REQUEST")
	}
	cert, err := c.Renew(ctx, m.Peer.ID, kind, block.Bytes)
	switch {
	case errors.Is(err, ErrNotActive), errors.Is(err, ErrExpiredCredential), errors.Is(err, ErrNoMachine), errors.Is(err, ErrRenewRefused):
		return nil, protocol.NewError(protocol.CodeForbidden, "%v", err)
	case err != nil:
		return nil, err
	}
	return &protocol.Reply{Type: protocol.TypeCertIssued, Payload: protocol.CertIssued{Cert: string(pemCert(cert))}}, nil
}

// HandleLeave is the handler of guild.leave on the Citadel: a Tower asking to
// leave because it is being uninstalled. Its certificates are revoked and it
// is marked as left; the connection stays until the machine closes it.
func (c *Citadel) HandleLeave(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
	p, ok := m.Payload.(*protocol.GuildLeave)
	if !ok || p.Reason != "uninstall" {
		return nil, protocol.NewError(protocol.CodeInvalidPayload, "a machine asks to leave with reason uninstall")
	}
	if m.Peer.Role != protocol.RoleMachine {
		return nil, protocol.NewError(protocol.CodeForbidden, "only the engine of a machine leaves the Guild")
	}
	mach, err := c.activeNotCitadel(ctx, m.Peer.ID)
	switch {
	case errors.Is(err, ErrNotActive):
		return nil, nil // already gone: the ack tells the machine to go on
	case errors.Is(err, ErrCitadelProtected), errors.Is(err, ErrNoMachine):
		return nil, protocol.NewError(protocol.CodeForbidden, "%v", err)
	case err != nil:
		return nil, err
	}
	if err := c.retire(ctx, mach, MachineLeft); err != nil {
		return nil, err
	}
	c.cfg.Logger.Info("guild: a machine left", "machine", mach.ID)
	if h := c.cfg.Hooks.Removed; h != nil {
		h(mach, p.Reason)
	}
	return nil, nil
}

func pemCert(c *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
}
