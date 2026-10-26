package attach

import (
	"bufio"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// The cookie of the session goes only to the host the session was opened on: when
// the Citadel is found somewhere else on a reconnection, no cookie goes there.
func TestTheSessionCookieNeverGoesToAnotherHost(t *testing.T) {
	t.Parallel()
	c := newPanelClient(nil, "CLI em x", nil)
	home := &url.URL{Scheme: "https", Host: "mestre.local:7443"}
	other := &url.URL{Scheme: "https", Host: "outra.local:7443"}
	c.setBase(home)
	c.mu.Lock()
	c.token, c.tokenHost = "segredo", home.Host
	c.mu.Unlock()

	h := http.Header{}
	c.sign(h)
	if h.Get("Cookie") == "" || h.Get("Origin") != "https://mestre.local:7443" {
		t.Fatalf("headers at home: %v", h)
	}
	if !c.sameSessionHost(home) || c.sameSessionHost(other) {
		t.Fatal("the host of the session is not told from another")
	}
	c.setBase(other)
	h = http.Header{}
	c.sign(h)
	if h.Get("Cookie") != "" {
		t.Fatalf("the cookie went to another host: %v", h)
	}
}

// The client is used by the reader of the socket, the poll of the terminal and the
// logout at once.
func TestTheClientIsSafeToUseFromSeveralGoroutines(t *testing.T) {
	t.Parallel()
	c := newPanelClient(nil, "CLI em x", nil)
	c.setBase(&url.URL{Scheme: "https", Host: "a:1"})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); c.setBase(&url.URL{Scheme: "https", Host: "a:1"}) }()
		go func() { defer wg.Done(); c.sign(http.Header{}); _ = c.sameSessionHost(c.current()) }()
	}
	wg.Wait()
}

// A password pasted right after the login stays in the line reader's buffer; it is
// dropped before the password is asked and before the keys are read, so that it
// can never be typed into an agent.
func TestWhatIsBufferedAfterTheLoginLineIsDropped(t *testing.T) {
	t.Parallel()
	pr, pw := io.Pipe()
	c := &osConsole{in: nil, out: nil, rd: bufio.NewReader(pr)}
	go func() { _, _ = pw.Write([]byte("bia\nsenha-colada\nTECLAS")); pw.Close() }()
	_, _ = c.rd.Peek(1) // let the reader fill, as a paste does
	line, err := c.rd.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "bia" {
		t.Fatalf("line %q (%v)", line, err)
	}
	c.drain()
	if n := c.rd.Buffered(); n != 0 {
		t.Fatalf("%d bytes left in the buffer", n)
	}
}
