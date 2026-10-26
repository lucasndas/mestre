package users_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/users"
)

// The table of levels of the document: what each level can do.
func TestTheTableOfLevelsIsTheOneOfTheDocument(t *testing.T) {
	t.Parallel()
	type row struct {
		c    users.Capability
		want map[users.Level]bool
	}
	all := func(o, a, p, ob bool) map[users.Level]bool {
		return map[users.Level]bool{users.Owner: o, users.Administrator: a, users.Approver: p, users.Observer: ob}
	}
	for _, r := range []row{
		{users.Notifications, all(true, true, true, true)},
		{users.Status, all(true, true, true, true)},
		{users.TalkToGrandMaster, all(true, true, true, false)},
		{users.DecideApprovals, all(true, true, true, false)},
		{users.OneOffException, all(true, true, true, false)},
		{users.PermanentDecree, all(true, true, false, false)},
		{users.Pause, all(true, true, false, false)},
		{users.PauseButtons, all(true, true, true, false)},
		{users.RevokeTemporaryRules, all(true, true, false, false)},
		{users.Lock, all(true, true, false, false)},
		{users.Unlock, all(true, false, false, false)},
		{users.CreateWorkspace, all(true, true, false, false)},
		{users.RaiseCeiling, all(true, true, false, false)},
		{users.ManageUsers, all(true, false, false, false)},
		{users.ManageMachines, all(true, false, false, false)},
		{users.GitHubApps, all(true, false, false, false)},
		{users.ConnectOwnGitHub, all(true, true, true, false)},
		{users.TransferOwner, all(true, false, false, false)},
	} {
		for lvl, want := range r.want {
			if got := users.Can(lvl, r.c); got != want {
				t.Errorf("%s: %s can = %v, want %v", r.c, lvl, got, want)
			}
		}
	}
	if users.Can("sudo", users.Status) || users.Can(users.Owner, "invented") {
		t.Error("an unknown level or capability is allowed: it must fail closed")
	}
	for _, c := range users.Capabilities() { // the Owner can everything of the table
		if !users.Can(users.Owner, c) {
			t.Errorf("the Owner can not %s", c)
		}
	}
}

// CA-USR-02
func TestAnApproverCannotMakeAPermanentRuleAndIsToldWhichLevelIsNeeded(t *testing.T) {
	t.Parallel()
	err := users.Require(users.Approver, users.PermanentDecree)
	var le *users.LevelError
	if !errors.Is(err, users.ErrLevel) || !errors.As(err, &le) || le.Need != users.Administrator {
		t.Fatalf("Require = %v, want a refusal that needs an Administrator", err)
	}
	if !strings.Contains(err.Error(), "Administrador ou acima") {
		t.Errorf("the refusal does not say the level: %q", err.Error())
	}
	if err := users.Require(users.Owner, users.Unlock); err != nil {
		t.Errorf("the Owner is refused: %v", err)
	}
	if err := users.Require(users.Administrator, "invented"); err == nil {
		t.Error("an unknown capability is allowed")
	}
}

// Part of CA-USR-10: the capabilities behind the buttons and the command; the bot (the tap, the refusals with the level, the Observer) is in internal/telegram/stallchan_test.go.
func TestTheButtonsThatOnlyRestrictAreOfTheApproverAndTheOthersAreNot(t *testing.T) {
	t.Parallel()
	if !users.Can(users.Approver, users.PauseButtons) {
		t.Error("the Approver can not touch Pausar mesa / Pausar autonomia")
	}
	for _, c := range []users.Capability{users.Pause, users.RevokeTemporaryRules} { // /pausar and "Foi um erro"
		err := users.Require(users.Approver, c)
		var le *users.LevelError
		if !errors.As(err, &le) || le.Need != users.Administrator {
			t.Errorf("%s by an Approver = %v, want Administrator needed", c, err)
		}
	}
	for _, c := range []users.Capability{users.PauseButtons, users.Pause, users.RevokeTemporaryRules} {
		if users.Can(users.Observer, c) {
			t.Errorf("the Observer can %s", c)
		}
	}
}

func TestWhoRevokesWhichDecrees(t *testing.T) {
	t.Parallel()
	for lvl, want := range map[users.Level]users.RevokeScope{
		users.Owner: users.RevokeAny, users.Administrator: users.RevokeAdminAndApprover, users.Approver: users.RevokeOwnUnused, users.Observer: users.RevokeNone,
	} {
		if got := users.RevokeDecrees(lvl); got != want {
			t.Errorf("%s revokes %v, want %v", lvl, got, want)
		}
	}
}
