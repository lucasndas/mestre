package protocol

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"github.com/lucasndas/mestre/pkg/clock"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// ULID is a 128-bit identifier: 48 bits of the time in milliseconds and 80
// random bits. Its text form is 26 characters of Crockford's base 32.
type ULID [16]byte

// ErrBadULID is wrapped by ParseULID.
var ErrBadULID = errors.New("protocol: not a ULID")

// IDGen generates ULIDs from the injected clock.
type IDGen struct {
	clk clock.Clock
	rnd io.Reader
}

// NewIDGen returns a generator that reads the time from clk and the random
// bits from crypto/rand.
func NewIDGen(clk clock.Clock) *IDGen { return &IDGen{clk: clk, rnd: rand.Reader} }

// New returns a fresh ULID.
func (g *IDGen) New() ULID {
	var u ULID
	ms := uint64(g.clk.Now().UnixMilli())
	for i := range 6 {
		u[5-i] = byte(ms >> (8 * i))
	}
	if _, err := io.ReadFull(g.rnd, u[6:]); err != nil {
		// crypto/rand does not fail on the platforms we run on; an id
		// without randomness would break idempotency, so stop.
		panic(fmt.Sprintf("protocol: random id: %v", err))
	}
	return u
}

// NewString returns a fresh ULID in text form.
func (g *IDGen) NewString() string { return g.New().String() }

// String is the 26-character form.
func (u ULID) String() string {
	var out [26]byte
	var acc uint32
	var bits uint
	pos := 25
	for i := 15; i >= 0; i-- {
		acc |= uint32(u[i]) << bits
		bits += 8
		for bits >= 5 {
			out[pos] = crockford[acc&31]
			pos--
			acc >>= 5
			bits -= 5
		}
	}
	if bits > 0 {
		out[pos] = crockford[acc&31]
	}
	return string(out[:])
}

var ulidValue = func() [256]int8 {
	var t [256]int8
	for i := range t {
		t[i] = -1
	}
	for i := range len(crockford) {
		c := crockford[i]
		t[c] = int8(i)
		if c >= 'A' && c <= 'Z' {
			t[c+('a'-'A')] = int8(i)
		}
	}
	return t
}()

// ParseULID reads the 26-character form.
func ParseULID(s string) (ULID, error) {
	var u ULID
	if len(s) != 26 {
		return u, fmt.Errorf("%w: %q has %d characters, want 26", ErrBadULID, s, len(s))
	}
	var acc uint32
	var bits uint
	pos := 15
	for i := 25; i >= 0; i-- {
		v := ulidValue[s[i]]
		if v < 0 {
			return ULID{}, fmt.Errorf("%w: %q has an invalid character", ErrBadULID, s)
		}
		acc |= uint32(v) << bits
		bits += 5
		for bits >= 8 && pos >= 0 {
			u[pos] = byte(acc)
			pos--
			acc >>= 8
			bits -= 8
		}
	}
	if acc != 0 { // the two leading bits of a 130-bit number must be zero
		return ULID{}, fmt.Errorf("%w: %q overflows 128 bits", ErrBadULID, s)
	}
	return u, nil
}
