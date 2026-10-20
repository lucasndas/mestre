package guild_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/fakechannel"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The pieces of the chain a request of the engine does not touch: it has no
// requester, no workspace and no agent in front of the people.
type noCharacters struct{}

func (noCharacters) Get(context.Context, string) (characters.Character, error) {
	return characters.Character{}, characters.ErrNoCharacter
}

func (noCharacters) Superior(context.Context, string) (characters.Superior, error) {
	return characters.Superior{People: true}, nil
}

func (noCharacters) TableMaster(context.Context, string, string) (characters.Character, bool, error) {
	return characters.Character{}, false, nil
}

func (noCharacters) GrandMaster(context.Context, string) (characters.Character, bool, error) {
	return characters.Character{}, false, nil
}
func (noCharacters) Relabel(context.Context, string, string) error { return nil }

type noInbox struct{}

func (noInbox) Enqueue(context.Context, inbox.NewItem) (inbox.Item, error) {
	return inbox.Item{}, errors.New("no inbox")
}
func (noInbox) Cancel(context.Context, string, inbox.State) error { return nil }
func (noInbox) Pump(context.Context, string) error                { return nil }

type noClasses struct{}

func (noClasses) Rules(context.Context, string) (*classes.Rules, error) {
	return nil, errors.New("no classes")
}

func (noClasses) Env(context.Context, string) (classes.Env, error) {
	return classes.Env{}, errors.New("no classes")
}

func (noClasses) Domains(context.Context, string) ([]string, error) { return nil, nil }

// lateFlowChain gives the flow the chain that is made after it.
type lateFlowChain struct{ c **chain.Service }

func (l lateFlowChain) Get(ctx context.Context, id int64) (chain.Request, error) {
	return (*l.c).Get(ctx, id)
}

func (l lateFlowChain) DecideByPerson(ctx context.Context, d chain.PersonDecision) (chain.Outcome, error) {
	return (*l.c).DecideByPerson(ctx, d)
}

func (l lateFlowChain) Invalidate(ctx context.Context, id int64, why string) error {
	return (*l.c).Invalidate(ctx, id, why)
}

// watched tells a test when the Owner was asked.
type watched struct {
	guild.Approver
	asked chan guild.ApprovalRequest
}

func (w watched) Ask(ctx context.Context, r guild.ApprovalRequest) (string, error) {
	id, err := w.Approver.Ask(ctx, r)
	if err == nil {
		w.asked <- r
	}
	return id, err
}

// entryWorld is the Guild of one machine whose Owner is asked through the real
// approver of the engine (app.EntryApprover), the chain of approval and the
// flow with the PIN, over a channel of messages that is a double.
type entryWorld struct {
	*rig
	chn   *fakechannel.Channel
	svc   *users.Service
	flow  *approvals.Flow
	chain *chain.Service
	owner users.User
	asked chan guild.ApprovalRequest
}

func newEntryWorld(t *testing.T, clk clock.Clock) *entryWorld {
	t.Helper()
	ew := &entryWorld{chn: fakechannel.New(), asked: make(chan guild.ApprovalRequest, 8)}
	var db *store.DB
	ew.rig = newRig(t, clk, func(cfg *guild.CitadelConfig) {
		db = cfg.DB
		var err error
		if ew.svc, err = users.New(users.Options{DB: db, Clock: clk, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}}); err != nil {
			t.Fatal(err)
		}
		if ew.owner, err = ew.svc.CreateOwner(context.Background(), "Lucas"); err != nil {
			t.Fatal(err)
		}
		if ew.flow, err = approvals.New(approvals.Options{DB: db, Clock: clk, Chain: lateFlowChain{&ew.chain}, Users: ew.svc, PINs: ew.svc, Messenger: ew.chn,
			Locked: func(context.Context) bool { return false }}); err != nil {
			t.Fatal(err)
		}
		entry := app.EntryApprover{Chain: func() *chain.Service { return ew.chain },
			Decide: func(ctx context.Context, id string, d guild.Decision) error { return ew.citadel.Decide(ctx, id, d) },
			Tell:   func(context.Context, string) {}}
		if ew.chain, err = chain.NewService(chain.Options{Store: &chain.Store{DB: db}, Clock: clk, Characters: noCharacters{}, Inbox: noInbox{}, Classes: noClasses{},
			Policy: chain.StaticPolicy{}, Users: ew.flow, Sinks: map[classes.RequestType]chain.Sink{classes.RequestGuildEntry: entry}}); err != nil {
			t.Fatal(err)
		}
		cfg.Approver = watched{Approver: entry, asked: ew.asked}
	})
	if err := ew.svc.SetPIN(context.Background(), ew.owner.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	return ew
}

// the request of the chain the Owner was asked about
func (ew *entryWorld) pending(t *testing.T) (int64, string) {
	t.Helper()
	_, msg, ok := ew.chn.To(ew.owner.ID)
	if !ok {
		t.Fatal("the Owner was not asked")
	}
	return msg.Request.ID, msg.Text
}

// CA-GUILDA-02
func TestTheOwnerAcceptsAnEntryThroughTheButtonAndThePINAndNothingIsIssuedWithout(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(day0)
	ew := newEntryWorld(t, clk)
	n := ew.newNode("desktop-ana")
	j := ew.startJoin(n)

	q := <-ew.asked
	code := <-j.code
	id, text := ew.pending(t)
	if q.Code != code || len(code) != 7 || code[3] != ' ' || !strings.Contains(text, code) {
		t.Fatalf("the Owner sees %q (%q), the new machine shows %q; want the same six digits", q.Code, text, code)
	}
	_, msg, _ := ew.chn.To(ew.owner.ID)
	if !slices.Equal(msg.Answers, []chain.Answer{chain.AnswerApprove, chain.AnswerDeny}) {
		t.Errorf("the Owner was offered %v, want only to approve or deny", msg.Answers)
	}
	ew.expectNothingIssued() // nothing is issued before the answer

	rep, err := ew.flow.Answer(t.Context(), ew.owner.ID, approvals.Input{Request: id, Answer: chain.AnswerApprove})
	if err != nil || rep.PIN == "" {
		t.Fatalf("the button: %+v, %v", rep, err)
	}
	ew.expectNothingIssued() // the button alone issues nothing
	if err := ew.svc.CheckPIN(t.Context(), rep.PIN, "000000"); err == nil {
		t.Fatal("a wrong PIN was accepted")
	}
	if _, err := ew.flow.ConfirmPIN(t.Context(), ew.owner.ID, rep.PIN); !errors.Is(err, approvals.ErrPIN) {
		t.Fatalf("after a wrong PIN: %v", err)
	}
	ew.expectNothingIssued()
	if err := ew.svc.CheckPIN(t.Context(), rep.PIN, "123456"); err != nil {
		t.Fatal(err)
	}
	if _, err := ew.flow.ConfirmPIN(t.Context(), ew.owner.ID, rep.PIN); err != nil {
		t.Fatal(err)
	}
	got, err := j.result()
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != code || got.GuildID != ew.founded.GuildID || got.Name != "desktop-ana" || got.Excluded {
		t.Fatalf("joined = %+v", got.Identity)
	}
	if !slices.Equal(got.Motor.DNSNames, []string{"desktop-ana.local"}) || !slices.Equal(got.Guardian.DNSNames, []string{"desktop-ana.local"}) {
		t.Errorf("certificate names = %v %v, want only desktop-ana.local (never mestre.local)", got.Motor.DNSNames, got.Guardian.DNSNames)
	}
	if ew.count("machines") != 2 || ew.count("machine_credentials") != 4 {
		t.Errorf("registry: %d machines, %d credentials", ew.count("machines"), ew.count("machine_credentials"))
	}
	// who approved is the user that gave the PIN, as recorded
	if n := ew.countWhere("join_requests", "state = 'approved' AND decided_by = ?", ew.owner.ID); n != 1 {
		t.Errorf("%d entries approved by the Owner", n)
	}
	if r, err := ew.chain.Get(t.Context(), id); err != nil || r.State != chain.Approved {
		t.Errorf("the request = %+v, %v", r, err)
	}
}

// CA-GUILDA-02
func TestAnOwnerWhoDeniesTheEntryIssuesNothingAndAnAdministratorCannotAnswerIt(t *testing.T) {
	t.Parallel()
	ew := newEntryWorld(t, clock.NewFake(day0))
	ana, err := ew.svc.Create(t.Context(), ew.owner, users.NewUser{Name: "Ana", Level: users.Administrator, Workspaces: []string{"loja"}})
	if err != nil {
		t.Fatal(err)
	}
	n := ew.newNode("desktop-ana")
	j := ew.startJoin(n)
	<-ew.asked
	id, _ := ew.pending(t)
	if _, _, ok := ew.chn.To(ana.ID); ok {
		t.Error("an Administrator was asked about an entry in the Guild")
	}
	if _, err := ew.flow.Answer(t.Context(), ana.ID, approvals.Input{Request: id, Answer: chain.AnswerApprove}); !errors.Is(err, approvals.ErrNotYours) {
		t.Errorf("an Administrator answering: %v", err)
	}
	ew.expectNothingIssued()
	if rep, err := ew.flow.Answer(t.Context(), ew.owner.ID, approvals.Input{Request: id, Answer: chain.AnswerDeny}); err != nil || rep.PIN != "" {
		t.Fatalf("deny: %+v, %v", rep, err)
	}
	if _, err := j.result(); err == nil {
		t.Fatal("the machine entered after the Owner denied")
	}
	ew.expectNothingIssued()
}

// The hour of the entry ends the question: the request is taken down, the
// buttons go away and an answer that comes late issues nothing.
func TestAnEntryThatNobodyAnswersIsTakenDownAfterTheHour(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(day0)
	ew := newEntryWorld(t, clk)
	n := ew.newNode("desktop-ana")
	j := ew.startJoin(n)
	<-ew.asked
	id, _ := ew.pending(t)
	clk.Advance(time.Hour)
	if _, err := j.result(); err == nil {
		t.Fatal("the machine entered with nobody answering")
	}
	if r, err := ew.chain.Get(t.Context(), id); err != nil || r.State != chain.Expired {
		t.Fatalf("the request = %+v, %v, want expired", r, err)
	}
	if _, err := ew.flow.Answer(t.Context(), ew.owner.ID, approvals.Input{Request: id, Answer: chain.AnswerApprove}); !errors.Is(err, approvals.ErrNotOpen) {
		t.Errorf("an answer after the hour: %v", err)
	}
	ew.expectNothingIssued()
}
