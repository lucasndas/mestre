//go:build full && windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/testkit/fakeagent"
)

// CA-DUBLES-13: the double runs an Antigravity hook as the real agy does,
// cmd /c with the line as one Go-quoted argument, not the cmd /s /c form.
func TestAntigravityHookRunsAsCmdSlashCWithGoQuoting(t *testing.T) {
	t.Parallel()
	const line = `echo "two words" & echo done`
	c := hookCommand(context.Background(), fakeagent.Antigravity, line, t.TempDir(), nil)
	if c.SysProcAttr != nil && c.SysProcAttr.CmdLine != "" {
		t.Fatalf("custom command line %q: the real agy lets Go quote the arguments", c.SysProcAttr.CmdLine)
	}
	if want := []string{"cmd", "/c", line}; !reflect.DeepEqual(c.Args, want) {
		t.Fatalf("Args = %q, want %q", c.Args, want)
	}
	for _, p := range []fakeagent.Provider{fakeagent.Claude, fakeagent.Codex} {
		if o := hookCommand(context.Background(), p, line, t.TempDir(), nil); len(o.Args) > 1 && o.Args[1] == "/c" {
			t.Fatalf("%v hook uses cmd /c", p)
		}
	}
	// A line with quotes reaches cmd with the quoting of Go, as in the real agy.
	q, err := hookCommand(context.Background(), fakeagent.Antigravity, `echo "x"`, t.TempDir(), nil).CombinedOutput()
	if err != nil || !strings.Contains(string(q), `\"x\"`) {
		t.Fatalf(`echo "x" through cmd /c = %q, %v; want \"x\"`, q, err)
	}
	out, err := hookCommand(context.Background(), fakeagent.Antigravity, "echo hello", t.TempDir(), nil).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "hello") {
		t.Fatalf("echo through cmd /c = %q, %v", out, err)
	}
}

// CA-DUBLES-13 (the line the adapter generates)
//
// The hook line the Antigravity adapter generates runs through the double as
// the real agy runs it, with a fake mestre in a folder whose name has a
// blank (the line takes its 8.3 name): the hook runs and calls the mestre.
func TestTheGeneratedAntigravityHookLineRunsThroughTheDouble(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	folder := filepath.Join(root, "mestre bin")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(folder, "mestre.bat")
	marker := filepath.Join(root, "ran.txt")
	body := "@echo off\r\necho %* > \"" + marker + "\"\r\necho {\"decision\":\"allow\"}\r\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	files, err := providers.NewAntigravity(providers.Options{}).HookFiles(providers.HookFilesRequest{OS: "windows", Root: root, Binary: stub})
	if err != nil {
		t.Skipf("no usable path for the line on this machine (no 8.3 name): %v", err)
	}
	doc, err := files[0].Merge(nil)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]any
	if err := json.Unmarshal(doc, &top); err != nil {
		t.Fatal(err)
	}
	line := findCommand(top["mestre"], "PreToolUse")
	if line == "" {
		t.Fatalf("no PreToolUse command in %s", doc)
	}
	env := append(os.Environ(), "MESTRE_AGENT_ID=a1")
	out, err := hookCommand(context.Background(), fakeagent.Antigravity, line, root, env).CombinedOutput()
	if err != nil {
		t.Fatalf("the generated line failed under cmd /c: %v\n%s\nline: %s", err, out, line)
	}
	if b, err := os.ReadFile(marker); err != nil || !strings.Contains(string(b), "hook --provider antigravity --event PreToolUse") {
		t.Fatalf("the fake mestre did not run with the hook arguments: %q, %v\nline: %s\n%s", b, err, line, out)
	}
}

// findCommand finds the command of the first handler under group[event].
func findCommand(group any, event string) string {
	m, _ := group.(map[string]any)
	var walk func(v any) string
	walk = func(v any) string {
		switch x := v.(type) {
		case map[string]any:
			if c, ok := x["command"].(string); ok {
				return c
			}
			for _, e := range x {
				if c := walk(e); c != "" {
					return c
				}
			}
		case []any:
			for _, e := range x {
				if c := walk(e); c != "" {
					return c
				}
			}
		}
		return ""
	}
	return walk(m[event])
}
