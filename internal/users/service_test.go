package users_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/users"
)

func TestThereIsOnlyOneOwnerAndTheOwnerCannotSuspendOrRemoveSelf(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if _, err := w.svc.Create(w.ctx, w.owner, users.NewUser{Name: "Outro", Level: users.Owner}); !errors.Is(err, users.ErrOwnerExists) {
		t.Errorf("a second Owner = %v", err)
	}
	if _, err := w.svc.CreateOwner(w.ctx, "Outro"); !errors.Is(err, users.ErrOwnerExists) {
		t.Errorf("a second first Owner = %v", err)
	}
	if err := w.svc.Suspend(w.ctx, w.owner, w.owner.ID); !errors.Is(err, users.ErrSelf) {
		t.Errorf("the Owner suspends self = %v", err)
	}
	if err := w.svc.Remove(w.ctx, w.owner, w.owner.ID); !errors.Is(err, users.ErrSelf) {
		t.Errorf("the Owner removes self = %v", err)
	}
	ana := w.add("Ana", users.Administrator, "loja")
	if _, err := w.svc.Create(w.ctx, ana, users.NewUser{Name: "X", Level: users.Observer}); !errors.Is(err, users.ErrLevel) {
		t.Errorf("an Administrator registers a user = %v", err)
	}
	if _, err := w.svc.Edit(w.ctx, w.owner, ana.ID, users.Edit{Level: ptr(users.Owner)}); !errors.Is(err, users.ErrOwnerExists) {
		t.Errorf("a user made Owner by edit = %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestAUserActsOnlyInTheWorkspacesOfTheList(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver, "loja")
	if !ana.Acts("loja") || ana.Acts("blog") || !w.owner.Acts("blog") {
		t.Errorf("acts: ana loja=%v blog=%v, owner blog=%v", ana.Acts("loja"), ana.Acts("blog"), w.owner.Acts("blog"))
	}
	if err := w.svc.Suspend(w.ctx, w.owner, ana.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := w.svc.Get(w.ctx, ana.ID)
	if got.Acts("loja") {
		t.Error("a suspended user acts")
	}
}

// CA-USR-05
func TestSuspendingAnApproverInvalidatesWhatHeApprovedAndRemovingFreesTheTelegramId(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver, "loja")
	w.pair(ana, 4242)
	if err := w.svc.Suspend(w.ctx, w.owner, ana.ID); err != nil {
		t.Fatal(err)
	}
	if len(w.appr.ids) != 1 || w.appr.ids[0] != ana.ID {
		t.Fatalf("approvals invalidated for %v, want Ana", w.appr.ids)
	}
	// the bot ignores a suspended user; the id stays his until he is removed
	if u, err := w.svc.ByTelegram(w.ctx, 4242); err != nil || u.State != users.Suspended {
		t.Errorf("ByTelegram = %+v, %v", u, err)
	}
	if err := w.svc.Reactivate(w.ctx, w.owner, ana.ID); err != nil {
		t.Fatal(err)
	}
	if len(w.appr.ids) != 1 {
		t.Error("reactivating invalidated approvals")
	}
	if err := w.svc.Remove(w.ctx, w.owner, ana.ID); err != nil {
		t.Fatal(err)
	}
	if len(w.appr.ids) != 2 {
		t.Errorf("removing did not invalidate: %v", w.appr.ids)
	}
	if _, err := w.svc.ByTelegram(w.ctx, 4242); !errors.Is(err, users.ErrNoUser) {
		t.Errorf("the Telegram id of a removed user = %v, want free", err)
	}
	bia := w.add("Bia", users.Observer)
	w.pair(bia, 4242) // the id is free again
	// and the removed row stays for the history
	if got, err := w.svc.Get(w.ctx, ana.ID); err != nil || got.State != users.Removed || got.Name != "Ana" {
		t.Errorf("the removed user = %+v, %v", got, err)
	}
}

func TestANarrowerLevelOrListOfWorkspacesInvalidatesTheApprovals(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Administrator, "loja", "blog")
	if _, err := w.svc.Edit(w.ctx, w.owner, ana.ID, users.Edit{Workspaces: ptr([]string{"loja", "blog", "novo"})}); err != nil || len(w.appr.ids) != 0 {
		t.Fatalf("widening: %v, invalidated %v", err, w.appr.ids)
	}
	if _, err := w.svc.Edit(w.ctx, w.owner, ana.ID, users.Edit{Workspaces: ptr([]string{"loja"})}); err != nil || len(w.appr.ids) != 1 {
		t.Fatalf("narrowing the workspaces: %v, invalidated %v", err, w.appr.ids)
	}
	if _, err := w.svc.Edit(w.ctx, w.owner, ana.ID, users.Edit{Level: ptr(users.Observer)}); err != nil || len(w.appr.ids) != 2 {
		t.Fatalf("narrowing the level: %v, invalidated %v", err, w.appr.ids)
	}
}

// CA-USR-03
func TestACodePairsOnceAndTheSecondUseIsRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver, "loja")
	c, err := w.svc.NewCode(w.ctx, w.owner, ana.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Code) != users.CodeLength || strings.ContainsAny(c.Code, "01OI") || !users.LooksLikeCode("/start "+c.Code) {
		t.Fatalf("code %q is not of the format", c.Code)
	}
	got, err := w.svc.Pair(w.ctx, users.PairRequest{Text: "/start " + strings.ToLower(c.Code), TelegramID: 111, Private: true})
	if err != nil || got.TelegramID != 111 || got.ID != ana.ID {
		t.Fatalf("Pair = %+v, %v", got, err)
	}
	if len(w.al.owner) != 1 {
		t.Errorf("the Owner was told %v, want one notice", w.al.owner)
	}
	if _, err := w.svc.Pair(w.ctx, users.PairRequest{Text: c.Code, TelegramID: 111, Private: true}); !errors.Is(err, users.ErrBadCode) {
		t.Errorf("the second use of the code = %v", err)
	}
	if _, err := w.svc.NewCode(w.ctx, ana, ana.ID); !errors.Is(err, users.ErrLevel) {
		t.Errorf("an Approver makes a code = %v", err)
	}
}

// CA-USR-04
func TestACodeOlderThanThirtyMinutesFailsAndCountsAsAnUnauthorizedAttempt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver)
	c, _ := w.svc.NewCode(w.ctx, w.owner, ana.ID)
	w.clk.Advance(users.CodeTTL + time.Second)
	if _, err := w.svc.Pair(w.ctx, users.PairRequest{Text: c.Code, TelegramID: 111, Private: true}); !errors.Is(err, users.ErrBadCode) {
		t.Fatalf("an expired code = %v", err)
	}
	if len(w.al.unauthorized) != 1 {
		t.Errorf("unauthorized attempts: %v, want one", w.al.unauthorized)
	}
}

// CA-USR-09
func TestSixWrongCodesInAnHourSilenceTheBotAndACodeOfAnotherUserIsNotSpent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver)
	bia := w.add("Bia", users.Approver)
	for i := range users.PairingFailures {
		if _, err := w.svc.Pair(w.ctx, users.PairRequest{Text: "AAAAAAA" + string(users.CodeAlphabet[i]), TelegramID: 999, Private: true}); !errors.Is(err, users.ErrBadCode) {
			t.Fatalf("wrong code %d = %v", i, err)
		}
	}
	if len(w.al.unauthorized) != users.PairingFailures {
		t.Errorf("%d failures recorded, want %d", len(w.al.unauthorized), users.PairingFailures)
	}
	c, _ := w.svc.NewCode(w.ctx, w.owner, ana.ID)
	if _, err := w.svc.Pair(w.ctx, users.PairRequest{Text: c.Code, TelegramID: 999, Private: true}); !errors.Is(err, users.ErrSilent) {
		t.Fatalf("the sixth attempt = %v, want silence (even with a right code)", err)
	}
	w.clk.Advance(users.PairingWindow + time.Minute)
	// the code of Ana sent by the id that already is Bia's
	w.pair(bia, 555)
	c2, _ := w.svc.NewCode(w.ctx, w.owner, ana.ID)
	if _, err := w.svc.Pair(w.ctx, users.PairRequest{Text: c2.Code, TelegramID: 555, Private: true}); !errors.Is(err, users.ErrTelegramTaken) {
		t.Fatalf("a code of Ana from the id of Bia = %v", err)
	}
	if _, err := w.svc.Pair(w.ctx, users.PairRequest{Text: c2.Code, TelegramID: 777, Private: true}); err != nil {
		t.Errorf("the code was spent by the refusal: %v", err)
	}
}

func TestThePairingIsOnlyInAPrivateChatAndFollowsTheIdOfTheRegistration(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Approver)
	w.pair(ana, 111)
	c, _ := w.svc.NewCode(w.ctx, w.owner, ana.ID)
	if _, err := w.svc.Pair(w.ctx, users.PairRequest{Text: c.Code, TelegramID: 111, Private: false}); !errors.Is(err, users.ErrNotPrivate) {
		t.Errorf("a group = %v", err)
	}
	if _, err := w.svc.Pair(w.ctx, users.PairRequest{Text: c.Code, TelegramID: 222, Private: true}); !errors.Is(err, users.ErrWrongID) {
		t.Errorf("another id than the registered one = %v", err)
	}
	if err := w.svc.ClearTelegram(w.ctx, w.owner, ana.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Pair(w.ctx, users.PairRequest{Text: c.Code, TelegramID: 222, Private: true}); err != nil {
		t.Errorf("after clearing the id: %v", err)
	}
}

// CA-USR-07
func TestFivePinErrorsInFifteenMinutesLockThePinAndTheSixthIsRefusedWithoutVerification(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.pin(w.owner, "123456")
	var open users.PINRequest // a request opened before the fifth error
	for i := range users.PINLockErrors {
		r, err := w.svc.StartPIN(w.ctx, w.owner.ID, "cofre")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if i == users.PINLockErrors-1 {
			if open, err = w.svc.StartPIN(w.ctx, w.owner.ID, "cofre"); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.svc.CheckPIN(w.ctx, r.ID, "000000"); !errors.Is(err, users.ErrBadPIN) {
			t.Fatalf("wrong pin %d = %v", i, err)
		}
		w.clk.Advance(time.Minute)
	}
	if len(w.al.owner) == 0 || !strings.Contains(w.al.owner[len(w.al.owner)-1], "PIN") {
		t.Fatalf("the Owner was not alerted: %v", w.al.owner)
	}
	if _, err := w.svc.StartPIN(w.ctx, w.owner.ID, "cofre"); !errors.Is(err, users.ErrPINLocked) {
		t.Errorf("a new request while locked = %v", err)
	}
	// the right PIN, in the request that was open before the lock, is refused
	// without verification and the request is not spent
	if err := w.svc.CheckPIN(w.ctx, open.ID, "123456"); !errors.Is(err, users.ErrPINLocked) {
		t.Errorf("the right PIN during the lock = %v, want ErrPINLocked", err)
	}
	if err := w.svc.Consume(w.ctx, open.ID, w.owner.ID, "cofre"); !errors.Is(err, users.ErrPINRequest) {
		t.Errorf("the request answered during the lock was spent: %v", err)
	}
	w.clk.Advance(20 * time.Minute) // still inside the 30 minutes
	if err := w.svc.CheckPIN(w.ctx, open.ID, "123456"); !errors.Is(err, users.ErrPINLocked) {
		t.Errorf("the right PIN inside the 30 minutes = %v, want ErrPINLocked", err)
	}
	w.clk.Advance(time.Hour) // the lock is over
	r, err := w.svc.StartPIN(w.ctx, w.owner.ID, "cofre")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.svc.CheckPIN(w.ctx, r.ID, "123456"); err != nil {
		t.Errorf("the right PIN after the lock = %v", err)
	}
}

func TestThreeWrongPinsInOneRequestCancelItAndARightOneIsSpentOnce(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.pin(w.owner, "123456")
	r, _ := w.svc.StartPIN(w.ctx, w.owner.ID, "cofre")
	for range users.PINRequestErrors {
		_ = w.svc.CheckPIN(w.ctx, r.ID, "000000")
	}
	if err := w.svc.CheckPIN(w.ctx, r.ID, "123456"); !errors.Is(err, users.ErrPINRequest) {
		t.Errorf("a right PIN in a cancelled request = %v", err)
	}
	r2, _ := w.svc.StartPIN(w.ctx, w.owner.ID, "cofre")
	if err := w.svc.CheckPIN(w.ctx, r2.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Consume(w.ctx, r2.ID, w.owner.ID, "outra"); !errors.Is(err, users.ErrPINRequest) {
		t.Errorf("a request spent on another action = %v", err)
	}
	if err := w.svc.Consume(w.ctx, r2.ID, w.owner.ID, "cofre"); err != nil {
		t.Errorf("Consume = %v", err)
	}
	if err := w.svc.Consume(w.ctx, r2.ID, w.owner.ID, "cofre"); !errors.Is(err, users.ErrPINRequest) {
		t.Errorf("the second Consume = %v", err)
	}
	r3, _ := w.svc.StartPIN(w.ctx, w.owner.ID, "cofre")
	w.clk.Advance(users.PINRequestTTL + time.Second)
	if err := w.svc.CheckPIN(w.ctx, r3.ID, "123456"); !errors.Is(err, users.ErrPINRequest) {
		t.Errorf("an expired request = %v", err)
	}
}

func TestAUserWithoutPinCannotDoAnActionThatAsksForIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if _, err := w.svc.StartPIN(w.ctx, w.owner.ID, "cofre"); !errors.Is(err, users.ErrNoPIN) {
		t.Errorf("no PIN = %v", err)
	}
	if err := w.svc.SetPIN(w.ctx, w.owner.ID, "12ab"); err == nil {
		t.Error("a PIN with letters was taken")
	}
	w.pin(w.owner, "654321")
	got, _ := w.svc.Get(w.ctx, w.owner.ID)
	if !got.HasPIN {
		t.Error("the PIN was not kept")
	}
	if err := w.svc.ResetPIN(w.ctx, w.owner, w.owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.StartPIN(w.ctx, w.owner.ID, "cofre"); !errors.Is(err, users.ErrNoPIN) {
		t.Errorf("after the reset = %v", err)
	}
}
