package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
)

// KeySize is the size of a vault data key: 32 random bytes
// (docs/plataforma/cofre.md#chave-do-cofre).
const KeySize = 32

// FingerprintLen is how many hex characters of the HMAC are kept and shown.
const FingerprintLen = 8

// HKDF labels: the data key is never used directly, so the AEAD key and the
// fingerprint key are independent.
const (
	labelAEAD        = "mestre vault aead v1"
	labelFingerprint = "mestre vault fingerprint v1"
	aadLabel         = "mestre/vault/secret/v1"
)

// dataKey is one version of the vault data key with its derived keys.
type dataKey struct {
	version uint32
	raw     []byte
	aead    cipher.AEAD
	fpKey   []byte
}

func newDataKey(version uint32, raw []byte) (*dataKey, error) {
	if version == 0 {
		return nil, errors.New("vault: key version 0 is invalid")
	}
	if len(raw) != KeySize {
		return nil, fmt.Errorf("vault: key version %d has %d bytes, want %d", version, len(raw), KeySize)
	}
	k := &dataKey{version: version, raw: slices.Clone(raw)}
	encKey, err := hkdf.Key(sha256.New, k.raw, nil, labelAEAD, 32)
	if err != nil {
		return nil, fmt.Errorf("vault: derive key: %w", err)
	}
	defer zero(encKey)
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, fmt.Errorf("vault: derive key: %w", err)
	}
	if k.aead, err = cipher.NewGCM(block); err != nil {
		return nil, fmt.Errorf("vault: derive key: %w", err)
	}
	if k.fpKey, err = hkdf.Key(sha256.New, k.raw, nil, labelFingerprint, 32); err != nil {
		return nil, fmt.Errorf("vault: derive key: %w", err)
	}
	return k, nil
}

func (k *dataKey) destroy() {
	zero(k.raw)
	zero(k.fpKey)
}

// rowAAD binds a ciphertext to its row: name, type and key version, each
// length-prefixed so that no two rows share the same associated data.
func rowAAD(name string, typ Type, version uint32) []byte {
	b := make([]byte, 0, len(aadLabel)+len(name)+len(typ)+12)
	b = append(b, aadLabel...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(name)))
	b = append(b, name...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(typ)))
	b = append(b, typ...)
	return binary.BigEndian.AppendUint32(b, version)
}

// seal encrypts value for the row with a fresh random 96-bit nonce.
func (k *dataKey) seal(name string, typ Type, value []byte) (ciphertext, nonce []byte) {
	nonce = make([]byte, k.aead.NonceSize())
	_, _ = rand.Read(nonce) // crypto/rand.Read never fails (Go 1.24+)
	return k.aead.Seal(nil, nonce, value, rowAAD(name, typ, k.version)), nonce
}

// open decrypts a row; any change to the row or a wrong key fails the
// GCM verification.
func (k *dataKey) open(name string, typ Type, ciphertext, nonce []byte) ([]byte, error) {
	if len(nonce) != k.aead.NonceSize() {
		return nil, errVerify
	}
	plain, err := k.aead.Open(nil, nonce, ciphertext, rowAAD(name, typ, k.version))
	if err != nil {
		return nil, errVerify
	}
	return plain, nil
}

// fingerprint is the first FingerprintLen hex characters of
// HMAC-SHA256(fingerprint key, value): enough to compare, never to recover.
func (k *dataKey) fingerprint(value []byte) string {
	m := hmac.New(sha256.New, k.fpKey)
	m.Write(value)
	return hex.EncodeToString(m.Sum(nil))[:FingerprintLen]
}

var errVerify = errors.New("verification failed")

// keyring holds every live version of the data key: one normally, two
// during a rotation (docs/plataforma/cofre.md#chave-do-cofre).
type keyring struct {
	keys []*dataKey // ascending version
}

func (r *keyring) current() *dataKey { return r.keys[len(r.keys)-1] }

func (r *keyring) get(version uint32) *dataKey {
	for _, k := range r.keys {
		if k.version == version {
			return k
		}
	}
	return nil
}

func (r *keyring) versions() []uint32 {
	vs := make([]uint32, len(r.keys))
	for i, k := range r.keys {
		vs[i] = k.version
	}
	return vs
}

// subset returns the keys whose versions are in keep (sharing them).
func (r *keyring) subset(keep []uint32) *keyring {
	out := &keyring{}
	for _, k := range r.keys {
		if slices.Contains(keep, k.version) {
			out.keys = append(out.keys, k)
		}
	}
	return out
}

func (r *keyring) destroy() {
	for _, k := range r.keys {
		k.destroy()
	}
}

// Keyring encoding, used in the key file and inside the recovery envelope:
// "MCKR", format 1, count (uint16), then per key the version (uint32) and
// the 32 raw bytes.
const keyringMagic = "MCKR\x01"

func (r *keyring) marshal() []byte {
	b := make([]byte, 0, len(keyringMagic)+2+len(r.keys)*(4+KeySize))
	b = append(b, keyringMagic...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(r.keys)))
	for _, k := range r.keys {
		b = binary.BigEndian.AppendUint32(b, k.version)
		b = append(b, k.raw...)
	}
	return b
}

var errBadKeyring = errors.New("vault: key data is malformed")

func parseKeyring(b []byte) (*keyring, error) {
	if len(b) < len(keyringMagic)+2 || string(b[:len(keyringMagic)]) != keyringMagic {
		return nil, errBadKeyring
	}
	b = b[len(keyringMagic):]
	n := int(binary.BigEndian.Uint16(b))
	b = b[2:]
	if n == 0 || len(b) != n*(4+KeySize) {
		return nil, errBadKeyring
	}
	r := &keyring{}
	for i := range n {
		rec := b[i*(4+KeySize) : (i+1)*(4+KeySize)]
		v := binary.BigEndian.Uint32(rec)
		if len(r.keys) > 0 && v <= r.current().version {
			r.destroy()
			return nil, errBadKeyring
		}
		k, err := newDataKey(v, rec[4:])
		if err != nil {
			r.destroy()
			return nil, errBadKeyring
		}
		r.keys = append(r.keys, k)
	}
	return r, nil
}

func newRandomKey(version uint32) (*dataKey, error) {
	raw := make([]byte, KeySize)
	defer zero(raw)
	_, _ = rand.Read(raw)
	return newDataKey(version, raw)
}

// zero overwrites b; Go may still hold copies (GC moves, AES key schedule),
// so this only narrows the window.
func zero(b []byte) { clear(b) }
