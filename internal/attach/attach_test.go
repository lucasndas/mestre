package attach

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/pkg/clock"
)

// fakeConsole is a console no test types into: it records what is drawn and asked.
type fakeConsole struct {
	out         bytes.Buffer
	asked       []string
	interactive bool
	cols, rows  int
}

func (c *fakeConsole) Interactive() bool { return c.interactive }
func (c *fakeConsole) ReadLine(p string) (string, error) {
	c.asked = append(c.asked, p)
	return "", io.EOF
}
func (c *fakeConsole) ReadPassword(p string) (string, error) {
	c.asked = append(c.asked, p)
	return "", io.EOF
}
func (c *fakeConsole) Size() (int, int)          { return c.cols, c.rows }
func (c *fakeConsole) EnterRaw() (func(), error) { return func() {}, nil }
func (c *fakeConsole) Input() io.Reader          { return strings.NewReader("") }
func (c *fakeConsole) Output() io.Writer         { return &c.out }

func envWith(con *fakeConsole, vars map[string]string) (Env, *bytes.Buffer, *bytes.Buffer) {
	var so, se bytes.Buffer
	return Env{Stdout: &so, Stderr: &se, Console: con, Clock: clock.NewFake(clock.NewFake(clock.Real{}.Now()).Now()),
		LookupEnv: func(k string) (string, bool) { v, ok := vars[k]; return v, ok }}, &so, &se
}

// CA-ATTACH-03
// An agent never attaches: with MESTRE_AGENT_ID in the environment the command
// exits 3 not_for_agents before it asks for any login, in text and in --json.
func TestAnAgentIsRefusedBeforeAnyLoginIsAsked(t *testing.T) {
	t.Parallel()
	con := &fakeConsole{interactive: true, cols: 80, rows: 24}
	env, stdout, stderr := envWith(con, map[string]string{"MESTRE_AGENT_ID": "c1"})
	if code := Run(context.Background(), env, []string{"Dev-1"}); code != 3 {
		t.Fatalf("exit %d, want 3", code)
	}
	if len(con.asked) != 0 {
		t.Fatalf("it asked for %v before refusing", con.asked)
	}
	if !strings.HasPrefix(stderr.String(), "error: ") || !strings.Contains(stderr.String(), "\nhint: ") || stdout.Len() != 0 {
		t.Fatalf("not the two-line error: stdout %q stderr %q", stdout, stderr)
	}

	env, stdout, stderr = envWith(con, map[string]string{"MESTRE_AGENT_ID": "c1"})
	if code := Run(context.Background(), env, []string{"--json", "Dev-1"}); code != 3 {
		t.Fatalf("--json: exit %d, want 3", code)
	}
	var got struct {
		OK    bool `json:"ok"`
		Error struct{ Code string }
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || got.OK || got.Error.Code != "not_for_agents" || stderr.Len() != 0 {
		t.Fatalf("--json: %q %q (%v)", stdout, stderr, err)
	}
}

func TestTheArgumentsAreChecked(t *testing.T) {
	t.Parallel()
	for name, args := range map[string][]string{
		"no character":       {},
		"two characters":     {"a", "b"},
		"unknown flag":       {"a", "--bogus"},
		"flag without value": {"a", "--workspace"},
		"key not a control":  {"a", "--detach-key", "x"},
		"key is Esc":         {"a", "--detach-key", "ctrl-["},
		"server over http":   {"a", "--server", "http://mestre.local:7443"},
	} {
		con := &fakeConsole{interactive: true, cols: 80, rows: 24}
		env, _, stderr := envWith(con, nil)
		if code := Run(context.Background(), env, args); code != 2 {
			t.Errorf("%s: exit %d, want 2 (%q)", name, code, stderr)
		}
		if len(con.asked) != 0 {
			t.Errorf("%s: it asked for a login", name)
		}
	}
	o, err := parseArgs([]string{"--write", "Dev-1", "--workspace=meu-app", "--detach-key", "ctrl-a", "--server", "x:1"}, "principal")
	if err != nil || !o.write || o.character != "Dev-1" || o.workspace != "meu-app" || o.detach != 1 || o.server != "x:1" {
		t.Fatalf("parsed %+v (%v)", o, err)
	}
	if o, err := parseArgs([]string{"Dev-1"}, "principal"); err != nil || o.detach != 0x1d || o.write {
		t.Fatalf("defaults %+v (%v)", o, err)
	}
}

func TestAttachNeedsATerminal(t *testing.T) {
	t.Parallel()
	con := &fakeConsole{interactive: false}
	env, _, _ := envWith(con, nil)
	if code := Run(context.Background(), env, []string{"Dev-1", "--dir", t.TempDir()}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

// CA-ATTACH-04
// A window of 120x30 on a terminal of 200x50 with the cursor on row 45 shows the
// rows around the cursor and the line of state with the size, and nothing the CLI
// draws asks the terminal of the person for another size.
func TestASmallWindowFollowsTheCursorAndNeverAsksForAResize(t *testing.T) {
	t.Parallel()
	s := newScreen(200, 50)
	defer s.Close()
	var feed strings.Builder
	for row := 1; row <= 44; row++ {
		feed.WriteString("linha-" + pad(row) + "\r\n")
	}
	feed.WriteString("linha-045 cursor aqui") // row 45 (1-based), where the cursor stays
	s.Write([]byte(feed.String()))

	frame := s.Frame(120, 30, "read-only", true)
	for _, want := range []string{"linha-045 cursor aqui", "linha-040", "linha-049"[:0] + "linha-032", "terminal 200x50", "window 120x30"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the frame lacks %q", want)
		}
	}
	if strings.Contains(frame, "linha-001") {
		t.Error("the frame shows rows far from the cursor")
	}
	// the cursor sits where the agent's is, inside the view
	if !strings.Contains(frame, "\x1b[?25h") {
		t.Error("the cursor is not shown")
	}
	// what the CLI writes has no request to resize the window (CSI 8 ; rows ; cols t) or the PTY
	if windowOps.MatchString(frame) {
		t.Error("the frame asks for a resize")
	}
	if v := viewFor(200, 50, 120, 29, 20, 45); v.rows != 29 || v.cols != 120 || v.top != 21 || v.left != 0 {
		t.Fatalf("view %+v", v)
	}
	if v := viewFor(200, 50, 300, 80, 0, 0); v.cols != 200 || v.rows != 50 || v.left != 0 || v.top != 0 {
		t.Fatalf("a big window shows it all: %+v", v)
	}
}

// windowOps matches the sequences that ask a terminal to resize (CSI … t, such as CSI 8 ; rows ; cols t).
var windowOps = regexp.MustCompile("\x1b\\[[0-9;]*t")

func pad(n int) string {
	s := "000" + itoa(n)
	return s[len(s)-3:]
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestAWideCharacterTakesItsTwoColumns(t *testing.T) {
	t.Parallel()
	s := newScreen(10, 3)
	defer s.Close()
	s.Write([]byte("a中b"))
	frame := s.Frame(10, 4, "", false)
	if !strings.Contains(frame, "a中b ") {
		t.Fatalf("the column after the wide character was drawn as a space: %q", frame)
	}
}

func TestAHalfCharacterTypedIsHeldUntilItIsWhole(t *testing.T) {
	t.Parallel()
	b := []byte("é") // 0xC3 0xA9
	if n := incompleteTail(b[:1]); n != 1 {
		t.Fatalf("held %d of the first byte", n)
	}
	if n := incompleteTail(b); n != 0 {
		t.Fatalf("held %d of a whole character", n)
	}
	if n := incompleteTail([]byte("abc")); n != 0 {
		t.Fatalf("held %d of ascii", n)
	}
}
