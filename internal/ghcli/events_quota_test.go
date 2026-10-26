package ghcli_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/testkit/fakenet"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// pollWorld is the poller of the events over the same Guild, double of GitHub and
// quota as the commands: the quota is of the App "mestre", whose token both use.
type pollWorld struct {
	p      *github.Poller
	mu     sync.Mutex
	alerts []string
	ws     []github.Watch
}

type quietSituation struct{ mode github.Mode }

func (q quietSituation) Mode(context.Context, github.Watch) github.Mode { return q.mode }
func (quietSituation) TablePRs(context.Context, github.Watch) []github.TablePR {
	return nil
}
func (quietSituation) TableOf(context.Context, string, int) string { return "" }

type nullSink struct{}

func (nullSink) Deliver(context.Context, github.Watch, string, []github.Event) error { return nil }

func newPollWorld(t *testing.T, r *rig, workspaces ...string) *pollWorld {
	t.Helper()
	w := &pollWorld{}
	for _, name := range workspaces {
		w.ws = append(w.ws, github.Watch{Workspace: name, Repo: repoFull, Since: r.clk.Now()})
	}
	p, err := github.NewPoller(github.PollerOptions{
		DB: r.db, Clock: r.clk, API: "https://api.github.com", HTTP: &http.Client{Transport: fakenet.HandlerTransport(r.gh)},
		Token: func(context.Context, string) (string, error) { return "tok_mestre", nil },
		Sink:  nullSink{}, Situation: quietSituation{mode: github.ModeAttentive},
		Watches: func(context.Context) ([]github.Watch, error) { return w.ws, nil },
		Quota:   r.quota, App: "mestre",
		Alert: func(_ context.Context, text string) { w.mu.Lock(); w.alerts = append(w.alerts, text); w.mu.Unlock() },
	})
	if err != nil {
		t.Fatal(err)
	}
	w.p = p
	return w
}

func (w *pollWorld) told() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.alerts...)
}

// CA-GH-EV-09
func TestWhenTheQuotaRunsOutEveryLoopWaitsTheOwnerIsToldOnceAndAWriteAsksedMeanwhileGoesOutOnceAfterTheRenewal(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	prOfTheTable(t, r)
	n := r.gh.AddIssue(repoFull, "da mesa")
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindIssue, Number: n, Table: "meu-app/42-login"}); err != nil {
		t.Fatal(err)
	}
	pw := newPollWorld(t, r, "meu-app", "outro")
	reset := r.clk.Now().Add(30 * time.Minute)
	r.gh.FailNextRead(http.StatusForbidden, map[string]string{
		"x-ratelimit-limit": "5000", "x-ratelimit-remaining": "0", "x-ratelimit-reset": strconv.FormatInt(reset.Unix(), 10)})
	if err := pw.p.Tick(r.ctx); err != nil {
		t.Fatal(err)
	}
	reads := len(r.gh.Reads())
	if told := pw.told(); len(told) != 1 || !strings.Contains(told[0], "12:30 UTC") {
		t.Fatalf("the Owner was told %q", told)
	}
	// no workspace is read, not even the one that was not asked yet
	r.clk.Advance(10 * time.Minute)
	for range 3 {
		_ = pw.p.Tick(r.ctx)
	}
	if len(r.gh.Reads()) != reads {
		t.Fatalf("GitHub was asked %d times with the quota over", len(r.gh.Reads())-reads)
	}
	// a comment asked meanwhile waits in the queue and goes out once
	// (it keeps the uuid of the request, which is the marker of the operation)
	uuid, err := localchan.NewID(clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	got := r.runID(uuid, "tm", "issue comment", nil, itoa(n), "uma vez só")
	if !got.OK || got.Data["queued"] != true {
		t.Fatalf("the comment: %+v", got)
	}
	if cs := r.gh.Comments(repoFull, n); len(cs) != 0 {
		t.Fatalf("the comment went out with the quota over: %q", cs)
	}
	r.clk.Advance(20 * time.Minute) // the renewal
	r.gh.IssueToken("tok_mestre", "mestre")
	if cs := r.gh.Comments(repoFull, n); len(cs) != 1 || !strings.HasPrefix(cs[0], "uma vez só") || !strings.Contains(cs[0], "mestre:op="+uuid) {
		t.Fatalf("after the renewal the comments are %q, want one carrying the uuid %s", cs, uuid)
	}
	// the same request again (a resending of the CLI) does not make a second one
	if again := r.runID(uuid, "tm", "issue comment", nil, itoa(n), "uma vez só"); !again.OK {
		t.Fatalf("the resending: %+v", again)
	}
	if cs := r.gh.Comments(repoFull, n); len(cs) != 1 {
		t.Fatalf("the resending made %d comments, want 1", len(cs))
	}
	_ = pw.p.Tick(r.ctx)
	if len(r.gh.Reads()) == reads {
		t.Error("the loops did not come back after the renewal")
	}
	if told := pw.told(); len(told) != 1 {
		t.Errorf("the Owner was told %d times", len(told))
	}
	// a new outage is told; still limited when it came back, it is not told again;
	// after a good reading, the next one is
	run := func(after time.Duration) {
		reset := r.clk.Now().Add(after)
		r.gh.FailNextRead(http.StatusForbidden, map[string]string{
			"x-ratelimit-limit": "5000", "x-ratelimit-remaining": "0", "x-ratelimit-reset": strconv.FormatInt(reset.Unix(), 10)})
	}
	r.clk.Advance(github.IntervalAttentive)
	run(10 * time.Minute)
	_ = pw.p.Tick(r.ctx)
	if told := pw.told(); len(told) != 2 {
		t.Fatalf("a new outage was told %d times in all, want 2", len(told))
	}
	r.clk.Advance(11 * time.Minute)
	r.gh.IssueToken("tok_mestre", "mestre")
	run(10 * time.Minute)
	_ = pw.p.Tick(r.ctx)
	if told := pw.told(); len(told) != 2 {
		t.Fatalf("the same outage was told %d times in all", len(told))
	}
	r.clk.Advance(11 * time.Minute)
	r.gh.IssueToken("tok_mestre", "mestre")
	_ = pw.p.Tick(r.ctx) // a good reading
	r.clk.Advance(github.IntervalIdle)
	run(10 * time.Minute)
	_ = pw.p.Tick(r.ctx)
	if told := pw.told(); len(told) != 3 {
		t.Fatalf("the outage after a good reading was told %d times in all, want 3", len(told))
	}
}

// CA-GH-EV-10
func TestUnderTenPercentTheWorkspaceIsParadoWritesWaitAndASecondaryLimitIsNotRepeated(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	prOfTheTable(t, r)
	n := r.gh.AddIssue(repoFull, "da mesa")
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindIssue, Number: n, Table: "meu-app/42-login"}); err != nil {
		t.Fatal(err)
	}
	pw := newPollWorld(t, r, "meu-app")
	// a change was just seen: the workspace is active (30 s)
	r.clk.Advance(10 * time.Second)
	r.gh.AddIssueComment(repoFull, n, "pessoa", "oi")
	if err := pw.p.Tick(r.ctx); err != nil {
		t.Fatal(err)
	}
	if got := pw.p.Interval(r.ctx, pw.ws[0]); got != github.IntervalActive {
		t.Fatalf("before the quota is low: %v", got)
	}
	// 9% left (the answers teach the poller)
	r.gh.SetUsed("tok_mestre", 4550)
	r.clk.Advance(github.IntervalActive)
	if err := pw.p.Tick(r.ctx); err != nil {
		t.Fatal(err)
	}
	if got := pw.p.Interval(r.ctx, pw.ws[0]); got != github.IntervalIdle {
		t.Fatalf("with 9%% of the quota: %v, want the idle interval (parado)", got)
	}
	// what changes data waits for the renewal
	got := r.run("tm", "issue comment", nil, itoa(n), "depois")
	if !got.OK || got.Data["queued"] != true {
		t.Fatalf("a write under 10%%: %+v", got)
	}
	if cs := r.gh.Comments(repoFull, n); len(cs) != 1 { // only the one of the person
		t.Fatalf("the comment went out: %q", cs)
	}
	// and the reading goes on, at the idle interval
	reads := len(r.gh.Reads())
	r.clk.Advance(github.IntervalIdle)
	if err := pw.p.Tick(r.ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.gh.Reads()) == reads {
		t.Error("the reading stopped with the quota low")
	}
	// after the renewal, a secondary limit makes the write fail with the reason and it is not repeated
	r.clk.Advance(time.Hour)
	r.gh.IssueToken("tok_mestre", "mestre")
	r.gh.FailNextWrite(http.StatusForbidden, "60")
	m := r.run("tm", "issue comment", nil, itoa(n), "x")
	if m.OK || m.Code != "github_unavailable" || !strings.Contains(m.Error, "limit") {
		t.Fatalf("under a secondary limit: %+v", m)
	}
	r.clk.Advance(5 * time.Minute)
	for _, c := range r.gh.Comments(repoFull, n) {
		if c == "x" {
			t.Error("the write refused by the secondary limit was repeated")
		}
	}
}

func TestAnAnswerOutOfOrderDoesNotHideTheLowestCountOfTheWindow(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	reset := strconv.FormatInt(r.clk.Now().Add(time.Hour).Unix(), 10)
	h := func(remaining string) http.Header {
		return http.Header{"X-Ratelimit-Limit": {"5000"}, "X-Ratelimit-Remaining": {remaining}, "X-Ratelimit-Reset": {reset}}
	}
	r.quota.Observe("mestre", h("400"))
	r.quota.Observe("mestre", h("600")) // a slower answer that was sent earlier
	if _, low := r.quota.Low("mestre"); !low {
		t.Fatal("the count of 600 hid the one of 400: the App is under 10%")
	}
}

func TestALimitWithAResetInThePastDoesNotMakeALoopAndTheLongerHoldWins(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	pw := newPollWorld(t, r, "meu-app", "outro")
	stale := strconv.FormatInt(r.clk.Now().Add(-10*time.Second).Unix(), 10)
	r.gh.FailNextRead(http.StatusForbidden, map[string]string{"x-ratelimit-limit": "5000", "x-ratelimit-remaining": "0", "x-ratelimit-reset": stale})
	_ = pw.p.Tick(r.ctx)
	reads := len(r.gh.Reads())
	for range 5 {
		r.clk.Advance(10 * time.Second)
		_ = pw.p.Tick(r.ctx)
	}
	if got := len(r.gh.Reads()) - reads; got != 0 {
		t.Fatalf("%d requests in 50 s after a limit with a reset in the past", got)
	}
}

func TestASecondaryLimitStopsOnlyThatWorkspaceAndTellsNobody(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	pw := newPollWorld(t, r, "meu-app", "outro")
	r.gh.FailNextRead(http.StatusTooManyRequests, map[string]string{"Retry-After": "60"})
	_ = pw.p.Tick(r.ctx)
	if told := pw.told(); len(told) != 0 {
		t.Fatalf("the Owner was told of a secondary limit: %q", told)
	}
	// the other workspace was read in the same tick
	if len(r.gh.Reads()) == 0 {
		t.Fatal("the other workspace was not read")
	}
}
