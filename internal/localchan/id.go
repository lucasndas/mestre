package localchan

import (
	"crypto/rand"
	"fmt"
	"math/big"

	"github.com/lucasndas/mestre/pkg/clock"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewID returns a ULID: 48 bits of the time in milliseconds and 80 random
// bits, in 26 characters of Crockford's base 32. It is the id of a request
// of the CLI, and the key of its idempotency.
func NewID(clk clock.Clock) (string, error) {
	var b [16]byte
	ms := uint64(clk.Now().UnixMilli())
	for i := range 6 {
		b[5-i] = byte(ms >> (8 * i))
	}
	if _, err := rand.Read(b[6:]); err != nil {
		return "", fmt.Errorf("random id: %w", err)
	}
	v := new(big.Int).SetBytes(b[:])
	var out [26]byte
	mask := big.NewInt(31)
	digit := new(big.Int)
	for i := 25; i >= 0; i-- {
		digit.And(v, mask)
		out[i] = crockford[digit.Int64()]
		v.Rsh(v, 5)
	}
	return string(out[:]), nil
}
