package vault

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/lucasndas/mestre/internal/logging"
)

// RecoveryKey is the seed of the X25519 key pair that opens the recovery
// envelope (docs/plataforma/cofre.md#backup-e-chave-de-recuperação). It is
// shown once and never stored: printing or logging it yields the redaction
// marker; only Reveal returns the text.
type RecoveryKey struct {
	priv *ecdh.PrivateKey
	seed []byte
}

var recoveryEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// RecoveryKeyLen is the length of the base32 text without separators.
const RecoveryKeyLen = 52

// NewRecoveryKey generates 32 random bytes.
func NewRecoveryKey() (*RecoveryKey, error) {
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	return newRecoveryKey(seed)
}

func newRecoveryKey(seed []byte) (*RecoveryKey, error) {
	priv, err := ecdh.X25519().NewPrivateKey(seed)
	if err != nil {
		return nil, fmt.Errorf("vault: recovery key: %w", err)
	}
	return &RecoveryKey{priv: priv, seed: seed}, nil
}

// ParseRecoveryKey reads the text typed by the Owner: 52 base32 characters,
// case-insensitive, with any spaces or dashes between groups.
func ParseRecoveryKey(s string) (*RecoveryKey, error) {
	clean := strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, strings.ToUpper(s))
	errInvalid := errors.New("vault: the recovery key must be 52 base32 characters (A-Z, 2-7)")
	if len(clean) != RecoveryKeyLen {
		return nil, errInvalid
	}
	seed, err := recoveryEncoding.DecodeString(clean)
	if err != nil || len(seed) != 32 || recoveryEncoding.EncodeToString(seed) != clean {
		return nil, errInvalid
	}
	return newRecoveryKey(seed)
}

// Reveal returns the key as 13 groups of 4 base32 characters joined by
// dashes. Call it only to show the key once to the Owner.
func (k *RecoveryKey) Reveal() string {
	s := recoveryEncoding.EncodeToString(k.seed)
	groups := make([]string, 0, len(s)/4)
	for i := 0; i < len(s); i += 4 {
		groups = append(groups, s[i:i+4])
	}
	return strings.Join(groups, "-")
}

// PublicKey is the X25519 public key, the only part the Guild stores.
func (k *RecoveryKey) PublicKey() []byte { return k.priv.PublicKey().Bytes() }

// Destroy overwrites the seed held by k.
func (k *RecoveryKey) Destroy() { zero(k.seed) }

// The printing methods have value receivers so that a dereferenced key
// (fmt.Sprint(*rk)) is redacted too, not only the pointer.

func (RecoveryKey) String() string             { return logging.Redacted }
func (RecoveryKey) GoString() string           { return logging.Redacted }
func (RecoveryKey) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(logging.Redacted)) }
func (RecoveryKey) LogValue() slog.Value       { return slog.StringValue(logging.Redacted) }

// Envelope format (cofre-envelope.bin): the keyring encrypted to the
// recovery public key with an ephemeral X25519 key, HKDF-SHA256 and
// AES-256-GCM. The header is in the clear and authenticated:
//
//	"MCEV" 0x01 | recipient public key (32) | ephemeral public key (32) |
//	count (uint16) | key versions (uint32 each) | nonce (12) | ciphertext
const (
	envelopeMagic = "MCEV\x01"
	envelopeLabel = "mestre vault envelope v1"
)

// ErrRecoveryKeyMismatch is returned when the recovery key typed does not
// match the public key the envelope was made for.
var ErrRecoveryKeyMismatch = errors.New("vault: the recovery key does not match this envelope")

var errBadEnvelope = errors.New("vault: recovery envelope is malformed")

func sealEnvelope(recipient []byte, ring *keyring) ([]byte, error) {
	pub, err := ecdh.X25519().NewPublicKey(recipient)
	if err != nil {
		return nil, fmt.Errorf("vault: recovery public key: %w", err)
	}
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("vault: envelope: %w", err)
	}
	header := envelopeHeader(recipient, eph.PublicKey().Bytes(), ring.versions())
	aead, err := envelopeAEAD(eph, pub, header)
	if err != nil {
		return nil, err
	}
	plain := ring.marshal()
	defer zero(plain)
	nonce := make([]byte, aead.NonceSize())
	_, _ = rand.Read(nonce)
	out := make([]byte, 0, len(header)+len(nonce)+len(plain)+aead.Overhead())
	out = append(append(out, header...), nonce...)
	return aead.Seal(out, nonce, plain, header), nil
}

func envelopeHeader(recipient, ephemeral []byte, versions []uint32) []byte {
	h := make([]byte, 0, len(envelopeMagic)+64+2+4*len(versions))
	h = append(h, envelopeMagic...)
	h = append(h, recipient...)
	h = append(h, ephemeral...)
	h = binary.BigEndian.AppendUint16(h, uint16(len(versions)))
	for _, v := range versions {
		h = binary.BigEndian.AppendUint32(h, v)
	}
	return h
}

func envelopeAEAD(priv *ecdh.PrivateKey, peer *ecdh.PublicKey, header []byte) (cipher.AEAD, error) {
	shared, err := priv.ECDH(peer)
	if err != nil {
		return nil, fmt.Errorf("vault: envelope: %w", err)
	}
	defer zero(shared)
	// salt = recipient and ephemeral public keys, as in the header.
	salt := header[len(envelopeMagic) : len(envelopeMagic)+64]
	key, err := hkdf.Key(sha256.New, shared, salt, envelopeLabel, 32)
	if err != nil {
		return nil, fmt.Errorf("vault: envelope: %w", err)
	}
	defer zero(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("vault: envelope: %w", err)
	}
	return cipher.NewGCM(block)
}

// envelopeInfo is the clear part of an envelope.
type envelopeInfo struct {
	recipient []byte
	ephemeral []byte
	versions  []uint32
	headerLen int
}

func parseEnvelopeHeader(b []byte) (envelopeInfo, error) {
	if len(b) < len(envelopeMagic)+64+2 || string(b[:len(envelopeMagic)]) != envelopeMagic {
		return envelopeInfo{}, errBadEnvelope
	}
	p := len(envelopeMagic)
	info := envelopeInfo{recipient: b[p : p+32], ephemeral: b[p+32 : p+64]}
	n := int(binary.BigEndian.Uint16(b[p+64:]))
	p += 66
	if n == 0 || len(b) < p+4*n {
		return envelopeInfo{}, errBadEnvelope
	}
	for i := range n {
		info.versions = append(info.versions, binary.BigEndian.Uint32(b[p+4*i:]))
	}
	info.headerLen = p + 4*n
	return info, nil
}

// openEnvelope decrypts an envelope with the recovery key.
func openEnvelope(b []byte, rk *RecoveryKey) (*keyring, error) {
	info, err := parseEnvelopeHeader(b)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(info.recipient, rk.PublicKey()) {
		return nil, ErrRecoveryKeyMismatch
	}
	eph, err := ecdh.X25519().NewPublicKey(info.ephemeral)
	if err != nil {
		return nil, errBadEnvelope
	}
	header := b[:info.headerLen]
	aead, err := envelopeAEAD(rk.priv, eph, header)
	if err != nil {
		return nil, err
	}
	rest := b[info.headerLen:]
	if len(rest) < aead.NonceSize() {
		return nil, errBadEnvelope
	}
	plain, err := aead.Open(nil, rest[:aead.NonceSize()], rest[aead.NonceSize():], header)
	if err != nil {
		return nil, errBadEnvelope
	}
	defer zero(plain)
	ring, err := parseKeyring(plain)
	if err != nil {
		return nil, errBadEnvelope
	}
	if !slices.Equal(ring.versions(), info.versions) {
		ring.destroy()
		return nil, errBadEnvelope
	}
	return ring, nil
}

// RestoreKey opens a recovery envelope with the recovery key typed by the
// Owner and saves the data key into keys, protected by this machine. It is
// the only way to restore the vault from a backup
// (docs/plataforma/cofre.md#backup-e-chave-de-recuperação).
func RestoreKey(ctx context.Context, envelope []byte, rk *RecoveryKey, keys KeyStore) error {
	ring, err := openEnvelope(envelope, rk)
	if err != nil {
		return err
	}
	defer ring.destroy()
	data := ring.marshal()
	defer zero(data)
	if err := keys.Save(ctx, data); err != nil {
		return fmt.Errorf("vault: save restored key: %w", err)
	}
	return nil
}
