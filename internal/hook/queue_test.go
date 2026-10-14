package hook_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

func queueFile(t *testing.T, dir string, n int, kind string, rec any) string {
	t.Helper()
	name := fmt.Sprintf("%020d-%016x-%s.json", n, n, kind)
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o640); err != nil {
		t.Fatal(err)
	}
	return name
}

func info(n int) hook.QueueRecord {
	return hook.QueueRecord{V: hook.QueueVersion, Kind: hook.QueuedInfo, Event: providers.Event{
		Version: providers.EventVersion, Event: providers.EventTurnEnd, AgentID: agent, TurnID: fmt.Sprint(n)}}
}

// The engine reads the queue oldest first; over the limit it drops the
// oldest informative events and never a decision; it removes every file it
// read and every file the dispatcher could not have written, and leaves a
// file too young to be whole for the next time.
func TestTheEngineDrainsTheQueue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dec := hook.QueueRecord{V: hook.QueueVersion, Kind: hook.QueuedDecision, Event: providers.Event{
		Version: providers.EventVersion, Event: providers.EventPreTool, AgentID: agent,
		Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: "npm test"}},
		Decision: &hook.QueuedVerdict{Verdict: classes.Allow}}
	queueFile(t, dir, 1, "i", info(1))
	queueFile(t, dir, 2, "d", dec)
	queueFile(t, dir, 3, "i", info(3))
	queueFile(t, dir, 4, "i", info(4))
	queueFile(t, dir, 5, "d", info(5)) // the name says decision, the record does not
	if err := os.WriteFile(filepath.Join(dir, "not-ours.json"), []byte("{}"), 0o640); err != nil {
		t.Fatal(err)
	}
	var got []string
	n, err := hook.Drain(dir, 3, farFuture, slog.New(slog.DiscardHandler), func(r hook.QueueRecord) {
		got = append(got, r.Kind+r.Event.TurnID)
	})
	// Of five names, three kept: the decisions (2 and 5) and the newest
	// informative one; 5 is not a valid record.
	if err != nil || n != 2 || strings.Join(got, ",") != "d,i4" {
		t.Fatalf("drained %d %v (%v)", n, got, err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("files left: %v", left)
	}
	if runtime.GOOS != "windows" {
		// A link an agent left in place of a file is not followed.
		if err := os.Symlink("/etc/passwd", filepath.Join(dir, fmt.Sprintf("%020d-%016x-i.json", 6, 6))); err != nil {
			t.Fatal(err)
		}
		if n, err := hook.Drain(dir, 3, farFuture, slog.New(slog.DiscardHandler), func(hook.QueueRecord) {}); err != nil || n != 0 {
			t.Fatalf("the link was read: %d (%v)", n, err)
		}
		if left, _ := os.ReadDir(dir); len(left) != 0 {
			t.Fatalf("the link was left: %v", left)
		}
	}

	// A file just written stays for the next drain.
	young := queueFile(t, dir, 7, "i", info(7))
	if n, err := hook.Drain(dir, 3, clock.Real{}, slog.New(slog.DiscardHandler), func(hook.QueueRecord) {}); err != nil || n != 0 {
		t.Fatalf("drained %d (%v)", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, young)); err != nil {
		t.Fatalf("the young file is gone: %v", err)
	}
	// A queue that does not exist yet is empty.
	if n, err := hook.Drain(filepath.Join(dir, "none"), 3, farFuture, slog.New(slog.DiscardHandler), nil); err != nil || n != 0 {
		t.Fatalf("missing queue: %d (%v)", n, err)
	}
}

// A queued event over the size of a record is cut to fit, and marked.
func TestABigEventIsCutToFitTheQueue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mc := machine()
	mc.Agents, mc.Queue = filepath.Join(dir, "agentes"), dir
	stdin, _ := json.Marshal(map[string]any{"session_id": "s1", "prompt_id": "p1", "cwd": worktree(), "tool_name": "Bash",
		"tool_input": map[string]any{"command": "echo " + strings.Repeat("x", 200<<10)}})
	res := call{provider: providers.ClaudeCode, event: "PreToolUse", stdin: stdin, dial: down, files: hook.OSFiles{}, machine: mc}.run(t.Context())
	if v, _ := decision(t, providers.ClaudeCode, res); v != "deny" {
		t.Fatalf("without a snapshot, a command is %s", v)
	}
	var recs []hook.QueueRecord
	if _, err := hook.Drain(dir, hook.MaxQueued, farFuture, slog.New(slog.DiscardHandler), func(r hook.QueueRecord) { recs = append(recs, r) }); err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || !recs[0].Truncated || recs[0].Event.Tool.RawInput != nil || len(recs[0].Event.Tool.Command) > 8<<10 {
		t.Fatalf("records = %d", len(recs))
	}
}

// CA-HOOK-06: the plan of the Linux installation gives agentes/ mode 0750
// and policy.json 0640, both of the owner mestre and the group
// mestre-agente, and the hook queue 1730.
func TestTheLinuxPlanOfTheFolders(t *testing.T) {
	t.Parallel()
	plan := hook.LinuxPlan("principal")
	by := map[string]hook.PlanEntry{}
	for _, e := range plan {
		by[filepath.ToSlash(e.Path)] = e
	}
	check := func(path string, dir bool, mode fs.FileMode) {
		t.Helper()
		e, ok := by[path]
		if !ok {
			t.Fatalf("no %s in the plan: %+v", path, plan)
		}
		if e.Dir != dir || e.Mode != mode || e.Owner != "mestre" || e.Group != "mestre-agente" {
			t.Fatalf("%s: dir %v mode %v (%o) %s:%s", path, e.Dir, e.Mode, e.Mode.Perm(), e.Owner, e.Group)
		}
	}
	check("/var/lib/mestre/principal/agentes", true, 0o750)
	check("/var/lib/mestre/principal/agentes/<id>/policy.json", false, 0o640)
	check("/var/spool/mestre/principal/hook", true, fs.ModeSticky|0o730)
	// What the dispatcher reads is where the plan puts it.
	m := hook.MachineOf("linux", "", "principal")
	if filepath.ToSlash(m.Agents) != "/var/lib/mestre/principal/agentes" || filepath.ToSlash(m.Queue) != "/var/spool/mestre/principal/hook" {
		t.Fatalf("machine = %+v", m)
	}
}

// The instance of the dispatcher comes from MESTRE_ENDPOINT; an endpoint
// that is not an installed instance's socket names none.
func TestTheInstanceComesFromTheEndpoint(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		if inst, ok := hook.InstanceOf("windows", filepath.Join(pd, "Mestre", "teste", "run", "cli.sock")); !ok || inst != "teste" {
			t.Fatalf("instance %q %v", inst, ok)
		}
	} else if inst, ok := hook.InstanceOf("linux", "/run/mestre/teste/cli.sock"); !ok || inst != "teste" {
		t.Fatalf("instance %q %v", inst, ok)
	}
	if _, ok := hook.InstanceOf(runtime.GOOS, filepath.Join(t.TempDir(), "run", "cli.sock")); ok {
		t.Fatal("a --dir endpoint named an instance")
	}
}
