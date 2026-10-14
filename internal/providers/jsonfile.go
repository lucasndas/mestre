package providers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// parseObject reads the content of a JSON settings file: an object (nil or
// blank content is an empty one). Numbers keep their text, so what the
// Mestre does not own comes back as it was. Anything else is an error that
// names the file: it is never overwritten blindly.
func parseObject(cur []byte, path string) (map[string]any, error) {
	if len(bytes.TrimSpace(cur)) == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(cur))
	dec.UseNumber()
	var v any
	err := dec.Decode(&v)
	if err == nil {
		if _, e := dec.Token(); !errors.Is(e, io.EOF) {
			err = errors.New("text after the JSON value")
		}
	}
	o, isObj := v.(map[string]any)
	if err == nil && (!isObj || o == nil) {
		err = errors.New("not an object")
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s is not a JSON object: %v", ErrNativeFile, path, err)
	}
	return o, nil
}

// marshalObject writes a settings file: two spaces of indentation, keys in
// order, the characters of a shell line as they are.
func marshalObject(obj map[string]any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(obj); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
