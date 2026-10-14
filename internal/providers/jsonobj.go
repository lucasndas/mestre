package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// jsonObject is a JSON object read with its keys in file order and its
// values untouched, so the generated files change only the key the Mestre
// owns and keep the others as the person wrote them (docs/provedores/adaptador.md:
// every generation merges only the managed part).
type jsonObject struct {
	keys []string
	vals map[string]json.RawMessage
	dup  map[string]bool // keys that appeared more than once in the file
}

// parseJSONObject reads cur, an object; a missing or blank file is an empty
// object. Anything else, including a second value after the object, is an
// error and the file is never rewritten.
func parseJSONObject(cur []byte) (*jsonObject, error) {
	o := &jsonObject{vals: map[string]json.RawMessage{}, dup: map[string]bool{}}
	if len(bytes.TrimSpace(cur)) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(cur))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("not a JSON object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if _, seen := o.vals[key]; seen {
			o.dup[key] = true
		} else {
			o.keys = append(o.keys, key)
		}
		o.vals[key] = raw
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("data after the JSON object")
	}
	return o, nil
}

func (o *jsonObject) get(key string) (json.RawMessage, bool) {
	v, ok := o.vals[key]
	return v, ok
}

// set puts v under key, in place when the key exists and at the end
// otherwise, once.
func (o *jsonObject) set(key string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = bytes.TrimSpace(buf.Bytes())
	delete(o.dup, key)
	return nil
}

// bytes renders the object indented by two spaces, with a final newline.
func (o *jsonObject) bytes() ([]byte, error) {
	var compact bytes.Buffer
	compact.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			compact.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		compact.Write(kb)
		compact.WriteByte(':')
		compact.Write(o.vals[k])
	}
	compact.WriteByte('}')
	var out bytes.Buffer
	if err := json.Indent(&out, compact.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// sameJSON reports whether two JSON values are equal ignoring formatting.
func sameJSON(a, b []byte) bool {
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return false
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}
