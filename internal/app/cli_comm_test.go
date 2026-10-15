package app_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// pair makes Dev and Revisor, connected to each other and to the master.
func (w *commWorld) pair() (dev, rev characters.Character) {
	w.t.Helper()
	dev, rev = w.recruit("Dev", "Dev Claude"), w.recruit("Revisor", "Revisor Codex")
	if r := w.run(w.master, "connect", "Dev", "Revisor"); r.code != 0 {
		w.t.Fatalf("connect: %+v", r)
	}
	return dev, rev
}

// ask runs `mestre ask` in the background and waits until the engine waits for
// the answer (its timer is armed); the result comes on the channel.
func (w *commWorld) ask(c characters.Character, args ...string) <-chan result {
	armed := w.clk.Armed()
	done := make(chan result, 1)
	go func() { done <- w.run(c, append([]string{"ask"}, args...)...) }()
	w.clk.BlockUntil(armed + 1)
	return done
}

// CA-CLI-COM-01: a target that does not answer in the default time limit: exit
// 6, status pending, and the late answer reaches the inbox of who asked.
func TestAskThatRunsOutOfTimeIsPendingAndTheLateAnswerComesToTheInbox(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev, rev := w.pair()
	done := w.ask(dev, "Revisor", "revise o PR", "--json")
	w.clk.Advance(90 * time.Second) // the default time limit of an ask
	r := <-done
	v := decodeJSON(t, r)
	if r.code != 6 || v.OK || v.Error.Code != "timeout" || v.Error.Details["status"] != "pending" || v.Error.Details["to"] != "Revisor" || v.Error.Details["request_id"] == "" {
		t.Fatalf("ask that ran out of time = %d %s", r.code, r.stdout)
	}
	if !strings.Contains(v.Error.Hint, "não reenvie") {
		t.Errorf("hint = %q", v.Error.Hint)
	}
	// The request goes on: the target answers later, in the turn that handled it.
	if r := w.run(rev, "done", "aprovado, com ressalvas"); r.code != 0 {
		t.Fatalf("done of the target: %+v", r)
	}
	w.turnEnds(rev, "")
	items, err := w.orc.Inbox.Store().All(w.ctx, dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got *inbox.Item
	for i := range items {
		if items[i].Kind == inbox.KindAnswer {
			got = &items[i]
		}
	}
	if got == nil || !strings.Contains(got.Text, "aprovado, com ressalvas") || got.Sender != "Revisor" {
		t.Fatalf("the late answer: %+v", items)
	}
	// The text mode of a request that is still pending.
	done = w.ask(dev, "Revisor", "e agora?", "--timeout", "1m")
	w.clk.Advance(time.Minute)
	if r := <-done; r.code != 6 || !strings.Contains(r.stderr, "error: sem resposta no prazo") || !strings.Contains(r.stderr, "hint: ") {
		t.Errorf("text of the pending ask: %+v", r)
	}
}

// A normal ask: the answer is the final text of the turn of the target, in the
// text and in --json; --no-wait delivers and returns.
func TestAskAnswersAndNoWaitReturnsAtOnce(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev, rev := w.pair()
	done := w.ask(dev, "Revisor", "tudo certo?")
	w.turnEnds(rev, "sim, tudo certo")
	if r := <-done; r.code != 0 || strings.TrimSpace(r.stdout) != "sim, tudo certo" {
		t.Fatalf("ask = %+v", r)
	}
	done = w.ask(dev, "Revisor", "e o PR?", "--json")
	w.turnEnds(rev, "aprovado")
	r := <-done
	v := decodeJSON(t, r)
	if r.code != 0 || !v.OK || v.Data["status"] != "answered" || v.Data["response"] != "aprovado" || v.Data["to"] != "Revisor" || v.Data["request_id"] == "" {
		t.Fatalf("ask --json = %+v", r)
	}
	r = w.run(dev, "ask", "Revisor", "depois eu vejo", "--no-wait", "--json")
	if v := decodeJSON(t, r); r.code != 0 || v.Data["status"] != "sent" {
		t.Fatalf("ask --no-wait = %+v", r)
	}
	if r := w.run(dev, "ask", "Dev", "oi", "--json"); r.code != 2 {
		t.Errorf("asking oneself = %+v", r)
	}
	if r := w.run(dev, "ask", "Revisor"); r.code != 2 {
		t.Errorf("ask with no prompt = %+v", r)
	}
}

// CA-CLI-COM-02: --batch prints one JSON with a result for each, with no
// --json, and exits 0 when everyone answered.
func TestAskBatchPrintsOneJSONWithAResultForEach(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev, a, b := w.recruit("Dev", "Dev Claude"), w.recruit("A", "Dev Claude"), w.recruit("B", "Dev Claude")
	for _, n := range []string{"A", "B"} {
		if r := w.run(w.master, "connect", n, "Dev"); r.code != 0 {
			t.Fatalf("connect: %+v", r)
		}
	}
	armed := w.clk.Armed()
	done := make(chan result, 1)
	go func() { done <- w.run(dev, "ask", "--batch", `{"A":"x","B":"y"}`) }()
	w.clk.BlockUntil(armed + 2)
	w.turnEnds(a, "resposta de A")
	w.turnEnds(b, "resposta de B")
	r := <-done
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("ask --batch = %+v", r)
	}
	// stdout is a JSON, as it is, with no --json
	var out struct {
		Results map[string]struct{ Status, Response string }
	}
	if err := decodeInto(r.stdout, &out); err != nil || out.Results["A"].Response != "resposta de A" || out.Results["B"].Response != "resposta de B" ||
		out.Results["A"].Status != "answered" || out.Results["B"].Status != "answered" {
		t.Fatalf("stdout = %q (%v)", r.stdout, err)
	}
	// one that does not answer in time makes the exit 6
	armed = w.clk.Armed()
	go func() { done <- w.run(dev, "ask", "--batch", `{"A":"x","B":"y"}`, "--timeout", "2m") }()
	w.clk.BlockUntil(armed + 2)
	w.turnEnds(a, "A de novo")
	w.clk.Advance(2 * time.Minute)
	if r := <-done; r.code != 6 || !strings.Contains(r.stdout, "pending") || !strings.Contains(r.stdout, "A de novo") {
		t.Errorf("a batch with one pending = %+v", r)
	}
	// a name that is not connected refuses the whole batch before any is asked
	if r := w.run(dev, "ask", "--batch", `{"A":"x","Mestre2":"y"}`); r.code == 0 {
		t.Errorf("a batch with a name that does not exist = %+v", r)
	}
}

// CA-CLI-COM-03: the escapes of --raw become the bytes typed, at once.
func TestAskRawTypesTheEscapedBytes(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev, _ := w.pair()
	r := w.run(w.master, "ask", "Dev", "--raw", `1\n\x1b[B`)
	if r.code != 0 {
		t.Fatalf("ask --raw = %+v", r)
	}
	if got := w.mach.typedRaw(dev.ID); string(got) != "\x31\x0a\x1b\x5b\x42" {
		t.Errorf("typed % x, want 31 0a 1b 5b 42", got)
	}
	// a target that works is not interrupted
	w.mach.Report(w.ctx, dev.ID, characters.Busy)
	if r := w.run(w.master, "ask", "Dev", "--raw", `x`, "--json"); r.code != 7 || decodeJSON(t, r).Error.Code != "busy" {
		t.Errorf("--raw to a busy target = %+v", r)
	}
	if got := w.mach.typedRaw(dev.ID); len(got) != 5 {
		t.Errorf("typed % x into a busy target", got)
	}
}

// CA-CLI-COM-04: the name of a scroll is the first line of its content, and it
// starts connected to who created it.
func TestNoteCreateNamesTheScrollByItsFirstLineAndConnectsIt(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev, _ := w.pair()
	r := w.run(dev, "note", "create", "# Plano de testes\nprimeiro o caminho feliz", "--json")
	v := decodeJSON(t, r)
	if r.code != 0 || v.Data["name"] != "Plano de testes" || v.Data["rev"] != float64(1) {
		t.Fatalf("note create = %+v", r)
	}
	l := decodeJSON(t, w.run(dev, "list", "--json"))
	notes, _ := l.Data["notes"].([]any)
	if len(notes) != 1 || notes[0].(map[string]any)["name"] != "Plano de testes" {
		t.Fatalf("notes of list: %v", l.Data["notes"])
	}
	found := false
	for _, e := range l.Data["connections"].([]any) {
		m := e.(map[string]any)
		if (m["from"] == "Plano de testes" && m["to"] == "Dev") || (m["from"] == "Dev" && m["to"] == "Plano de testes") {
			found = true
		}
	}
	if !found {
		t.Errorf("the scroll is not connected to its creator: %v", l.Data["connections"])
	}
	if r := w.run(dev, "note", "create", "outro", "--name", "Plano de testes", "--json"); r.code != 7 || decodeJSON(t, r).Error.Code != "name_taken" {
		t.Errorf("a name in use = %+v", r)
	}
	if r := w.run(dev, "note", "read", "Plano de testes"); r.code != 0 || !strings.Contains(r.stdout, "primeiro o caminho feliz") {
		t.Errorf("note read = %+v", r)
	}
}

// The content of a scroll comes from a file or from the standard input; the
// CLI reads it, the engine never opens a path.
func TestNoteContentCanComeFromAFileOrTheStandardInput(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev, _ := w.pair()
	w.files["/work/plano.md"] = []byte("# Do arquivo\ncorpo")
	if r := w.run(dev, "note", "create", "--file", "plano.md", "--json"); r.code != 0 || decodeJSON(t, r).Data["name"] != "Do arquivo" {
		t.Fatalf("note create --file = %+v", r)
	}
	w.stdin = "# Da entrada\ncorpo"
	if r := w.run(dev, "note", "create", "--file", "-", "--json"); r.code != 0 || decodeJSON(t, r).Data["name"] != "Da entrada" {
		t.Fatalf("note create --file - = %+v", r)
	}
	w.files["/work/novo.md"] = []byte("# Do arquivo\nreescrito")
	if r := w.run(dev, "note", "write", "Do arquivo", "--file", "novo.md", "--json"); r.code != 0 || decodeJSON(t, r).Data["rev"] != float64(2) {
		t.Fatalf("note write --file = %+v", r)
	}
	if r := w.run(dev, "note", "read", "Do arquivo"); !strings.Contains(r.stdout, "reescrito") {
		t.Errorf("note read = %+v", r)
	}
	// both, a file that does not exist, a file over the limit
	if r := w.run(dev, "note", "create", "x", "--file", "plano.md"); r.code != 2 {
		t.Errorf("content and --file = %+v", r)
	}
	if r := w.run(dev, "note", "create", "--file", "nao-existe.md", "--json"); r.code != 4 || decodeJSON(t, r).Error.Code != "not_found" {
		t.Errorf("a file that does not exist = %+v", r)
	}
	w.files["/work/grande.md"] = []byte(strings.Repeat("a", 300<<10))
	if r := w.run(dev, "note", "create", "--file", "grande.md", "--json"); r.code != 8 || decodeJSON(t, r).Error.Code != "too_large" {
		t.Errorf("a file over the limit = %+v", r)
	}
	if r := w.run(dev, "note", "write", "Do arquivo", "--json"); r.code != 2 {
		t.Errorf("write with no content = %+v", r)
	}
}

// CA-CLI-COM-05: a stale revision does not overwrite the scroll.
func TestNoteWriteWithAStaleRevisionIsRevConflict(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev, _ := w.pair()
	w.run(dev, "note", "create", "# Plano\nv1")
	for _, c := range []string{"v2", "v3", "v4"} {
		if r := w.run(dev, "note", "write", "Plano", "# Plano\n"+c, "--json"); r.code != 0 {
			t.Fatal(r)
		}
	}
	r := w.run(dev, "note", "write", "Plano", "# Plano\nsobrescrito", "--expect-rev", "3", "--json")
	if v := decodeJSON(t, r); r.code != 7 || v.Error.Code != "rev_conflict" {
		t.Fatalf("write with a stale revision = %+v", r)
	}
	read := decodeJSON(t, w.run(dev, "note", "read", "Plano", "--json"))
	if read.Data["rev"] != float64(4) || !strings.Contains(read.Data["content"].(string), "v4") {
		t.Errorf("the scroll changed: %v", read.Data)
	}
	if r := w.run(dev, "note", "edit", "Plano", "--old", "v4", "--new", "v5", "--expect-rev", "4", "--json"); r.code != 0 {
		t.Errorf("edit with the right revision = %+v", r)
	}
	if r := w.run(dev, "note", "edit", "Plano", "--old", "naoexiste", "--new", "x", "--json"); r.code != 4 || decodeJSON(t, r).Error.Code != "edit_not_found" {
		t.Errorf("edit of a piece that is not there = %+v", r)
	}
}

// CA-CLI-COM-06: only the Grand Master answers the users.
func TestReplyByACharacterThatIsNotTheGrandMasterIsLevelDenied(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev, _ := w.pair()
	r := w.run(dev, "reply", "oi", "--json")
	if v := decodeJSON(t, r); r.code != 3 || v.Error.Code != "level_denied" {
		t.Fatalf("reply by a character = %+v", r)
	}
	// The Grand Master with no message of a user to answer.
	if r := w.run(w.gm, "reply", "oi", "--json"); r.code != 7 || decodeJSON(t, r).Error.Code != "nothing_to_reply" {
		t.Errorf("reply with nothing to answer = %+v", r)
	}
	// A text that looks like a secret is refused before anything is sent.
	if r := w.run(w.gm, "reply", "a chave é ghp_abcdefghijklmnopqrstuvwxyz0123456789", "--json"); r.code != 3 || decodeJSON(t, r).Error.Code != "secret_detected" {
		t.Errorf("reply with a secret = %+v", r)
	}
	// An attachment that does not exist, or is over the limit, is checked by the CLI.
	if r := w.run(w.gm, "reply", "veja", "--attach", "nada.png", "--json"); r.code != 4 {
		t.Errorf("attachment that does not exist = %+v", r)
	}
	w.files["/work/enorme.bin"] = nil
	w.sizes = map[string]int64{"/work/enorme.bin": 60 << 20}
	if r := w.run(w.gm, "reply", "veja", "--attach", "enorme.bin", "--json"); r.code != 8 {
		t.Errorf("attachment over the limit = %+v", r)
	}
}

// CA-CLI-COM-07: a done with nothing to answer is registered and ignored.
func TestDoneWithNoActiveTaskIsNoActiveTask(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev, rev := w.pair()
	r := w.run(dev, "done", "x", "--json")
	if v := decodeJSON(t, r); r.code != 7 || v.Error.Code != "no_active_task" {
		t.Fatalf("done with no task = %+v", r)
	}
	if dones, _ := w.orc.Registry.Dones(w.ctx, dev.ID); len(dones) != 1 || dones[0].Text != "x" {
		t.Errorf("the done was not registered: %+v", dones)
	}
	// What is an active task: a message of another agent that is under way
	// (here an ask with --no-wait); its done answers it, once.
	if r := w.run(dev, "ask", "Revisor", "revise isto", "--no-wait"); r.code != 0 {
		t.Fatal(r)
	}
	if got := w.get(rev); !got.TaskOpen {
		t.Fatal("the message of another agent is not a task of the target")
	}
	r = w.run(rev, "done", "revisado", "--json")
	v := decodeJSON(t, r)
	if r.code != 0 || !v.OK || v.Data["queued"] != false || v.Data["returned_to"] != nil {
		t.Fatalf("done of a task = %+v", r)
	}
	if got := w.get(rev); got.TaskOpen {
		t.Error("the task is still open after its done")
	}
	if r := w.run(rev, "done", "de novo", "--json"); r.code != 7 {
		t.Errorf("the second done = %+v", r)
	}
	w.turnEnds(rev, "")
	// An ask answered by the last message of the turn, with no done, ends the task.
	done := w.ask(dev, "Revisor", "outra pergunta")
	if got := w.get(rev); !got.TaskOpen {
		t.Fatal("the ask is not a task")
	}
	w.turnEnds(rev, "resposta sem done")
	if r := <-done; r.code != 0 || !strings.Contains(r.stdout, "resposta sem done") {
		t.Errorf("ask = %+v", r)
	}
	if got := w.get(rev); got.TaskOpen {
		t.Error("an ask answered by the last message left the task open")
	}
	// The message of a user and the wake-up of a wait are no task.
	if got := w.get(dev); got.TaskOpen {
		t.Error("the asker has a task")
	}
}

// CA-CLI-COM-08: wait registers and returns at once; the caller shows as
// waiting for who it waits.
func TestWaitReturnsAtOnceWithTheIdOfTheWait(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev, rev := w.pair()
	start := w.clk.Now()
	r := w.run(dev, "wait", "Revisor", "--timeout", "10m", "--json")
	v := decodeJSON(t, r)
	// It returned with nobody moving the clock: it did not wait for anything.
	if r.code != 0 || v.Data["wait_id"] == "" || v.Data["kind"] == nil || v.Data["timeout_at"] == nil || !w.clk.Now().Equal(start) {
		t.Fatalf("wait = %+v", r)
	}
	if targets, _ := v.Data["targets"].([]any); len(targets) != 1 || targets[0] != "Revisor" {
		t.Errorf("targets = %v", v.Data["targets"])
	}
	// The turn of the caller ends and the runtime puts it waiting.
	reason, ok, err := w.orc.Waits.Active(w.ctx, dev.ID)
	if err != nil || !ok {
		t.Fatalf("Active = %q %v %v", reason, ok, err)
	}
	w.mach.Report(w.ctx, dev.ID, characters.Busy)
	if _, _, err := w.orc.Registry.Transition(w.ctx, dev.ID, characters.Waiting, characters.Change{Reason: reason, At: w.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	l := decodeJSON(t, w.run(rev, "list", "--json"))
	var row map[string]any
	for _, a := range l.Data["agents"].([]any) {
		if a.(map[string]any)["name"] == "Dev" {
			row = a.(map[string]any)
		}
	}
	if row == nil || row["status"] != "aguardando Revisor" || row["waiting_for"] != "Revisor" {
		t.Fatalf("Dev in the list of the Reviewer: %v", row)
	}
	if text := w.run(rev, "list"); !strings.Contains(text.stdout, "aguardando Revisor") {
		t.Errorf("list: %s", text.stdout)
	}
	// The map of waits: one line each, the table of the caller.
	m := w.run(rev, "waits")
	if m.code != 0 || !strings.Contains(m.stdout, "Dev -> Revisor") {
		t.Errorf("waits = %+v", m)
	}
	mj := decodeJSON(t, w.run(rev, "waits", "--json"))
	if ws, _ := mj.Data["waits"].([]any); len(ws) != 1 || mj.Data["cycles"] == nil {
		t.Errorf("waits --json = %v", mj.Data)
	}
	if r := w.run(dev, "wait", "Fantasma", "--json"); r.code != 4 {
		t.Errorf("wait for a name that does not exist = %+v", r)
	}
	if r := w.run(dev, "waits", "--all", "--json"); r.code != 3 {
		t.Errorf("waits --all by a character = %+v", r)
	}
	if r := w.run(dev, "wait", "--until", "11:00", "--json"); r.code != 2 {
		t.Errorf("wait until a time in the past = %+v", r)
	}
}

// A target that is not connected is refused before anything happens.
func TestTheGuardRefusesATargetThatIsNotConnected(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev, _ := w.pair()
	stranger := w.recruit("Estranho", "Dev Claude")
	_ = stranger
	// Dev is connected to the master, to the Reviewer and to nobody else.
	for _, args := range [][]string{
		{"ask", "Estranho", "oi"}, {"check", "Estranho"}, {"wait", "Estranho"}, {"wait", "Revisor", "Estranho"},
		{"ask", "--batch", `{"Estranho":"oi"}`}, {"ask", "Estranho", "--raw", "x"},
	} {
		r := w.run(dev, append(args, "--json")...)
		if v := decodeJSON(t, r); r.code != 3 || v.Error.Code != "not_connected" {
			t.Errorf("%v = %d %s", args, r.code, r.stdout)
		}
	}
	if got := w.mach.Delivered(w.get(stranger)); len(got) != 0 {
		t.Errorf("the stranger got %q", got)
	}
}

// check reads the screen and sends nothing.
func TestCheckShowsTheScreenOfTheTarget(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev, _ := w.pair()
	w.mach.screen[dev.ID] = "$ go test\nok"
	r := w.run(w.master, "check", "Dev")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "$ go test\nok" {
		t.Fatalf("check = %+v", r)
	}
	v := decodeJSON(t, w.run(w.master, "check", "Dev", "--json"))
	if v.Data["name"] != "Dev" || v.Data["screen"] != "$ go test\nok" || v.Data["status"] != "idle" || v.Data["captured_at"] == nil {
		t.Errorf("check --json = %v", v.Data)
	}
	if r := w.run(w.master, "check", "Dev", "--lines", "abc"); r.code != 2 {
		t.Errorf("check with --lines abc = %+v", r)
	}
	if got := w.mach.Delivered(dev); len(got) != 0 {
		t.Errorf("check sent %q", got)
	}
}

// Part of CA-CLI-COM-09 (the attachments of a real channel and their copy are
// milestone 5, here a double): a message of the inbox is passed on whole, with
// the path of the copy of its attachment in the table of the target.
func TestAskFromMessagePassesTheMessageOnWithItsAttachments(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	text := "Corrija o login, por favor!\n\nO print do erro está anexo."
	it, err := w.orc.Inbox.Enqueue(w.ctx, inbox.NewItem{Workspace: w.slug, Target: w.gm.ID, Kind: inbox.KindUser, Sender: "Lucas", Text: text,
		Attachments: []string{".mestre/anexos/2026/10/print.png"}, Origin: &inbox.Origin{Channel: "telegram", User: "u1", UserName: "Lucas", Level: characters.LevelOwner}})
	if err != nil {
		t.Fatal(err)
	}
	r := w.run(w.gm, "ask", "Mestre", "--table", w.table.ID, "--from-message", it.ID, "--no-wait", "--json")
	if v := decodeJSON(t, r); r.code != 0 || v.Data["status"] != "sent" {
		t.Fatalf("ask --from-message = %+v", r)
	}
	got := w.mach.Delivered(w.get(w.master))
	if len(got) == 0 {
		t.Fatal("the table master got nothing")
	}
	last := got[len(got)-1]
	if !strings.Contains(last, text) || !strings.Contains(last, ".mestre/anexos/print.png") {
		t.Errorf("the table master got:\n%s", last)
	}
	if w.atts.table != w.table.ID {
		t.Errorf("the attachments were copied to %q, not to the table of the target", w.atts.table)
	}
	// With a prompt, it goes before the message, as a note.
	r = w.run(w.gm, "ask", "Mestre", "veja isto antes", "--table", w.table.ID, "--from-message", it.ID, "--no-wait")
	if r.code != 0 {
		t.Fatal(r)
	}
	w.turnEnds(w.master, "")
	got = w.mach.Delivered(w.get(w.master))
	if last := got[len(got)-1]; !strings.HasPrefix(last, "veja isto antes") || !strings.Contains(last, text) {
		t.Errorf("with a note:\n%s", last)
	}
	// A message that is not in the inbox of the caller.
	if r := w.run(w.gm, "ask", "Mestre", "--table", w.table.ID, "--from-message", "m999", "--json"); r.code != 4 {
		t.Errorf("a message that is not there = %+v", r)
	}
}

// CA-CLI-COM-11: the Grand Master with two open tables names the table.
func TestAskToMestreWithTwoTablesNeedsTheTable(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	second, master2 := w.openTable(43)
	r := w.run(w.gm, "ask", "Mestre", "status?", "--json")
	if v := decodeJSON(t, r); r.code != 2 || v.Error.Code != "table_required" {
		t.Fatalf("ask Mestre with no --table = %+v", r)
	}
	if r := w.run(w.gm, "ask", "Mestre", "status?", "--table", second.ID, "--no-wait"); r.code != 0 {
		t.Errorf("ask Mestre with --table = %+v", r)
	}
	if got := w.mach.Delivered(w.get(master2)); len(got) == 0 || !strings.Contains(got[len(got)-1], "status?") {
		t.Errorf("the master of the second table got %q", got)
	}
	if r := w.run(w.gm, "check", "Mestre", "--json"); r.code != 2 {
		t.Errorf("check Mestre with no --table = %+v", r)
	}
}

// CA-CLI-COM-13: the Grand Master has no scrolls.
func TestTheGrandMasterCannotCreateANote(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	r := w.run(w.gm, "note", "create", "x", "--json")
	if v := decodeJSON(t, r); r.code != 3 || v.Error.Code != "level_denied" {
		t.Fatalf("note create by the Grand Master = %+v", r)
	}
	l := decodeJSON(t, w.run(w.master, "list", "--json"))
	if notes, _ := l.Data["notes"].([]any); len(notes) != 0 {
		t.Errorf("a scroll was made: %v", notes)
	}
}

// inbox shows the queue of the caller, in the order of delivery.
func TestInboxShowsTheQueueInTheOrderOfDelivery(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev, rev := w.pair()
	w.mach.Report(w.ctx, rev.ID, characters.Busy)
	w.run(dev, "ask", "Revisor", "primeira", "--no-wait")
	w.run(dev, "ask", "Revisor", "segunda "+strings.Repeat("x", 100), "--no-wait")
	v := decodeJSON(t, w.run(rev, "inbox", "--json"))
	items, _ := v.Data["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("inbox = %v", v.Data)
	}
	first := items[0].(map[string]any)
	if first["from"] != "Dev" || first["kind"] != "agent" || !strings.Contains(first["text"].(string), "primeira") || first["id"] == "" || first["received_at"] == nil {
		t.Errorf("first = %v", first)
	}
	short := w.run(rev, "inbox")
	full := w.run(rev, "inbox", "--full")
	if strings.Count(short.stdout, "x") >= 100 || strings.Count(full.stdout, "x") < 100 {
		t.Errorf("--full: short has %d, full has %d", strings.Count(short.stdout, "x"), strings.Count(full.stdout, "x"))
	}
}
