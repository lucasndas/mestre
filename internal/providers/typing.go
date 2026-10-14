package providers

import (
	"errors"
	"fmt"
	"strings"
)

// MaxTyped is the longest text TypeMessage types (8 KiB); a longer one goes
// to a file and the agent gets the line that tells it to read the file
// (docs/orquestracao/esperas-e-caixa-de-entrada.md).
const MaxTyped = 8 << 10

// ErrTooLong is a text above MaxTyped.
var ErrTooLong = errors.New("text too long to type")

// Bracketed paste markers.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// clean drops the control characters of a message except newline and tab:
// an ESC in the text could close the paste early and type the rest as keys.
func clean(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f)) {
			return r
		}
		return -1
	}, text)
}

func checkLen(text string) error {
	if len(text) > MaxTyped {
		return fmt.Errorf("%w: %d bytes, at most %d", ErrTooLong, len(text), MaxTyped)
	}
	return nil
}

// pasteAndSend is the delivery of the agent adapters: the text between the
// paste markers, so newlines do not send it halfway, then the send key.
func pasteAndSend(text, send string) ([]byte, error) {
	if err := checkLen(text); err != nil {
		return nil, err
	}
	return []byte(pasteStart + clean(text) + pasteEnd + send), nil
}

// linesAndSend is the null adapter's: each line typed and followed by the
// send key.
func linesAndSend(text, send string) ([]byte, error) {
	if err := checkLen(text); err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, line := range strings.Split(clean(text), "\n") {
		b.WriteString(line)
		b.WriteString(send)
	}
	return []byte(b.String()), nil
}
