package main

import "testing"

func TestTheReadyLineRoundTripsAndNothingElseParses(t *testing.T) {
	t.Parallel()
	url, code, ok := parseReady(readyLine("https://127.0.0.1:7443", "ABCD2345EFGH") + "\n")
	if !ok || url != "https://127.0.0.1:7443" || code != "ABCD2345EFGH" {
		t.Fatalf("parseReady = %q %q %v", url, code, ok)
	}
	for _, line := range []string{"", "E2E_READY", "E2E_READY url= setup_code=x", "E2E_READY url=https://x setup_code=", `{"level":"INFO"}`} {
		if _, _, ok := parseReady(line); ok {
			t.Errorf("%q parses as a ready line", line)
		}
	}
}
