package github_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/github"
)

type batch struct {
	Table  string
	Events []github.Event
}

type sinkFake struct {
	mu      sync.Mutex
	batches []batch
	fail    error
}

func (s *sinkFake) Deliver(_ context.Context, _ github.Watch, table string, evs []github.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	s.batches = append(s.batches, batch{table, append([]github.Event(nil), evs...)})
	return nil
}

func (s *sinkFake) events() []github.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var all []github.Event
	for _, b := range s.batches {
		all = append(all, b.Events...)
	}
	return all
}

type situationFake struct {
	mu     sync.Mutex
	mode   github.Mode
	tables map[int]string
	prs    []github.TablePR
}

func (s *situationFake) TablePRs(context.Context, github.Watch) []github.TablePR {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]github.TablePR(nil), s.prs...)
}

// mirrorFake is the mirror: the commits that can be reached from the base.
type mirrorFake struct {
	mu      sync.Mutex
	reach   map[string]bool
	asked   int
	marked  []string
	failing error
	markErr error
}

func (m *mirrorFake) Reachable(_ context.Context, _ github.Watch, sha string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.asked++
	if sha == "" {
		return false, errors.New("no commit to look for")
	}
	return m.reach[sha], m.failing
}

func (m *mirrorFake) Merged(_ context.Context, _ github.Watch, table string, n int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.markErr != nil {
		return m.markErr
	}
	m.marked = append(m.marked, fmt.Sprintf("%s#%d", table, n))
	return nil
}

func (m *mirrorFake) get() (asked int, marked []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.asked, append([]string(nil), m.marked...)
}

func (s *situationFake) Mode(context.Context, github.Watch) github.Mode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

func (s *situationFake) TableOf(_ context.Context, _ string, n int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tables[n]
}

func (s *situationFake) set(m github.Mode) { s.mu.Lock(); s.mode = m; s.mu.Unlock() }

type pollRig struct {
	*world
	p     *github.Poller
	sink  *sinkFake
	sit   *situationFake
	watch github.Watch
	rand  float64
	mir   *mirrorFake
}

const pollRepo = "lucas/meu-app"

func newPoll(t *testing.T) *pollRig {
	t.Helper()
	w := newWorld(t)
	w.gh.AddRepo("lucas", "meu-app")
	w.gh.IssueToken("tok", "grao-mestre")
	r := &pollRig{world: w, mir: &mirrorFake{reach: map[string]bool{}}, sink: &sinkFake{}, sit: &situationFake{mode: github.ModeAttentive, tables: map[int]string{}}, rand: 0.5}
	r.watch = github.Watch{Workspace: "meu-app", Repo: pollRepo, Since: w.clk.Now()}
	r.p = r.poller(w)
	return r
}

func (r *pollRig) poller(w *world) *github.Poller {
	p, err := github.NewPoller(github.PollerOptions{
		DB: w.db, Clock: w.clk, API: w.srv.URL, HTTP: w.srv.Client(),
		Token:   func(context.Context, string) (string, error) { return "tok", nil },
		Objects: &github.Objects{DB: w.db, Clock: w.clk}, Sink: r.sink, Situation: r.sit, Merges: r.mir,
		Watches: func(context.Context) ([]github.Watch, error) { return []github.Watch{r.watch}, nil },
		Rand:    func() float64 { return r.rand },
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return p
}

func (r *pollRig) cycle() bool {
	r.t.Helper()
	changed, err := r.p.Cycle(r.ctx, r.watch)
	if err != nil {
		r.t.Fatal(err)
	}
	return changed
}

// CA-GH-EV-01
func TestAnETagKeptMakesTheNextReadingConditionalAndA304MakesNoEvent(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	r.clk.Advance(10 * time.Second)
	n := r.gh.AddIssue(pollRepo, "bug")
	r.gh.AddIssueComment(pollRepo, n, "pessoa", "olha isso")
	if !r.cycle() {
		t.Fatal("the first cycle saw nothing")
	}
	evs := r.sink.events()
	var comments int
	for _, e := range evs {
		if e.Kind == github.EventComment {
			comments++
			if e.Number != n || e.Author != "pessoa" || e.Summary != "olha isso" {
				t.Errorf("comment event: %+v", e)
			}
		}
	}
	if comments != 1 {
		t.Fatalf("%d comment events in %+v", comments, evs)
	}
	before := len(evs)
	reads := len(r.gh.Reads())
	if r.cycle() {
		t.Error("an unchanged repository gave events")
	}
	second := r.gh.Reads()[reads:]
	if len(second) != 3 {
		t.Fatalf("the second cycle made %d requests, want the 3 lists", len(second))
	}
	for _, rq := range second {
		if !rq.IfNoneMatch || rq.Status != http.StatusNotModified {
			t.Errorf("%s: conditional %v, status %d", rq.Path, rq.IfNoneMatch, rq.Status)
		}
		if !strings.Contains(rq.Path, "sort=updated&direction=desc&per_page=100") {
			t.Errorf("the address changed: %s", rq.Path)
		}
	}
	if len(r.sink.events()) != before {
		t.Error("a 304 made an event")
	}
}

// CA-GH-EV-02
func TestTheIntervalFollowsTheActivityAndChangesBringTheThirtySeconds(t *testing.T) {
	t.Parallel()
	r := newPoll(t) // a table is open: attentive
	if got := r.p.Interval(r.ctx, r.watch); got != github.IntervalAttentive {
		t.Fatalf("with a table open and nothing new: %v", got)
	}
	r.sit.set(github.ModeIdle)
	if got := r.p.Interval(r.ctx, r.watch); got != github.IntervalIdle {
		t.Fatalf("with no table: %v", got)
	}
	r.sit.set(github.ModeAttentive)
	r.clk.Advance(10 * time.Second)
	n := r.gh.AddIssue(pollRepo, "bug")
	r.gh.AddIssueComment(pollRepo, n, "pessoa", "oi")
	r.cycle()
	if got := r.p.Interval(r.ctx, r.watch); got != github.IntervalActive {
		t.Fatalf("after a change: %v, want 30 s", got)
	}
	r.clk.Advance(github.ActiveHold - time.Second)
	if got := r.p.Interval(r.ctx, r.watch); got != github.IntervalActive {
		t.Fatalf("4m59s after the change: %v", got)
	}
	r.clk.Advance(2 * time.Second)
	if got := r.p.Interval(r.ctx, r.watch); got != github.IntervalAttentive {
		t.Fatalf("5 minutes after the change: %v, want 2 min", got)
	}
	r.gh.AddIssueComment(pollRepo, n, "pessoa", "de novo")
	r.cycle()
	if got := r.p.Interval(r.ctx, r.watch); got != github.IntervalActive {
		t.Fatalf("a new change: %v, want 30 s again", got)
	}
	// an action of the engine at GitHub keeps it active too
	r.clk.Advance(10 * time.Minute)
	r.p.Acted(r.watch.Workspace)
	if got := r.p.Interval(r.ctx, r.watch); got != github.IntervalActive {
		t.Fatalf("after an action of the engine: %v", got)
	}
}

func TestEachIntervalVariesTenPercentAtMost(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	for rnd, want := range map[float64]time.Duration{0: 108 * time.Second, 0.5: 120 * time.Second, 0.999999: 132 * time.Second} {
		r.rand = rnd
		got := r.p.Interval(r.ctx, r.watch)
		if diff := got - want; diff < -time.Second || diff > time.Second {
			t.Errorf("random %v: %v, want about %v", rnd, got, want)
		}
	}
	r.rand = 0
	if got := r.p.Interval(r.ctx, r.watch); got < time.Duration(0.9*float64(github.IntervalAttentive)) {
		t.Errorf("below the jitter: %v", got)
	}
}

// CA-GH-EV-03
func TestAChangeAlreadyProcessedIsNotAnEventAgainAfterARestart(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	r.clk.Advance(10 * time.Second)
	n := r.gh.AddIssue(pollRepo, "bug")
	r.gh.AddIssueComment(pollRepo, n, "pessoa", "oi")
	r.cycle()
	first := len(r.sink.events())
	if first == 0 {
		t.Fatal("no event the first time")
	}
	// the Citadel restarts: a new poller, and the first answer is a 200 with the same comment
	restarted := r.poller(r.world)
	if _, err := r.db.Writer().ExecContext(r.ctx, `UPDATE github_poll_state SET etag = ''`); err != nil {
		t.Fatal(err)
	}
	changed, err := restarted.Cycle(r.ctx, r.watch)
	if err != nil {
		t.Fatal(err)
	}
	if changed || len(r.sink.events()) != first {
		t.Fatalf("the same comment became %d new events", len(r.sink.events())-first)
	}
	// and an edit of the comment is a new fact
	r.clk.Advance(5 * time.Second)
	r.gh.EditIssueComment(pollRepo, 1, "") // no such comment: nothing changes
	id := r.gh.AddIssueComment(pollRepo, n, "pessoa", "segundo")
	r.clk.Advance(5 * time.Second)
	r.gh.EditIssueComment(pollRepo, id, "segundo, editado")
	if !r.cycle() {
		t.Fatal("an edited comment is a change")
	}
	last := r.sink.events()[len(r.sink.events())-1]
	if last.Kind != github.EventComment || last.Summary != "segundo, editado" {
		t.Errorf("last event: %+v", last)
	}
}

// CA-GH-EV-04
func TestWhatTheGuildWroteItselfIsNoEvent(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	r.clk.Advance(10 * time.Second)
	n := r.gh.AddIssue(pollRepo, "bug")
	// a comment the engine made: registered, with its marker
	op := github.NewOp()
	id := r.gh.AddIssueComment(pollRepo, n, "mestre[bot]", github.WithMarker("feito pelo motor", op))
	// and one the engine made whose marker was lost: it is in the records
	id2 := r.gh.AddIssueComment(pollRepo, n, "mestre[bot]", "sem marcador")
	objs := &github.Objects{DB: r.db, Clock: r.clk}
	if err := objs.Register(r.ctx, github.Object{Repo: pollRepo, Kind: github.KindComment, Number: int(id2), GitHubID: id2, ParentNumber: n}); err != nil {
		t.Fatal(err)
	}
	_ = id
	for _, e := range r.sink.events() {
		t.Fatalf("event before the cycle: %+v", e)
	}
	r.cycle()
	for _, e := range r.sink.events() {
		if e.Kind == github.EventComment {
			t.Errorf("the engine's own comment became an event: %+v", e)
		}
	}
	// a person after them is an event
	r.gh.AddIssueComment(pollRepo, n, "pessoa", "obrigado")
	r.cycle()
	var got int
	for _, e := range r.sink.events() {
		if e.Kind == github.EventComment {
			got++
		}
	}
	if got != 1 {
		t.Errorf("%d comment events, want only the person's", got)
	}
}

// CA-GH-EV-06
func TestALimitOfGitHubStopsTheReadingsOfTheWorkspaceUntilTheTimeItSaid(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	if err := r.p.Tick(r.ctx); err != nil { // the first reading happens at once
		t.Fatal(err)
	}
	reads := len(r.gh.Reads())
	r.clk.Advance(github.IntervalAttentive)
	r.gh.FailNextRead(http.StatusTooManyRequests, map[string]string{"Retry-After": "60"})
	if err := r.p.Tick(r.ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.gh.Reads()) != reads {
		t.Fatalf("a limited request counted as a reading")
	}
	asked := func() int { return len(r.gh.Reads()) }
	r.clk.Advance(59 * time.Second)
	_ = r.p.Tick(r.ctx)
	if asked() != reads {
		t.Fatalf("GitHub was asked again after 59 s of a retry-after of 60")
	}
	r.clk.Advance(time.Second)
	_ = r.p.Tick(r.ctx)
	if asked() == reads {
		t.Fatal("nothing was asked after the 60 s")
	}
}

func TestOnlyWhatChangedAfterTheWorkspaceWasMadeBecomesAnEventAndAllPagesAreRead(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	r.gh.AddIssue(pollRepo, "antiga") // the history of the repository, before the workspace
	r.clk.Advance(10 * time.Second)
	r.watch.Since = r.clk.Now() // the workspace is made now: the cursor starts here
	r.clk.Advance(time.Second)
	for i := range 120 {
		r.gh.AddIssue(pollRepo, fmt.Sprintf("issue %d", i))
		r.clk.Advance(time.Second)
	}
	r.cycle()
	seen := map[int]bool{}
	for _, e := range r.sink.events() {
		seen[e.Number] = true
	}
	if seen[1] {
		t.Error("the history from before the workspace became an event")
	}
	for n := 2; n <= 121; n++ {
		if !seen[n] {
			t.Fatalf("the issue #%d was not read: the second page is missing", n)
		}
	}
}

func TestTheCursorMovesOnlyAfterTheEventsWereDelivered(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	r.clk.Advance(10 * time.Second)
	n := r.gh.AddIssue(pollRepo, "bug")
	r.gh.AddIssueComment(pollRepo, n, "pessoa", "oi")
	r.sink.fail = errors.New("the inbox is down")
	if _, err := r.p.Cycle(r.ctx, r.watch); err == nil {
		t.Fatal("a failed delivery was a success")
	}
	r.sink.fail = nil
	if !r.cycle() {
		t.Fatal("the change was lost with the failed delivery")
	}
	var comments int
	for _, e := range r.sink.events() {
		if e.Kind == github.EventComment {
			comments++
		}
	}
	if comments != 1 {
		t.Errorf("%d comment events after the retry", comments)
	}
}

func TestTheEventsOfACycleGoInOneBatchForEachTable(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	r.sit.tables = map[int]string{1: "mesa-a", 2: "mesa-a", 3: ""}
	r.clk.Advance(10 * time.Second)
	for range 3 {
		r.gh.AddIssue(pollRepo, "x")
	}
	r.gh.AddIssueComment(pollRepo, 1, "pessoa", "a")
	r.cycle()
	r.sink.mu.Lock()
	defer r.sink.mu.Unlock()
	perTable := map[string]int{}
	for _, b := range r.sink.batches {
		perTable[b.Table]++
	}
	if perTable["mesa-a"] != 1 || perTable[""] != 1 || len(r.sink.batches) != 2 {
		t.Errorf("batches by table: %v", perTable)
	}
}

func TestAPersonQuotingAMarkerInTheMiddleOfAReplyIsStillAnEvent(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	r.clk.Advance(10 * time.Second)
	n := r.gh.AddIssue(pollRepo, "bug")
	r.gh.AddIssueComment(pollRepo, n, "pessoa", "> "+github.Marker("abc")+"\nconcordo com o motor")
	r.cycle()
	if len(r.sink.events()) == 0 {
		t.Fatal("a reply that quotes a marker was taken for the engine's")
	}
}

func TestAWorkspaceWithoutAMomentToStartFromDoesNotTurnTheHistoryIntoEvents(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	for range 3 {
		n := r.gh.AddIssue(pollRepo, "antiga")
		r.gh.AddIssueComment(pollRepo, n, "pessoa", "velho")
	}
	r.clk.Advance(time.Minute)
	r.watch.Since = time.Time{}
	r.cycle()
	if got := r.sink.events(); len(got) != 0 {
		t.Fatalf("%d events from the history", len(got))
	}
}

func TestAHundredPagesOfChangesInOneCycleAreAllRead(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	r.clk.Advance(time.Second)
	for i := range 2100 {
		r.gh.AddIssue(pollRepo, fmt.Sprintf("i%d", i))
	}
	seen := map[int]bool{}
	for range 4 {
		r.cycle()
	}
	for _, e := range r.sink.events() {
		seen[e.Number] = true
	}
	if len(seen) != 2100 {
		t.Fatalf("%d of 2100 issues became events", len(seen))
	}
}

func TestRetryAfterIsKeptBetweenOneSecondAndOneHour(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		hdr  map[string]string
		want time.Duration
	}{
		{map[string]string{"Retry-After": "0"}, time.Second},
		{map[string]string{"Retry-After": "99999999"}, time.Hour},
		{map[string]string{"x-ratelimit-remaining": "0", "x-ratelimit-reset": "1"}, time.Second},
	} {
		r := newPoll(t)
		r.gh.FailNextRead(http.StatusForbidden, tc.hdr)
		start := r.clk.Now()
		if err := r.p.Tick(r.ctx); err != nil {
			t.Fatal(err)
		}
		reads := len(r.gh.Reads())
		r.gh.IssueToken("tok", "grao-mestre") // the token of the double lives one hour
		r.clk.Advance(tc.want - time.Millisecond)
		_ = r.p.Tick(r.ctx)
		if len(r.gh.Reads()) != reads {
			t.Errorf("%v: asked before %v (from %v)", tc.hdr, tc.want, start)
		}
		r.clk.Advance(time.Millisecond)
		r.gh.IssueToken("tok", "grao-mestre")
		_ = r.p.Tick(r.ctx)
		if len(r.gh.Reads()) == reads {
			t.Errorf("%v: not asked after %v", tc.hdr, tc.want)
		}
	}
}

func TestAWorkspaceIsNeverReadTwiceAtTheSameTime(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	block := make(chan struct{})
	entered := make(chan struct{}, 1)
	slow := &slowSink{inner: r.sink, block: block, entered: entered}
	p, err := github.NewPoller(github.PollerOptions{
		DB: r.db, Clock: r.clk, API: r.srv.URL, HTTP: r.srv.Client(),
		Token: func(context.Context, string) (string, error) { return "tok", nil }, Sink: slow, Situation: r.sit,
		Watches: func(context.Context) ([]github.Watch, error) { return []github.Watch{r.watch}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	r.clk.Advance(10 * time.Second)
	n := r.gh.AddIssue(pollRepo, "bug")
	r.gh.AddIssueComment(pollRepo, n, "pessoa", "oi")
	done := make(chan struct{})
	go func() { _ = p.Tick(r.ctx); close(done) }()
	<-entered
	_ = p.Tick(r.ctx) // the first is still delivering: this one does nothing
	if slow.calls() != 1 {
		t.Errorf("%d deliveries while one was in flight", slow.calls())
	}
	close(block)
	<-done
}

type slowSink struct {
	inner          *sinkFake
	block, entered chan struct{}
	mu             sync.Mutex
	n              int
}

func (s *slowSink) Deliver(ctx context.Context, w github.Watch, table string, evs []github.Event) error {
	s.mu.Lock()
	s.n++
	first := s.n == 1
	s.mu.Unlock()
	if first {
		s.entered <- struct{}{}
		<-s.block
	}
	return s.inner.Deliver(ctx, w, table, evs)
}

func (s *slowSink) calls() int { s.mu.Lock(); defer s.mu.Unlock(); return s.n }

func (r *pollRig) tablePR(table, head string) (n int, sha string) {
	r.t.Helper()
	n = r.gh.AddPR(pollRepo, "feat", head, pollRepo)
	sha = fmt.Sprintf("%040d", n)
	r.gh.SetPull(pollRepo, n, sha, false, "clean")
	r.sit.mu.Lock()
	r.sit.tables[n] = table
	r.sit.prs = append(r.sit.prs, github.TablePR{Number: n, Table: table})
	r.sit.mu.Unlock()
	return n, sha
}

// the table the batch is of, with the kinds in it
func (r *pollRig) kindsOf(table string) []github.EventKind {
	r.sink.mu.Lock()
	defer r.sink.mu.Unlock()
	var out []github.EventKind
	for _, b := range r.sink.batches {
		if b.Table == table {
			for _, e := range b.Events {
				out = append(out, e.Kind)
			}
		}
	}
	return out
}

// Part of CA-GH-EV-05 (the item of the inbox of the Grand Master is the test of the inbox package)
func TestAReviewAndTheEndOfTheCIOfATablePRGoInOneBatchOfTheTable(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	r.clk.Advance(10 * time.Second)
	n, sha := r.tablePR("mesa-a", "feat/a")
	r.gh.AddReview(pollRepo, n, "pessoa", "APPROVED")
	r.gh.AddCheck(pollRepo, sha, "ci", "success")
	r.cycle()
	r.sink.mu.Lock()
	batches := len(r.sink.batches)
	r.sink.mu.Unlock()
	kinds := r.kindsOf("mesa-a")
	has := func(k github.EventKind) bool {
		for _, x := range kinds {
			if x == k {
				return true
			}
		}
		return false
	}
	if !has(github.EventReview) || !has(github.EventCI) {
		t.Fatalf("kinds of the table: %v", kinds)
	}
	for _, e := range r.sink.events() {
		if e.Table != "mesa-a" && e.Kind != github.EventPRChanged {
			t.Errorf("event of another table: %+v", e)
		}
	}
	_ = batches
	// the same review and check are not events again
	before := len(r.sink.events())
	r.cycle()
	if len(r.sink.events()) != before {
		t.Error("the review or the check came again")
	}
}

func TestACIThatIsRunningKeepsTheWorkspaceActiveUntilItEnds(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	r.clk.Advance(10 * time.Second)
	n, sha := r.tablePR("mesa-a", "feat/a")
	_ = n
	r.gh.AddCheck(pollRepo, sha, "ci", "") // running
	r.cycle()
	r.clk.Advance(github.ActiveHold + time.Minute) // nothing changes for longer than the hold
	if got := r.p.Interval(r.ctx, r.watch); got != github.IntervalActive {
		t.Fatalf("with the CI running: %v, want 30 s", got)
	}
	for _, k := range r.kindsOf("mesa-a") {
		if k == github.EventCI {
			t.Fatal("a check that is still running became an event")
		}
	}
	r.gh.FinishCheck(pollRepo, sha, "ci", "failure")
	r.cycle()
	var got github.Event
	for _, e := range r.sink.events() {
		if e.Kind == github.EventCI {
			got = e
		}
	}
	if got.Table != "mesa-a" || !strings.Contains(got.Summary, "failure") || !strings.Contains(got.Link, "/runs/") {
		t.Fatalf("the end of the CI: %+v", got)
	}
	r.clk.Advance(github.ActiveHold + time.Minute)
	if d := r.p.Interval(r.ctx, r.watch); d != github.IntervalAttentive {
		t.Errorf("after the CI ended and nothing else: %v, want 2 min", d)
	}
	// the commit whose checks ended is not asked about again
	reads := len(r.gh.Reads())
	r.cycle()
	for _, rq := range r.gh.Reads()[reads:] {
		if strings.Contains(rq.Path, "/check-runs") || strings.Contains(rq.Path, "/status") {
			t.Errorf("asked again about the commit that ended: %s", rq.Path)
		}
	}
}

// CA-GH-EV-08
func TestAMergeOnlyCountsWhenTheCommitIsInTheMirror(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	r.clk.Advance(10 * time.Second)
	lost, _ := r.tablePR("mesa-a", "feat/a")
	ok, _ := r.tablePR("mesa-b", "feat/b")
	r.gh.SetPullMerge(pollRepo, lost, "aaaa")
	r.gh.SetPullMerge(pollRepo, ok, "bbbb")
	r.gh.SetPullState(pollRepo, lost, "closed", true, "")
	r.gh.SetPullState(pollRepo, ok, "closed", true, "")
	r.sit.mu.Lock()
	r.sit.prs = nil
	r.sit.mu.Unlock()
	r.mir.reach["bbbb"] = true
	r.cycle()
	_, marked := r.mir.get()
	if len(marked) != 1 || marked[0] != fmt.Sprintf("mesa-b#%d", ok) {
		t.Fatalf("tables marked merged: %v", marked)
	}
	a := r.kindsOf("mesa-a")
	if len(a) != 1 || a[0] != github.EventWarning {
		t.Fatalf("the table whose commit is not in the mirror got %v, want only the warning", a)
	}
	if b := r.kindsOf("mesa-b"); len(b) != 1 || b[0] != github.EventPRMerged {
		t.Fatalf("the table whose commit is in the mirror got %v", b)
	}
}

// CA-GH-EV-11
func TestAMergeNotInTheMirrorYetIsMarkedWhenItArrivesWithoutANewWarningOrAlertedAfterAnHour(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	r.clk.Advance(10 * time.Second)
	late, _ := r.tablePR("mesa-a", "feat/a")
	never, _ := r.tablePR("mesa-b", "feat/b")
	r.gh.SetPullMerge(pollRepo, late, "aaaa")
	r.gh.SetPullMerge(pollRepo, never, "bbbb")
	r.gh.SetPullState(pollRepo, late, "closed", true, "")
	r.gh.SetPullState(pollRepo, never, "closed", true, "")
	r.sit.mu.Lock()
	r.sit.prs = nil
	r.sit.mu.Unlock()
	r.cycle()
	if _, marked := r.mir.get(); len(marked) != 0 {
		t.Fatalf("marked before the commits arrived: %v", marked)
	}
	r.clk.Advance(10 * time.Minute) // the mirror fetches and the commit of the first arrives
	r.mir.mu.Lock()
	r.mir.reach["aaaa"] = true
	r.mir.mu.Unlock()
	r.cycle()
	if _, marked := r.mir.get(); len(marked) != 1 || marked[0] != fmt.Sprintf("mesa-a#%d", late) {
		t.Fatalf("marked: %v", marked)
	}
	warnings := 0
	for _, e := range r.sink.events() {
		if e.Kind == github.EventWarning {
			warnings++
		}
	}
	if warnings != 2 { // one for each table, at the time of the merge, and no more
		t.Fatalf("%d warnings", warnings)
	}
	r.clk.Advance(github.MergeWait) // an hour and it never came
	r.gh.IssueToken("tok", "grao-mestre")
	r.cycle()
	var alerts []github.Event
	for _, e := range r.sink.events() {
		if e.Kind == github.EventAlert {
			alerts = append(alerts, e)
		}
	}
	if len(alerts) != 1 || alerts[0].Table != "mesa-b" {
		t.Fatalf("alerts: %+v", alerts)
	}
	asked, _ := r.mir.get()
	r.clk.Advance(time.Minute)
	r.cycle()
	if after, _ := r.mir.get(); after != asked {
		t.Error("the commit was checked again after the alert")
	}
	if _, marked := r.mir.get(); len(marked) != 1 {
		t.Errorf("marked: %v", marked)
	}
}

func TestTheReviewsFromBeforeTheWorkspaceAndTheOnesNotSubmittedAreNoEvents(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	n, _ := r.tablePR("mesa-a", "feat/a")
	r.gh.AddReview(pollRepo, n, "pessoa", "APPROVED") // from before the workspace
	r.clk.Advance(10 * time.Second)
	r.watch.Since = r.clk.Now()
	r.clk.Advance(time.Second)
	r.gh.AddReview(pollRepo, n, "pessoa", "PENDING") // not submitted yet
	r.cycle()
	for _, e := range r.sink.events() {
		if e.Kind == github.EventReview {
			t.Errorf("review event: %+v", e)
		}
	}
	r.clk.Advance(time.Second)
	r.gh.AddReview(pollRepo, n, "pessoa", "CHANGES_REQUESTED")
	r.cycle()
	got := 0
	for _, e := range r.sink.events() {
		if e.Kind == github.EventReview {
			got++
			if !strings.HasPrefix(e.Summary, "changes_requested") {
				t.Errorf("summary %q", e.Summary)
			}
		}
	}
	if got != 1 {
		t.Errorf("%d review events, want the submitted one", got)
	}
}

func TestAMergeWhoseCommitGitHubDidNotSayYetIsMarkedWhenItDoes(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	r.clk.Advance(10 * time.Second)
	n, _ := r.tablePR("mesa-a", "feat/a")
	r.gh.SetPullState(pollRepo, n, "closed", true, "") // merged, and merge_commit_sha still empty
	r.sit.mu.Lock()
	r.sit.prs = nil
	r.sit.mu.Unlock()
	r.cycle()
	if _, marked := r.mir.get(); len(marked) != 0 {
		t.Fatalf("marked without a commit: %v", marked)
	}
	r.clk.Advance(5 * time.Minute)
	r.gh.SetPullMerge(pollRepo, n, "cccc")
	r.mir.mu.Lock()
	r.mir.reach["cccc"] = true
	r.mir.mu.Unlock()
	r.cycle()
	if _, marked := r.mir.get(); len(marked) != 1 {
		t.Fatalf("the merge was never marked: %v", marked)
	}
	for _, e := range r.sink.events() {
		if e.Kind == github.EventAlert {
			t.Errorf("a good merge became an alert: %+v", e)
		}
	}
}

func TestATableThatCanNotBeMarkedDoesNotStopTheWorkspace(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	r.clk.Advance(10 * time.Second)
	n, _ := r.tablePR("mesa-a", "feat/a")
	r.gh.SetPullMerge(pollRepo, n, "aaaa")
	r.gh.SetPullState(pollRepo, n, "closed", true, "")
	r.sit.mu.Lock()
	r.sit.prs = nil
	r.sit.mu.Unlock()
	r.mir.reach["aaaa"] = true
	r.mir.markErr = errors.New("the table is gone")
	r.cycle()
	r.gh.AddIssueComment(pollRepo, n, "pessoa", "oi")
	if !r.cycle() { // a failure of the table would repeat forever and block this
		t.Fatal("the workspace is stuck on the table that could not be marked")
	}
}

func TestMoreThanAHundredReviewsAndAChecksReRunAreRead(t *testing.T) {
	t.Parallel()
	r := newPoll(t)
	r.clk.Advance(10 * time.Second)
	n, sha := r.tablePR("mesa-a", "feat/a")
	for range 130 {
		r.gh.AddReview(pollRepo, n, "pessoa", "COMMENTED")
	}
	r.gh.AddCheck(pollRepo, sha, "ci", "failure")
	r.cycle()
	reviews := 0
	for _, e := range r.sink.events() {
		if e.Kind == github.EventReview {
			reviews++
		}
	}
	if reviews != 130 {
		t.Fatalf("%d reviews read, want 130", reviews)
	}
	// the check ended; a re-run makes another check run on the same commit
	r.clk.Advance(11 * time.Minute)
	r.gh.IssueToken("tok", "grao-mestre")
	r.gh.AddCheck(pollRepo, sha, "ci", "success")
	r.cycle()
	var ci int
	for _, e := range r.sink.events() {
		if e.Kind == github.EventCI {
			ci++
		}
	}
	if ci != 2 {
		t.Fatalf("%d CI events, want the failure and the re-run", ci)
	}
}
