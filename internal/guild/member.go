package guild

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Errors of the membership.
var (
	// ErrNotMember: this machine belongs to no Guild.
	ErrNotMember = errors.New("guild: this machine is not in a Guild")
	// ErrAlreadyMember: this machine already belongs to a Guild (to enter
	// another it has to leave first).
	ErrAlreadyMember = errors.New("guild: this machine is already in a Guild")
)

// Identity is what a machine keeps of its place in the Guild: public data
// only (docs/arquitetura/persistencia.md, table machine_identity). The role
// is not stored: the Citadel is the machine the certificate of epoch names.
type Identity struct {
	MachineID string
	GuildID   string
	Instance  string
	CA        *x509.Certificate
	Motor     *x509.Certificate
	Guardian  *x509.Certificate
	// Epoch is the highest certificate of epoch seen.
	Epoch    protocol.Certificate
	JoinedAt time.Time
}

// IsCitadel says whether the certificate of epoch names this machine.
func (id Identity) IsCitadel() bool { return id.Epoch.CitadelMachineID == id.MachineID }

// Member is the machine's own record of its place in the Guild (the machine
// database) and the file that holds the private key of its engine.
type Member struct {
	db  *store.DB
	key vault.KeyStore
	clk clock.Clock
}

// NewMember returns the record over an open machine database. key keeps the
// private key of the engine certificate (a file protected by the system; on
// Windows, DPAPI plus the ACL of the service account); it may be nil where
// the key is not read.
func NewMember(db *store.DB, key vault.KeyStore, clk clock.Clock) *Member {
	if clk == nil {
		clk = clock.Real{}
	}
	return &Member{db: db, key: key, clk: clk}
}

// Identity reads the record; ErrNotMember when there is none.
func (m *Member) Identity(ctx context.Context) (Identity, error) {
	var (
		id                           Identity
		ca, motor, guardian          []byte
		epoch                        string
		joined                       int64
		machineID, guildID, instance string
	)
	err := m.db.Reader().QueryRowContext(ctx,
		`SELECT machine_id, guild_id, instance, ca_cert, motor_cert, guardian_cert, epoch_cert, joined_at FROM machine_identity WHERE id = 1`).
		Scan(&machineID, &guildID, &instance, &ca, &motor, &guardian, &epoch, &joined)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, ErrNotMember
	}
	if err != nil {
		return Identity{}, fmt.Errorf("guild: read the identity: %w", err)
	}
	id.MachineID, id.GuildID, id.Instance, id.JoinedAt = machineID, guildID, instance, time.UnixMilli(joined).UTC()
	for _, p := range []struct {
		der  []byte
		into **x509.Certificate
	}{{ca, &id.CA}, {motor, &id.Motor}, {guardian, &id.Guardian}} {
		if *p.into, err = x509.ParseCertificate(p.der); err != nil {
			return Identity{}, fmt.Errorf("guild: a certificate in the identity: %w", err)
		}
	}
	if id.Epoch, err = decodeCert(epoch); err != nil {
		return Identity{}, err
	}
	return id, nil
}

// Adopt records the place of the machine in the Guild, at the creation of
// the Guild and at the entry. It refuses a machine that is already in one.
func (m *Member) Adopt(ctx context.Context, id Identity) error {
	now := m.clk.Now().UnixMilli()
	res, err := m.db.Writer().ExecContext(ctx,
		`INSERT INTO machine_identity (id, machine_id, guild_id, instance, ca_cert, motor_cert, guardian_cert, epoch_cert, joined_at, updated_at)
		 VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
		id.MachineID, id.GuildID, id.Instance, id.CA.Raw, id.Motor.Raw, id.Guardian.Raw, encodeCert(id.Epoch), id.JoinedAt.UnixMilli(), now)
	if err != nil {
		return fmt.Errorf("guild: record the identity: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrAlreadyMember
	}
	return nil
}

// SaveEpoch keeps a certificate of epoch higher than the one kept; a lower
// or equal one is not written.
func (m *Member) SaveEpoch(ctx context.Context, c protocol.Certificate) error {
	cur, err := m.Identity(ctx)
	if err != nil {
		return err
	}
	if c.Epoch <= cur.Epoch.Epoch {
		return fmt.Errorf("%w: epoch %d, kept %d", ErrStaleCertificate, c.Epoch, cur.Epoch.Epoch)
	}
	_, err = m.db.Writer().ExecContext(ctx,
		`UPDATE machine_identity SET epoch_cert = ?, updated_at = ? WHERE id = 1 AND guild_id = ?`,
		encodeCert(c), m.clk.Now().UnixMilli(), c.GuildID)
	if err != nil {
		return fmt.Errorf("guild: keep the certificate of epoch: %w", err)
	}
	return nil
}

// SaveCertificate keeps the renewed certificate of the engine or the
// Guardian.
func (m *Member) SaveCertificate(ctx context.Context, k transport.Kind, cert *x509.Certificate) error {
	col := "motor_cert"
	if k == transport.KindGuardian {
		col = "guardian_cert"
	}
	res, err := m.db.Writer().ExecContext(ctx,
		`UPDATE machine_identity SET `+col+` = ?, updated_at = ? WHERE id = 1`, cert.Raw, m.clk.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("guild: keep the renewed certificate: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotMember
	}
	return nil
}

// Forget erases the record: the machine is outside any Guild again.
func (m *Member) Forget(ctx context.Context) error {
	if _, err := m.db.Writer().ExecContext(ctx, `DELETE FROM machine_identity`); err != nil {
		return fmt.Errorf("guild: forget the identity: %w", err)
	}
	return nil
}

// SaveEngineKey writes the private key of the engine to the key file.
func (m *Member) SaveEngineKey(ctx context.Context, key *ecdsa.PrivateKey) error {
	if m.key == nil {
		return errors.New("guild: no key store for the engine key")
	}
	pemKey, err := encodeKey(key)
	if err != nil {
		return err
	}
	defer clear(pemKey)
	return m.key.Save(ctx, pemKey)
}

// TLS loads what the machine presents and trusts: the CA certificate and the
// engine certificate with its private key as a CertSource, ready for the
// transport.
func (m *Member) TLS(ctx context.Context) (Identity, *transport.CertSource, error) {
	id, err := m.Identity(ctx)
	if err != nil {
		return Identity{}, nil, err
	}
	if m.key == nil {
		return Identity{}, nil, errors.New("guild: no key store for the engine key")
	}
	data, err := m.key.Load(ctx)
	if err != nil {
		return Identity{}, nil, fmt.Errorf("guild: load the engine key: %w", err)
	}
	defer clear(data)
	key, err := decodeKey(data)
	if err != nil {
		return Identity{}, nil, err
	}
	if pub, ok := id.Motor.PublicKey.(*ecdsa.PublicKey); !ok || !pub.Equal(&key.PublicKey) {
		return Identity{}, nil, errors.New("guild: the engine key does not match the engine certificate")
	}
	return id, transport.NewCertSource(id.Motor, key), nil
}

func encodeKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("guild: marshal a key: %w", err)
	}
	defer clear(der)
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

func decodeKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		return nil, errors.New("guild: the key is not an EC PRIVATE KEY block")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("guild: parse a key: %w", err)
	}
	return key, nil
}

// KeyPEM is a private key as the PEM the key files hold. The caller zeroes it
// after writing.
func KeyPEM(key *ecdsa.PrivateKey) ([]byte, error) { return encodeKey(key) }
