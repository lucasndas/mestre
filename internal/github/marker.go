package github

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// NewOp is the uuid of an operation, made before the sending and kept through
// every resending (docs/github/acoes-por-nivel.md#marcador-de-operação).
func NewOp() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("github: no randomness: " + err.Error())
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	h := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
}

// Marker is the invisible mark at the end of a body.
func Marker(op string) string { return "<!-- mestre:op=" + op + " -->" }

// WithMarker puts the marker at the end of the body. The text of the caller (an
// agent) goes through StripMarkers first: it never carries a marker of its own.
func WithMarker(body, op string) string {
	return strings.TrimRight(StripMarkers(body), "\n") + "\n\n" + Marker(op)
}

// StripMarkers takes out of a text anything that reads as a marker of operation.
func StripMarkers(body string) string {
	return strings.ReplaceAll(body, "<!-- mestre:op=", "<!-- mestre:op-removed=")
}

// HasMarker says whether the body ends with the marker of the operation: the
// place WithMarker puts it, which a text of somebody else does not reach.
func HasMarker(body, op string) bool {
	return strings.HasSuffix(strings.TrimRight(body, " \n\r\t"), Marker(op))
}
