package app

import (
	"encoding/json"
	"testing"
)

// The Citadel refuses at /join a sheet the registry could not read later.
// CA-GUILDA-17
func TestCheckJoinSheetRefusesWhatTheHelloWouldRefuse(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string]string{
		"wrong types":      `{"os":5,"arch":["x"],"providers":"no"}`,
		"null":             `null`,
		"an array":         `[1]`,
		"missing required": `{"os":"linux"}`,
		"unknown system":   `{"os":"plan9","arch":"amd64","providers":[]}`,
	} {
		if err := checkJoinSheet(json.RawMessage(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := checkJoinSheet(json.RawMessage(`{"os":"linux","arch":"amd64","providers":[]}`)); err != nil {
		t.Errorf("a good sheet: %v", err)
	}
}
