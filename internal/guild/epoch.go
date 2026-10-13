package guild

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/transport"
)

// epochDomain separates the signature of the certificate of epoch from every
// other thing the CA key could sign.
const epochDomain = "mestre/guild/epoch-certificate/v1"

// Errors of the certificate of epoch.
var (
	// ErrWrongGuild: the certificate is of another Guild.
	ErrWrongGuild = errors.New("guild: the certificate is of another Guild")
	// ErrStaleCertificate: the certificate is not higher than the one kept.
	ErrStaleCertificate = errors.New("guild: the certificate of epoch is not higher than the one kept")
)

// epochMessage is what the CA signs: every field of the certificate, each
// with its length in front, so no two certificates share a message.
func epochMessage(c protocol.Certificate) []byte {
	b := []byte(epochDomain)
	str := func(s string) {
		b = binary.BigEndian.AppendUint32(b, uint32(len(s)))
		b = append(b, s...)
	}
	str(c.GuildID)
	b = binary.BigEndian.AppendUint64(b, c.Epoch)
	str(c.CitadelMachineID)
	str(c.ApprovedBy)
	str(c.ApprovalID)
	b = binary.BigEndian.AppendUint64(b, uint64(c.IssuedAt.UnixMilli()))
	return b
}

// SignEpoch fills the signature of c with the key of the CA. Only the
// Citadel, which holds the CA, signs: at the creation of the Guild and at a
// promotion.
func SignEpoch(ca *transport.CA, c protocol.Certificate) (protocol.Certificate, error) {
	if c.GuildID == "" || c.CitadelMachineID == "" {
		return c, errors.New("guild: a certificate of epoch names the Guild and the Citadel")
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	sig, err := ca.Sign(epochMessage(c))
	if err != nil {
		return c, err
	}
	c.Signature = base64.StdEncoding.EncodeToString(sig)
	return c, nil
}

// Verifier is the protocol.CertVerifier of a Guild: a certificate of epoch is
// good when it is of this Guild and carries a signature of its CA.
type Verifier struct {
	ca      *x509.Certificate
	guildID string
}

var _ protocol.CertVerifier = (*Verifier)(nil)

// NewVerifier checks certificates of the Guild guildID against its CA.
func NewVerifier(ca *x509.Certificate, guildID string) *Verifier {
	return &Verifier{ca: ca, guildID: guildID}
}

// Verify implements protocol.CertVerifier.
func (v *Verifier) Verify(c protocol.Certificate) error {
	if c.GuildID != v.guildID {
		return fmt.Errorf("%w: %q, not %q", ErrWrongGuild, c.GuildID, v.guildID)
	}
	if err := c.Validate(); err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(c.Signature)
	if err != nil {
		return fmt.Errorf("guild: certificate of epoch %d: signature: %w", c.Epoch, transport.ErrBadSignature)
	}
	if err := transport.VerifySignature(v.ca, epochMessage(c), sig); err != nil {
		return fmt.Errorf("guild: certificate of epoch %d: %w", c.Epoch, err)
	}
	return nil
}

// EpochsConfig wires Epochs.
type EpochsConfig struct {
	// Member keeps the highest certificate seen (the machine database).
	Member *Member
	Logger *slog.Logger // nil discards
	// OnFollow runs before a higher certificate is kept: a Citadel demotes
	// itself to a Tower here, before any external action
	// (docs/guilda/cidadela-e-torres.md#volta-da-antiga-cidadela). An error
	// refuses the certificate.
	OnFollow func(ctx context.Context, cert protocol.Certificate) error
	// OnStale runs when a peer refused a message of this machine for its
	// epoch: the stage that finds the Citadel again refreshes the
	// certificate here.
	OnStale func(ctx context.Context, peer protocol.Peer)
	// OnRefused runs for every certificate that was refused (bad signature,
	// another Guild, not higher): the audit trail.
	OnRefused func(cert protocol.Certificate, reason error)
}

// Epochs is the protocol.EpochState and the protocol.CertVerifier of a
// machine of the Guild, over the highest certificate of epoch it has seen,
// kept in the machine database (docs/guilda/replicacao.md#certidão-de-época).
type Epochs struct {
	cfg      EpochsConfig
	verifier *Verifier

	mu   sync.Mutex
	cert protocol.Certificate
}

var (
	_ protocol.EpochState   = (*Epochs)(nil)
	_ protocol.CertVerifier = (*Epochs)(nil)
)

// LoadEpochs reads the certificate the machine has kept and checks its
// signature. It fails with ErrNotMember on a machine outside any Guild.
func LoadEpochs(ctx context.Context, cfg EpochsConfig) (*Epochs, error) {
	id, err := cfg.Member.Identity(ctx)
	if err != nil {
		return nil, err
	}
	v := NewVerifier(id.CA, id.GuildID)
	if err := v.Verify(id.Epoch); err != nil {
		return nil, fmt.Errorf("guild: the certificate of epoch in the database: %w", err)
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Epochs{cfg: cfg, verifier: v, cert: id.Epoch}, nil
}

// Epoch implements protocol.EpochState.
func (e *Epochs) Epoch() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cert.Epoch
}

// Certificate implements protocol.EpochState.
func (e *Epochs) Certificate() *protocol.Certificate {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.cert
	return &c
}

// Citadel is the machine the highest certificate names.
func (e *Epochs) Citadel() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cert.CitadelMachineID
}

// Verify implements protocol.CertVerifier; a refusal is logged and handed
// to OnRefused.
func (e *Epochs) Verify(c protocol.Certificate) error {
	err := e.verifier.Verify(c)
	if err != nil {
		e.refused(c, err)
	}
	return err
}

// Follow implements protocol.EpochState: the certificate is verified again,
// must be higher than the one kept, and is written to the database before
// Epoch returns it.
func (e *Epochs) Follow(ctx context.Context, c protocol.Certificate) error {
	if err := e.verifier.Verify(c); err != nil {
		e.refused(c, err)
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if c.Epoch <= e.cert.Epoch {
		err := fmt.Errorf("%w: epoch %d, kept %d", ErrStaleCertificate, c.Epoch, e.cert.Epoch)
		e.refused(c, err)
		return err
	}
	if e.cfg.OnFollow != nil {
		if err := e.cfg.OnFollow(ctx, c); err != nil {
			return err
		}
	}
	// A verified certificate is kept even if the caller gives up now: the
	// Citadel that follows it already stepped down in OnFollow.
	if err := e.cfg.Member.SaveEpoch(context.WithoutCancel(ctx), c); err != nil {
		return err
	}
	e.cfg.Logger.Info("guild: following a higher epoch", "from", e.cert.Epoch, "epoch", c.Epoch, "citadel", c.CitadelMachineID)
	e.cert = c
	return nil
}

// Stale implements protocol.EpochState.
func (e *Epochs) Stale(ctx context.Context, peer protocol.Peer) {
	e.cfg.Logger.Warn("guild: a peer refused our epoch", "peer", peer.ID)
	if e.cfg.OnStale != nil {
		e.cfg.OnStale(ctx, peer)
	}
}

func (e *Epochs) refused(c protocol.Certificate, err error) {
	e.cfg.Logger.Warn("guild: certificate of epoch refused", "epoch", c.Epoch, "citadel", c.CitadelMachineID, "err", err)
	if e.cfg.OnRefused != nil {
		e.cfg.OnRefused(c, err)
	}
}

// encodeCert and decodeCert are the JSON of the certificate in the databases.
func encodeCert(c protocol.Certificate) string {
	b, _ := json.Marshal(c) // a Certificate always encodes
	return string(b)
}

func decodeCert(s string) (protocol.Certificate, error) {
	var c protocol.Certificate
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return c, fmt.Errorf("guild: certificate of epoch in the database: %w", err)
	}
	return c, nil
}
