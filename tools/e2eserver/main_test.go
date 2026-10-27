package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

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

func TestABindThatIsNotAnIPIsRefusedBeforeAnythingStarts(t *testing.T) {
	t.Parallel()
	err := runWith(context.Background(), io.Discard, io.Discard, nil, runOptions{Dir: t.TempDir(), Bind: "mestre.local"})
	if err == nil || !strings.Contains(err.Error(), `-bind "mestre.local" is not an IP`) {
		t.Fatalf("runWith = %v", err)
	}
}

func TestAFreePortIsTakenOnTheIPOfTheBind(t *testing.T) {
	t.Parallel()
	port, err := freePort("127.0.0.1")
	if err != nil || port < portLow || port >= portLow+portSpan {
		t.Fatalf("freePort = %d, %v", port, err)
	}
	if _, err := freePort("192.0.2.1"); err == nil {
		t.Fatal("a port on an IP of no interface of the machine was handed out")
	}
}
