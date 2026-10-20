package tables_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

// run calls a command of the tables as the character agent, the way the
// forwarder of the Citadel does.
func (w *world) run(agent, cmd string, flags map[string]any, args ...string) localchan.Response {
	w.t.Helper()
	h, ok := w.svc.Handlers()[cmd]
	if !ok {
		w.t.Fatalf("no handler for %q", cmd)
	}
	if flags == nil {
		flags = map[string]any{}
	}
	return h.Handle(w.ctx, &localchan.Call{
		Request: localchan.Request{Cmd: cmd, AgentID: agent, Args: localchan.TextArgs(args...), Flags: flags},
		Caller:  localchan.Caller{Identified: true, AgentID: agent},
	})
}

func errCode(r localchan.Response) string {
	if r.Error == nil {
		return ""
	}
	return r.Error.Code
}

func dataOf(t *testing.T, r localchan.Response) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Data, &m); err != nil {
		t.Fatalf("data %s: %v", r.Data, err)
	}
	return m
}

// CA-GM-05
// CA-CLI-GM-01: a table master that runs `mestre table open --epic 42`
// leaves with 3 level_denied, and nothing is opened.
func TestATableMasterCannotOpenATable(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	w.gh.add(43, "Outro", "epico")
	w.open(43)
	master, _, _ := w.reg.TableMaster(w.ctx, w.slug(), "43-outro")
	for _, cmd := range []string{"table open", "table list", "table status", "table close"} {
		r := w.run(master.ID, cmd, map[string]any{"epic": "42"}, "43-outro")
		if r.OK || errCode(r) != "level_denied" || r.ExitCode() != 3 {
			t.Errorf("%s by a table master = %+v (exit %d)", cmd, r.Error, r.ExitCode())
		}
	}
	if ts, _ := w.svc.List(w.ctx, w.slug()); len(ts) != 1 {
		t.Errorf("tables = %d", len(ts))
	}
	// Not identified: refused too.
	h := w.svc.Handlers()["table open"]
	if r := h.Handle(w.ctx, &localchan.Call{Request: localchan.Request{Cmd: "table open", Flags: map[string]any{"epic": "42"}}}); r.OK || r.ExitCode() == 0 {
		t.Errorf("an unknown process opened a table: %+v", r)
	}
}

// CA-MESAS-10 through the command: the epic 7 is not in the fake GitHub, so
// `mestre table open --epic 7` leaves with 4 epic_not_found and creates
// neither an epic nor a branch. The other refusals have their codes too.
func TestTheCommandOpenRefusesWithTheCodesOfTheDocument(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	r := w.run(w.gm.ID, "table open", map[string]any{"epic": "7"})
	if r.OK || errCode(r) != "epic_not_found" || r.ExitCode() != 4 {
		t.Fatalf("epic 7 = %+v (exit %d)", r.Error, r.ExitCode())
	}
	if len(w.gh.epics) != 0 {
		t.Errorf("an epic was created: %+v", w.gh.epics)
	}
	if refs, _ := w.mirror().ListRefs(w.ctx, "refs/heads/epico/"); len(refs) != 0 {
		t.Errorf("branches = %+v", refs)
	}
	if r := w.run(w.gm.ID, "table open", map[string]any{"epic": "abc"}); errCode(r) != localchan.CodeUsage || r.ExitCode() != 2 {
		t.Errorf("a bad number = %+v", r.Error)
	}

	w.gh.add(42, "Login de usuários", "epico")
	r = w.run(w.gm.ID, "table open", map[string]any{"epic": "42"})
	if !r.OK {
		t.Fatalf("open = %+v", r.Error)
	}
	d := dataOf(t, r)
	master := d["master"].(map[string]any)
	if d["name"] != "42-login-de-usuarios" || d["branch"] != "epico/42-login-de-usuarios" || d["epic"] != float64(42) || d["worktree"] == "" ||
		master["name"] != "Mestre" || master["id"] == "" || master["machine"] != w.machine {
		t.Errorf("data = %v", d)
	}
	if r := w.run(w.gm.ID, "table open", map[string]any{"epic": "42"}); errCode(r) != "table_exists" || r.ExitCode() != 7 {
		t.Errorf("a second open = %+v (exit %d)", r.Error, r.ExitCode())
	}
	if err := w.wss.Pause(w.ctx, w.slug()); err != nil {
		t.Fatal(err)
	}
	w.gh.add(43, "Outro", "epico")
	if r := w.run(w.gm.ID, "table open", map[string]any{"epic": "43"}); errCode(r) != "workspace_not_active" || r.ExitCode() != 7 {
		t.Errorf("a paused workspace = %+v (exit %d)", r.Error, r.ExitCode())
	}
}

// `table list` and `table status` show the state with the mark of the pause,
// the epic, the branch, the table master, the machines and the PR.
func TestTheCommandsListAndStatus(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	w.gh.add(43, "Outro", "epico")
	w.open(42)
	w.open(43)
	if err := w.svc.Pause(w.ctx, w.slug(), "43-outro", tables.PauseRequest{Reason: "limite de uso", By: "usage_limit"}); err != nil {
		t.Fatal(err)
	}
	r := w.run(w.gm.ID, "table list", nil)
	if !r.OK {
		t.Fatal(r.Error)
	}
	var list struct {
		Text   string `json:"text"`
		Tables []struct {
			Name, State, Branch, Master string
			Paused                      bool
			Epic                        int
			Machines                    []string
		}
	}
	if err := json.Unmarshal(r.Data, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Tables) != 2 || list.Tables[0].Name != "42-login" || list.Tables[0].Master != "Mestre" || list.Tables[0].Machines[0] != w.machine ||
		!list.Tables[1].Paused || list.Tables[1].State != "working" {
		t.Errorf("list = %+v", list)
	}
	if !strings.Contains(list.Text, "(pausada)") {
		t.Errorf("text = %q", list.Text)
	}
	if r := w.run(w.gm.ID, "table list", map[string]any{"state": "closed"}); !r.OK || strings.Contains(string(r.Data), "42-login") {
		t.Errorf("list --state closed = %s", r.Data)
	}
	if r := w.run(w.gm.ID, "table list", map[string]any{"state": "x"}); errCode(r) != localchan.CodeUsage {
		t.Errorf("a bad state = %+v", r.Error)
	}
	r = w.run(w.gm.ID, "table status", nil, "43")
	if !r.OK {
		t.Fatal(r.Error)
	}
	d := dataOf(t, r)
	if d["name"] != "43-outro" || d["paused"] != true || d["pause_reason"] != "limite de uso" || !strings.Contains(d["text"].(string), "Mestre") {
		t.Errorf("status = %v", d)
	}
	if r := w.run(w.gm.ID, "table status", nil, "99-nada"); errCode(r) != "not_found" || r.ExitCode() != 4 {
		t.Errorf("an unknown table = %+v", r.Error)
	}
}

// `table close`: not_merged (7) without the merge, scheduled (9) while a
// character is not at a safe point, unmerged_changes (7) with the list, and
// the cancellation waits for the approval (9) and needs a reason.
func TestTheCommandCloseAnswersWithTheCodesOfTheDocument(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	tbl := w.open(42).Table
	if r := w.run(w.gm.ID, "table close", nil, tbl.ID); errCode(r) != "not_merged" || r.ExitCode() != 7 {
		t.Fatalf("close without the merge = %+v (exit %d)", r.Error, r.ExitCode())
	}
	head := commit(t, w.be, w.git, w.repoOf(tbl), "f.go", "package f\n", "feat: f")
	w.merged(tbl, head)
	dev, _ := w.dev(tbl, "Dev")
	w.chars.schedule[dev.Character.ID] = true
	r := w.run(w.gm.ID, "table close", nil, tbl.ID)
	if r.OK || errCode(r) != "scheduled" || r.ExitCode() != 9 || !strings.Contains(r.Error.Message, "Dev") {
		t.Fatalf("close with a character that has not reached its safe point = %+v (exit %d)", r.Error, r.ExitCode())
	}
	if err := w.chars.finish(w.ctx, dev.Character.ID); err != nil {
		t.Fatal(err)
	}
	w.be.write(t, w.git, w.repoOf(tbl).Dir(), "sobra.txt", "x")
	r = w.run(w.gm.ID, "table close", nil, tbl.ID)
	if r.OK || errCode(r) != "unmerged_changes" || r.ExitCode() != 7 {
		t.Fatalf("close with a file left behind = %+v (exit %d)", r.Error, r.ExitCode())
	}
	if dec, _ := r.Error.Details["decide"].([]string); len(dec) != 1 || !strings.Contains(dec[0], "sobra.txt") {
		t.Errorf("details = %+v", r.Error.Details)
	}
	r = w.run(w.gm.ID, "table close", map[string]any{"keep": true}, tbl.ID)
	if !r.OK || dataOf(t, r)["state"] != "closed" || len(dataOf(t, r)["retained"].([]any)) != 1 {
		t.Fatalf("close --keep = %+v %s", r.Error, r.Data)
	}

	w.gh.add(50, "Cancelar", "epico")
	other := w.open(50).Table
	if r := w.run(w.gm.ID, "table close", map[string]any{"cancel": true}, other.ID); errCode(r) != localchan.CodeUsage {
		t.Errorf("--cancel without a reason = %+v", r.Error)
	}
	r = w.run(w.gm.ID, "table close", map[string]any{"cancel": true, "reason": "x"}, other.ID)
	if r.OK || errCode(r) != "pending" || r.ExitCode() != 9 {
		t.Errorf("cancel without the approval = %+v (exit %d)", r.Error, r.ExitCode())
	}
	w.dest.allowed = true
	if r = w.run(w.gm.ID, "table close", map[string]any{"cancel": true, "reason": "x"}, other.ID); !r.OK || dataOf(t, r)["state"] != "cancelled" {
		t.Errorf("cancel after the approval = %+v %s", r.Error, r.Data)
	}
}
