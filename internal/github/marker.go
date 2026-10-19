package github

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
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

// StripMarkers takes out of a text anything that reads as a marker of the
// engine (of an operation or of an impression): what an agent writes never
// carries one.
func StripMarkers(body string) string {
	body = strings.ReplaceAll(body, "<!-- mestre:op=", "<!-- mestre:op-removed=")
	return strings.ReplaceAll(body, "<!-- mestre:impressao=", "<!-- mestre:impressao-removed=")
}

var markerRe = regexp.MustCompile(`\n*<!-- mestre:(op|impressao)=[^ \n]+ -->`)

// Markers are the marks of the engine a body ends with (the operation and the
// impression), as written, in order.
func Markers(body string) []string {
	return markerRe.FindAllString(strings.TrimRight(body, " \n\r\t"), -1)
}

// HideMarkers takes the marks of the engine out of a body, for whoever reads
// it (the agent never sees them, so it can not copy them back).
func HideMarkers(body string) string {
	return strings.TrimRight(markerRe.ReplaceAllString(body, ""), " \n")
}

// HasMarker says whether the body ends with the marker of the operation: the
// place WithMarker puts it, which a text of somebody else does not reach.
func HasMarker(body, op string) bool {
	return strings.HasSuffix(strings.TrimRight(body, " \n\r\t"), Marker(op))
}
