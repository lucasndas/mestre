package machinesheet_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/testkit/sheetfake"
	"github.com/lucasndas/mestre/pkg/clock"
)

func withStore(s machinesheet.SheetStore) func(*machinesheet.AgentConfig) {
	return func(c *machinesheet.AgentConfig) { c.Store = s }
}

// CA-FMAQ-01
// CA-FMAQ-01: a probe that fails as the engine comes up does not tell the Guild
// the machine has no providers; it keeps what the last run reported.
func TestAProbeThatFailsAtStartupKeepsTheSheetOfTheLastRun(t *testing.T) {
	t.Parallel()
	store := machinesheet.FileStore(filepath.Join(t.TempDir(), "ficha.json"))
	m := sheetfake.Linux("linux-pc")
	first := newAgent(t, clock.NewFake(day0), m.Sources(), withStore(store))
	if _, err := first.Hello(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := first.Current().Providers
	if len(want) == 0 || !want[0].Profiles[0].LoggedIn {
		t.Fatalf("setup: the fake machine has providers %+v", want)
	}

	// The engine restarts and the login monitor is not answering yet.
	m.Fail(sheetfake.PartProviders, errors.New("the monitor is starting"))
	m.SetProviders()
	second := newAgent(t, clock.NewFake(day0), m.Sources(), withStore(store))
	obj, err := second.Hello(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var got machinesheet.Sheet
	if err := unmarshal(obj, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Providers) != len(want) || !got.Providers[0].Profiles[0].LoggedIn {
		t.Errorf("the hello says providers %+v, want the last run's %+v", got.Providers, want)
	}
}

// CA-FMAQ-01
// The revision goes on above the last one sent even when the clock went back
// between two runs, so the patches are not older than the sheet of the Citadel.
func TestTheRevisionGoesOnAfterARestartWhenTheClockWentBack(t *testing.T) {
	t.Parallel()
	store := machinesheet.FileStore(filepath.Join(t.TempDir(), "ficha.json"))
	m := sheetfake.Linux("linux-pc")
	first := newAgent(t, clock.NewFake(day0.Add(24*time.Hour)), m.Sources(), withStore(store))
	rec := &recorder{}
	first.Attach(rec)
	if _, err := first.Hello(t.Context()); err != nil {
		t.Fatal(err)
	}
	m.SetTranscription(true)
	if err := first.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	last := revOf(t, rec.patches()[0])

	second := newAgent(t, clock.NewFake(day0), m.Sources(), withStore(store)) // a day behind
	rec2 := &recorder{}
	second.Attach(rec2)
	hello, err := second.Hello(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if revOf(t, hello) <= last {
		t.Errorf("hello rev %d does not pass the %d of the last run: the patches that wait in the outbox would win", revOf(t, hello), last)
	}
	m.SetTranscription(false)
	if err := second.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := revOf(t, rec2.patches()[0]); got <= revOf(t, hello) {
		t.Errorf("patch rev %d does not pass the hello's %d", got, revOf(t, hello))
	}
}

// A store that cannot be read or written does not stop the agent.
func TestAStoreThatFailsDoesNotStopTheAgent(t *testing.T) {
	t.Parallel()
	m := sheetfake.Linux("linux-pc")
	bad := machinesheet.FileStore(filepath.Join(t.TempDir(), "no", "such", "\x00dir", "ficha.json"))
	a := newAgent(t, clock.NewFake(day0), m.Sources(), withStore(bad))
	if _, err := a.Hello(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// CA-FMAQ-01
// A sheet that big is refused: its row would be a record that can only go to
// the Towers by full copy (CA-REPL-03).
func TestASheetOverTheLimitIsRefused(t *testing.T) {
	t.Parallel()
	c := newCitadel(t)
	addMachine(t, c.db, "tower1", "Tower", "")
	m := sheetfake.Linux("linux-pc")
	if err := c.hello(t, "tower1", towerSheet(t, m, "tower1", "").Object()); err != nil {
		t.Fatal(err)
	}
	before := lastLSN(t, c)

	big := towerSheet(t, m, "tower1", "")
	big.Rev = 200
	big.Tools = map[string]string{"git": strings.Repeat("x", machinesheet.MaxBytes)}
	for name, send := range map[string]func() error{
		"hello": func() error { return c.hello(t, "tower1", big.Object()) },
		"patch": func() error {
			return c.patch(t, "tower1", protocol.Object{"rev": big.Object()["rev"], "tools": big.Object()["tools"]})
		},
	} {
		var pe *protocol.Error
		if err := send(); !errors.As(err, &pe) || pe.Code != protocol.CodeInvalidPayload {
			t.Errorf("%s with a sheet over the limit = %v, want invalid_payload", name, err)
		}
	}
	if got := lastLSN(t, c); got != before {
		t.Errorf("the refused sheet left %d records", got-before)
	}
}

func TestADesktopSessionIsOneOfTheUserWithAGraphicalType(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]bool{
		"# This is private data.\nUID=1000\nUSER=ana\nACTIVE=1\nSTATE=active\nCLASS=user\nTYPE=wayland\n": true,
		"STATE=online\nCLASS=user\nTYPE=x11\n":       true,
		"STATE=closing\nCLASS=user\nTYPE=x11\n":      false,
		"STATE=active\nCLASS=user\nTYPE=tty\n":       false,
		"STATE=active\nCLASS=user\nTYPE=unspecified": false,
		"STATE=active\nCLASS=greeter\nTYPE=wayland":  false,
		"": false,
	} {
		if got := machinesheet.ParseLogindSession(in); got != want {
			t.Errorf("ParseLogindSession(%q) = %v, want %v", in, got, want)
		}
	}
}

func unmarshal(o protocol.Object, v any) error {
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func lastLSN(t *testing.T, c *citadelRig) uint64 {
	t.Helper()
	n, err := c.db.LastLSN(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return n
}
