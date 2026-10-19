package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

// Impress is the impression of a target: the SHA-256 of its canonical JSON
// (keys sorted, no spaces, numbers as written), so two spellings of the same
// target are one (docs/aprovacoes/fluxo.md#vínculo-ao-estado-exato). A target
// with a repeated key, invalid UTF-8 or anything after the value has no
// impression: two different targets never share one by the way they were read.
func Impress(target json.RawMessage) (string, error) {
	if !utf8.Valid(target) {
		return "", errors.New("the target is not UTF-8")
	}
	if err := noRepeats(json.NewDecoder(bytes.NewReader(target))); err != nil {
		return "", err
	}
	dec := json.NewDecoder(bytes.NewReader(target))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("the target is not JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return "", errors.New("the target has more than one value")
	}
	canon, err := json.Marshal(v) // maps are written with sorted keys
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), nil
}

// noRepeats walks the first value and refuses an object with a key twice.
func noRepeats(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("the target is not JSON: %w", err)
	}
	return walk(dec, tok)
}

func walk(dec *json.Decoder, tok json.Token) error {
	d, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return err
			}
			key, _ := k.(string)
			if seen[key] {
				return fmt.Errorf("the target repeats the key %q", key)
			}
			seen[key] = true
			v, err := dec.Token()
			if err != nil {
				return err
			}
			if err := walk(dec, v); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			v, err := dec.Token()
			if err != nil {
				return err
			}
			if err := walk(dec, v); err != nil {
				return err
			}
		}
	}
	_, err := dec.Token() // the closing delimiter
	return err
}
