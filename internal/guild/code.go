package guild

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// joinDomain is the label hashed in front of the join code.
const joinDomain = "mestre-join"

// NonceSize is the size of the two nonces of the entry, in bytes.
const NonceSize = 16

// Fingerprint is the SHA-256 of a DER certificate: of the one the Citadel
// presents in the TLS connection of the entry.
func Fingerprint(der []byte) [32]byte { return sha256.Sum256(der) }

// JoinCode is the short comparison that ties the request of a new machine to
// the TLS connection that carried it, with no secret shared before
// (docs/guilda/guilda.md#entrada-na-guilda): six digits of
// SHA-256("mestre-join" ‖ fp ‖ SHA-256(CSRs) ‖ nonce_n ‖ nonce_c), where fp
// is the fingerprint of the certificate the new machine saw, and the Citadel
// uses the one it presents. A machine in the middle shows the new machine a
// certificate of its own, so fp changes and the two codes differ; and since
// the new machine committed to nonce_n before it saw nonce_c, the middle
// cannot choose values that make them agree (one chance in a million).
// Each field enters with its length in front. The format is "123 456".
func JoinCode(fp [32]byte, csrs [][]byte, nonceN, nonceC []byte) string {
	h := sha256.New()
	field := func(b []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	field([]byte(joinDomain))
	field(fp[:])
	all := sha256.New()
	for _, c := range csrs {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(c)))
		all.Write(n[:])
		all.Write(c)
	}
	field(all.Sum(nil))
	field(nonceN)
	field(nonceC)
	sum := h.Sum(nil)
	n := binary.BigEndian.Uint32(sum[:4]) % 1_000_000
	return fmt.Sprintf("%03d %03d", n/1000, n%1000)
}

// commitment is what the new machine sends first: the hash of nonce_n.
func commitment(nonceN []byte) []byte {
	h := sha256.Sum256(nonceN)
	return h[:]
}
