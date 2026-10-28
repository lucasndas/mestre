package i18n

import (
	"encoding/json"
	"fmt"
)

// parseFlat reads a catalog file: one JSON object of code to text.
func parseFlat(name string, b []byte) (map[string]string, error) {
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return m, nil
}
