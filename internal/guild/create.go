package guild

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"errors"
	"fmt"
	"net"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

// CreateConfig is what the installer gives the creation of a Guild on the
// first machine (docs/guilda/guilda.md#formação).
type CreateConfig struct {
	// GuildDB is the Guild database, empty and migrated.
	GuildDB *store.DB
	// Member is the record of this machine (the machine database and the
	// engine key file).
	Member *Member
	// Secrets is the vault, created before with its recovery key
	// (docs/plataforma/cofre.md): the CA key goes in it, and only there.
	Secrets transport.SecretStore
	Clock   clock.Clock

	// Name is the name of the computer shown in the Guild; Hostname gives the
	// certificate names (its first label, <host>.local) and IPs the addresses
	// (those a certificate may carry, transport.CertificateIPs).
	Name     string
	Hostname string
	Instance string
	IPs      []net.IP
	// EnginePort is the engine port of this instance (guild.config).
	EnginePort int
	// Timezone is the Guild time zone, the one of this machine's system.
	Timezone string
	// RecoveryPublicKey is the public part of the recovery key, which the
	// Guild database keeps (docs/arquitetura/persistencia.md).
	RecoveryPublicKey []byte
}

// Founded is the new Guild as the first machine holds it. The private keys
// were made here; the caller keeps the Guardian's in its own folder.
type Founded struct {
	Identity
	CA          *transport.CA
	MotorKey    *ecdsa.PrivateKey
	GuardianKey *ecdsa.PrivateKey
	Name        string
}

// ErrAlreadyFounded: the Guild database already holds a Guild.
var ErrAlreadyFounded = errors.New("guild: this database already holds a Guild")

// Create founds a Guild on this machine, which becomes the Citadel of epoch 1:
// the guild_id (a ULID), the CA, with its key in the vault, the certificates
// of the engine and of the Guardian, the registry with this machine in it, the
// certificate of epoch 1 signed by the CA, and the identity in the machine
// database. It refuses a machine that already belongs to a Guild.
func Create(ctx context.Context, cfg CreateConfig) (*Founded, error) {
	if cfg.GuildDB == nil || cfg.Member == nil || cfg.Secrets == nil {
		return nil, errors.New("guild: CreateConfig needs GuildDB, Member and Secrets")
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Timezone == "" {
		cfg.Timezone = "UTC"
	}
	if _, err := cfg.Member.Identity(ctx); err == nil {
		return nil, ErrAlreadyMember
	} else if !errors.Is(err, ErrNotMember) {
		return nil, err
	}
	if _, err := readGuild(ctx, cfg.GuildDB.Reader()); err == nil {
		return nil, ErrAlreadyFounded
	} else if !errors.Is(err, ErrNoGuild) {
		return nil, err
	}
	name := cleanName(cfg.Name)
	if name == "" {
		name = cleanName(cfg.Hostname)
	}
	if name == "" {
		return nil, fmt.Errorf("%w: the machine has no name", ErrBadRequest)
	}

	host := transport.HostLabel(cfg.Hostname)

	ids := protocol.NewIDGen(cfg.Clock)
	guildID, machineID := ids.NewString(), ids.NewString()
	ca, err := transport.NewCA(guildID, cfg.Clock)
	if err != nil {
		return nil, err
	}
	if err := ca.Store(ctx, cfg.Secrets); err != nil {
		return nil, err
	}
	m := Machine{
		ID: machineID, Name: name, Instance: cfg.Instance, State: MachineActive,
		Hostname: host, IPs: transport.CertificateIPs(cfg.IPs), JoinedAt: cfg.Clock.Now().UTC(),
	}
	motorKey, motor, err := issueOwn(ca, transport.KindMachine, m.Names())
	if err != nil {
		return nil, err
	}
	guardKey, guardian, err := issueOwn(ca, transport.KindGuardian, m.Names())
	if err != nil {
		return nil, err
	}
	epoch, err := SignEpoch(ca, protocol.Certificate{
		GuildID: guildID, Epoch: 1, CitadelMachineID: machineID, IssuedAt: protocol.Timestamp{Time: m.JoinedAt},
	})
	if err != nil {
		return nil, err
	}

	now := m.JoinedAt.UnixMilli()
	tx, err := cfg.GuildDB.Writer().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("guild: create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO guild (id, guild_id, ca_cert, epoch, epoch_certificate, timezone, recovery_public_key, created_at) VALUES (1, ?, ?, 1, ?, ?, ?, ?)`,
		guildID, ca.DER(), encodeCert(epoch), cfg.Timezone, cfg.RecoveryPublicKey, now); err != nil {
		return nil, fmt.Errorf("guild: record the Guild: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO epochs (epoch, citadel_machine_id, from_lsn, started_at) VALUES (1, ?, 0, ?)`, machineID, now); err != nil {
		return nil, fmt.Errorf("guild: record the first epoch: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, joined_at) VALUES (?, ?, ?, 0, 0, 'active', `+withPort+`, ?)`,
		m.ID, m.Name, m.Instance, encodeNames(m.Hostname, m.IPs), cfg.EnginePort, now); err != nil {
		return nil, fmt.Errorf("guild: register this machine: %w", err)
	}
	for _, c := range []struct {
		kind transport.Kind
		der  []byte
		ser  string
		end  int64
	}{
		{transport.KindMachine, motor.Raw, transport.Serial(motor), motor.NotAfter.UnixMilli()},
		{transport.KindGuardian, guardian.Raw, transport.Serial(guardian), guardian.NotAfter.UnixMilli()},
	} {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO machine_credentials (serial, machine_id, role, cert, not_after, issued_at) VALUES (?, ?, ?, ?, ?, ?)`,
			c.ser, m.ID, roleOf(c.kind), c.der, c.end, now); err != nil {
			return nil, fmt.Errorf("guild: record a credential: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("guild: create: %w", err)
	}

	id := Identity{
		MachineID: machineID, GuildID: guildID, Instance: cfg.Instance, CA: ca.Certificate(),
		Motor: motor, Guardian: guardian, Epoch: epoch, JoinedAt: m.JoinedAt,
	}
	if err := cfg.Member.SaveEngineKey(ctx, motorKey); err != nil {
		return nil, err
	}
	if err := cfg.Member.Adopt(ctx, id); err != nil {
		return nil, err
	}
	return &Founded{Identity: id, CA: ca, MotorKey: motorKey, GuardianKey: guardKey, Name: name}, nil
}

// issueOwn makes a key and the certificate the CA gives it, on the machine
// that holds the CA.
func issueOwn(ca *transport.CA, k transport.Kind, n transport.Names) (*ecdsa.PrivateKey, *x509.Certificate, error) {
	key, err := transport.GenerateKey()
	if err != nil {
		return nil, nil, err
	}
	csr, err := transport.NewCSR(key)
	if err != nil {
		return nil, nil, err
	}
	issue := ca.IssueMachine
	if k == transport.KindGuardian {
		issue = ca.IssueGuardian
	}
	cert, err := issue(csr, n)
	if err != nil {
		return nil, nil, err
	}
	return key, cert, nil
}

// LoadCA opens the CA of the Guild on the Citadel: the public certificate
// from the Guild database and the key from the vault, a read the vault
// audits with purpose (docs/plataforma/cofre.md).
func LoadCA(ctx context.Context, db *store.DB, secrets transport.SecretStore, purpose string, clk clock.Clock) (*transport.CA, error) {
	g, err := readGuild(ctx, db.Reader())
	if err != nil {
		return nil, err
	}
	ca, err := transport.LoadCA(ctx, secrets, g.CA, purpose, clk)
	if err != nil {
		return nil, fmt.Errorf("guild: open the CA of the Guild: %w", err)
	}
	return ca, nil
}
