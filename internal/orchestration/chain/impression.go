package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Impress is the impression of a target: the SHA-256 of its canonical JSON
// (keys sorted, no spaces), so two spellings of the same target are one
// (docs/aprovacoes/fluxo.md#vínculo-ao-estado-exato).
func Impress(target json.RawMessage) (string, error) {
	var v any
	if err := json.Unmarshal(target, &v); err != nil {
		return "", fmt.Errorf("the target is not JSON: %w", err)
	}
	canon, err := json.Marshal(v) // maps are written with sorted keys
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), nil
}
