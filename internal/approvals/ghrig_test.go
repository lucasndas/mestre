package approvals_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/approvals"
	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/fakegithub"
	"github.com/mestre-ia/mestre/internal/testkit/fakenet"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// opener records the requests the engine opens.
type opener struct {
	mu   sync.Mutex
	made []chain.OpenRequest
}

func (o *opener) Request(_ context.Context, r chain.OpenRequest) (int64, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.made = append(o.made, r)
	return int64(100 + len(o.made)), nil
}

func (o *opener) all() []chain.OpenRequest {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]chain.OpenRequest(nil), o.made...)
}

// teller records what is said to the users and to the Owner.
type teller struct {
	mu   sync.Mutex
	user map[string][]string
	ws   []string
}

func (t *teller) Tell(_ context.Context, userID, text string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.user == nil {
		t.user = map[string][]string{}
	}
	t.user[userID] = append(t.user[userID], text)
	return nil
}

func (t *teller) toWorkspace(_ context.Context, _, text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ws = append(t.ws, text)
}

func (t *teller) said(userID string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.user[userID]...)
}

func (t *teller) toOwner() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.ws...)
}

// credentials gives a client to the people who have a personal credential.
type credentials struct {
	clients map[string]approvals.ReviewClient
}

func (c credentials) Client(_ context.Context, id string) (approvals.ReviewClient, error) {
	if cl, ok := c.clients[id]; ok {
		return cl, nil
	}
	return nil, approvals.ErrNoCredential
}

// owners are the logins the repository requires.
type owners struct {
	logins []string
	err    error
}

func (o owners) Required(context.Context, string, string, int) ([]string, error) {
	return o.logins, o.err
}

// delegations is the decree of delegation of a user, when there is one.
type delegations struct {
	author string
	used   []int64
}

func (d *delegations) Delegation(context.Context, chain.Request) (string, bool, error) {
	return d.author, d.author != "", nil
}

func (d *delegations) Used(_ context.Context, r chain.Request, _ string) error {
	d.used = append(d.used, r.ID)
	return nil
}

// ghLab is a repository on the fake GitHub with the Apps and the people the
// application of an approval needs.
type ghLab struct {
	t     *testing.T
	ctx   context.Context
	clk   *clock.Fake
	db    *store.DB
	gh    *fakegithub.Server
	svc   *users.Service
	owner users.User
	store *chain.Store
	grand *github.REST
	open  *opener
	tell  *teller
	modes *approvals.Modes
	pr    int
	sha   string
	nextR int64
}

func (l *ghLab) client(token string) *github.REST {
	return &github.REST{BaseURL: "https://api.github.com", HTTP: &http.Client{Transport: fakenet.HandlerTransport(l.gh)},
		Token: func(context.Context, string) (string, error) { return token, nil }}
}

func newGHLab(t *testing.T) *ghLab {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES ('loja', 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	gh, err := fakegithub.New(clk, t)
	if err != nil {
		t.Fatal(err)
	}
	gh.AddRepo("lucas", "meu-app")
	gh.IssueToken("ghs_grao", "mestre-grao-mestre")
	svc, err := users.New(users.Options{DB: db, Clock: clk, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
	if err != nil {
		t.Fatal(err)
	}
	l := &ghLab{t: t, ctx: ctx, clk: clk, db: db, gh: gh, svc: svc, store: &chain.Store{DB: db}, open: &opener{}, tell: &teller{},
		modes: &approvals.Modes{DB: db, Clock: clk}, sha: strings.Repeat("a", 40)}
	l.grand = l.client("ghs_grao")
	if l.owner, err = svc.CreateOwner(ctx, "Lucas"); err != nil {
		t.Fatal(err)
	}
	l.pr = gh.AddPR("lucas/meu-app", "Login", "42-login", "lucas/meu-app")
	gh.SetPullAuthor("lucas/meu-app", l.pr, "mestre-dev[bot]")
	gh.SetPull("lucas/meu-app", l.pr, l.sha, false, "clean")
	return l
}

// person registers a user with the login on GitHub.
func (l *ghLab) person(name, login string, level users.Level) users.User {
	l.t.Helper()
	u, err := l.svc.Create(l.ctx, l.owner, users.NewUser{Name: name, Level: level, GitHubLogin: login, Workspaces: []string{"loja"}})
	if err != nil {
		l.t.Fatal(err)
	}
	l.gh.AddUser(login, "User")
	return u
}

// approved stores a request of merge for the PR that was approved by `by`
// ("user:<id>" or the id of the Grão-Mestre).
func (l *ghLab) approved(by, sha string) chain.Request {
	l.t.Helper()
	l.nextR++
	r := chain.Request{Workspace: "loja", Table: "42-login", Type: classes.RequestMerge, State: chain.Approved, Level: chain.Level{Kind: chain.LevelPeople},
		PR: &chain.PRRef{Repo: "lucas/meu-app", Number: l.pr, SHA: sha}, CreatedAt: l.clk.Now(), ClosedAt: l.clk.Now(),
		Decision: &chain.Closing{State: chain.Approved, By: by, Answer: chain.AnswerApprove}}
	r, err := l.store.Insert(l.ctx, r)
	if err != nil {
		l.t.Fatal(err)
	}
	return r
}

func (l *ghLab) approver(mod func(*approvals.Approver)) *approvals.Approver {
	a := &approvals.Approver{Approved: l.store, Opener: l.open, Users: l.svc, Modes: l.modes, Tell: l.tell,
		GrandMaster: func(context.Context, string) (approvals.ReviewClient, string, error) {
			return l.grand, "mestre-grao-mestre[bot]", nil
		}}
	if mod != nil {
		mod(a)
	}
	return a
}

func (l *ghLab) order() approvals.ApproveOrder {
	return approvals.ApproveOrder{Workspace: "loja", Repo: "lucas/meu-app", Number: l.pr, SHA: l.sha, Reason: "tudo verde e a mudança é pequena"}
}

// sent are the reviews that reached GitHub, with the marks of the engine hidden.
func (l *ghLab) sent() []fakegithub.SentReview {
	rs := l.gh.ReviewsOf("lucas/meu-app", l.pr)
	for i := range rs {
		rs[i].Body = github.HideMarkers(rs[i].Body)
	}
	return rs
}

// isHumanRequired says the Approver asks the people: with --on-behalf, the refusal that is about
// the delegation carries its own code (CodeNoDelegation), which is the same demand.
func isHumanRequired(err error) bool {
	var ref *chain.Refusal
	if errors.As(err, &ref) && ref.Code == approvals.CodeNoDelegation {
		return true
	}
	return isHumanRequiredOnly(err)
}

func isHumanRequiredOnly(err error) bool {
	var ref *chain.Refusal
	return errors.As(err, &ref) && ref.Code == chain.CodeHumanRequired
}

func decodeTarget(t *testing.T, raw json.RawMessage) approvals.SettingsTarget {
	t.Helper()
	var tg approvals.SettingsTarget
	if err := json.Unmarshal(raw, &tg); err != nil {
		t.Fatal(err)
	}
	return tg
}
