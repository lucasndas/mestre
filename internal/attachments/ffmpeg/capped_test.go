package ffmpeg

import (
	"bytes"
	"errors"
	"testing"
)

// A strict cap stops the program that writes past it; a plain one keeps the first
// bytes and drops the rest.
func TestTheCapStopsOrDropsWhatPassesTheLimit(t *testing.T) {
	t.Parallel()
	var strict bytes.Buffer
	c := &capped{w: &strict, left: 4, strict: true}
	if _, err := c.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("defg")); !errors.Is(err, ErrOutputTooLarge) || !c.over {
		t.Fatalf("write past the cap = %v (over %v)", err, c.over)
	}
	var plain bytes.Buffer
	p := &capped{w: &plain, left: 4}
	if n, err := p.Write([]byte("abcdefgh")); err != nil || n != 8 || plain.String() != "abcd" {
		t.Fatalf("plain cap kept %q (%d, %v)", plain.String(), n, err)
	}
}
