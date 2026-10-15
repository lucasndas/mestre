package providers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

func stripperOf(t *testing.T, a providers.Adapter) providers.HookStripper {
	t.Helper()
	s, ok := a.(providers.HookStripper)
	if !ok {
		t.Fatalf("%T cannot strip its hooks", a)
	}
	return s
}

func sameValue(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("%s: %v", a, err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return reflect.DeepEqual(x, y)
}

// Merging and then stripping gives back what the person had, whatever the
// form; a file with nothing of the Mestre comes back byte for byte.
func TestStripTakesOnlyTheMestrePartOut(t *testing.T) {
	t.Parallel()
	req := providers.HookFilesRequest{OS: "linux", Root: "/wt", Binary: stableBinary}
	cases := []struct {
		name   string
		a      providers.Adapter
		person string
	}{
		{"claude-code", providers.NewClaudeCode(providers.Options{}), `{"permissions": {"allow": ["Bash(make test)"]}, "hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "./minha-checagem.sh"}]}]}}`},
		{"claude-code without hooks", providers.NewClaudeCode(providers.Options{}), `{"permissions": {"allow": ["Bash(make test)"]}}`},
		{"antigravity", providers.NewAntigravity(providers.Options{}), `{"projeto": {"enabled": true, "Stop": []}}`},
		{"antigravity empty", providers.NewAntigravity(providers.Options{}), `{}`},
	}
	for _, c := range cases {
		files, err := c.a.HookFiles(req)
		if err != nil || len(files) != 1 {
			t.Fatalf("%s: %v, %v", c.name, files, err)
		}
		merged, err := files[0].Merge([]byte(c.person))
		if err != nil || !strings.Contains(string(merged), "mestre") {
			t.Fatalf("%s: merge = %s, %v", c.name, merged, err)
		}
		s := stripperOf(t, c.a)
		got, err := s.StripHooks(files[0].Path, merged)
		if err != nil || !sameValue(t, got, []byte(c.person)) {
			t.Errorf("%s: strip(merge(person)) = %s, %v; want the person's %s", c.name, got, err, c.person)
		}
		if bytes.Contains(got, []byte("hook --provider")) {
			t.Errorf("%s: a hook of the Mestre is left: %s", c.name, got)
		}
		again, err := s.StripHooks(files[0].Path, []byte(c.person))
		if err != nil || string(again) != c.person {
			t.Errorf("%s: strip of a file without the Mestre's part = %q, %v; want it as it was", c.name, again, err)
		}
	}
}

// A file that is not valid is an error, never rewritten.
func TestStripRefusesAFileThatIsNotValid(t *testing.T) {
	t.Parallel()
	for _, a := range []providers.Adapter{providers.NewClaudeCode(providers.Options{}), providers.NewAntigravity(providers.Options{})} {
		if _, err := stripperOf(t, a).StripHooks("/wt/f.json", []byte("{isto não é json")); !errors.Is(err, providers.ErrNativeFile) {
			t.Errorf("%T: strip of an invalid file = %v; want ErrNativeFile", a, err)
		}
	}
}

// The handlers and the statusLine of the Mestre go; the person's statusLine
// and handlers stay.
func TestStripKeepsThePersonsStatusLineAndHandlers(t *testing.T) {
	t.Parallel()
	a := providers.NewClaudeCode(providers.Options{})
	const person = `{"statusLine": {"type": "command", "command": "./minha-barra.sh"}, "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "./fim.sh"}]}]}}`
	files, err := a.HookFiles(providers.HookFilesRequest{OS: "linux", Root: "/wt", Binary: stableBinary})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := files[0].Merge([]byte(person))
	if err != nil {
		t.Fatal(err)
	}
	got, err := stripperOf(t, a).StripHooks(files[0].Path, merged)
	if err != nil || !sameValue(t, got, []byte(person)) {
		t.Fatalf("strip = %s, %v; want %s", got, err, person)
	}
}
