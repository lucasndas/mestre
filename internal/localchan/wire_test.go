package localchan_test

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/pkg/clock"
)

func TestTheExitCodeOfAResponse(t *testing.T) {
	t.Parallel()
	nine := 9
	cases := []struct {
		name string
		resp localchan.Response
		want int
	}{
		{"ok", localchan.OK("x"), 0},
		{"ok pending", localchan.OK("x").WithExit(nine), 9},
		{"explicit exit wins", localchan.Fail("no_capable_machine", "m", "").WithExit(4), 4},
		{"internal", localchan.Fail(localchan.CodeInternal, "m", ""), 1},
		{"usage", localchan.Fail(localchan.CodeUsage, "m", ""), 2},
		{"denied", localchan.Fail(localchan.CodeDenied, "m", ""), 3},
		{"not_found", localchan.Fail(localchan.CodeNotFound, "m", ""), 4},
		{"unavailable", localchan.Fail(localchan.CodeUnavailable, "m", ""), 5},
		{"timeout", localchan.Fail(localchan.CodeTimeout, "m", ""), 6},
		{"conflict", localchan.Fail(localchan.CodeConflict, "m", ""), 7},
		{"limit", localchan.Fail(localchan.CodeLimit, "m", ""), 8},
		{"pending", localchan.Fail(localchan.CodePending, "m", ""), 9},
		{"not_identified", localchan.Fail(localchan.CodeNotIdentified, "m", ""), 3},
		{"engine_unreachable", localchan.Fail(localchan.CodeEngineUnreachable, "m", ""), 5},
		{"protocol_mismatch", localchan.Fail(localchan.CodeProtocolMismatch, "m", ""), 5},
		{"too_large", localchan.Fail(localchan.CodeTooLarge, "m", ""), 8},
		{"bad_request", localchan.Fail(localchan.CodeBadRequest, "m", ""), 1},
		{"an unknown code without exit", localchan.Fail("brand_new", "m", ""), 1},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.resp.ExitCode(); got != tt.want {
				t.Fatalf("exit = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestTheLinesOnTheWire(t *testing.T) {
	t.Parallel()
	req := localchan.Request{V: 1, ID: "01J", AgentID: "a1", Cmd: "ask", Args: localchan.TextArgs("Revisor", "revise o PR"),
		Flags: map[string]any{"timeout": "10m"}, Cwd: "/w"}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"v":1,"id":"01J","agent_id":"a1","cmd":"ask","args":["Revisor","revise o PR"],"flags":{"timeout":"10m"},"cwd":"/w"}`
	if string(b) != want {
		t.Fatalf("request = %s", b)
	}
	got, ok := req.Strings()
	if !ok || len(got) != 2 || got[1] != "revise o PR" {
		t.Fatalf("Strings = %v, %v", got, ok)
	}
	hook := localchan.Request{Args: []json.RawMessage{json.RawMessage(`{"event":"x"}`)}}
	if _, ok := hook.Strings(); ok {
		t.Fatal("an object argument was taken for text")
	}
}

func TestIdsAreULIDs(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
	re := regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
	a, err := localchan.NewID(clk)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := localchan.NewID(clk)
	if !re.MatchString(a) || a == b {
		t.Fatalf("ids %q and %q", a, b)
	}
	clk.Advance(time.Second)
	c, _ := localchan.NewID(clk)
	if a[:10] >= c[:10] { // the first 10 characters are the time, so ids sort by it
		t.Fatalf("a later id sorts before: %q then %q", a, c)
	}
}

func TestEndpoints(t *testing.T) {
	t.Parallel()
	cases := []struct{ url, path string }{
		{"unix:///run/mestre/principal/cli.sock", "/run/mestre/principal/cli.sock"},
		{"unix://C:/ProgramData/Mestre/principal/run/cli.sock", `C:/ProgramData/Mestre/principal/run/cli.sock`},
	}
	for _, tt := range cases {
		got, err := localchan.ParseEndpoint(tt.url)
		if err != nil || got != filepath.FromSlash(tt.path) {
			t.Errorf("ParseEndpoint(%q) = %q, %v", tt.url, got, err)
		}
		if back := localchan.EndpointURL(got); back != tt.url {
			t.Errorf("EndpointURL(%q) = %q, want %q", got, back, tt.url)
		}
	}
	for _, bad := range []string{"", "tcp://127.0.0.1:1", "unix://relative/cli.sock", "unix://", "/run/x.sock"} {
		if _, err := localchan.ParseEndpoint(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestTheEndpointOfTheEnvironment(t *testing.T) {
	t.Parallel()
	env := map[string]string{localchan.EnvEndpoint: "unix:///tmp/other/cli.sock"}
	got, err := localchan.EndpointFromEnv(func(k string) string { return env[k] })
	if err != nil || got != filepath.FromSlash("/tmp/other/cli.sock") {
		t.Fatalf("with the variable: %q, %v", got, err)
	}
	def, err := localchan.EndpointFromEnv(func(string) string { return "" })
	if err != nil || !strings.Contains(def, "principal") || !strings.HasSuffix(def, "cli.sock") {
		t.Fatalf("without it: %q, %v", def, err)
	}
	want, _ := localchan.SocketPath("seg")
	if !strings.Contains(want, "seg") {
		t.Fatalf("the path of another instance: %q", want)
	}
	if _, err := localchan.SocketPath("../etc"); err == nil {
		t.Fatal("an instance name with a path was accepted")
	}
}
