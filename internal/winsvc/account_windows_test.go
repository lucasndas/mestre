package winsvc

import (
	"errors"
	"testing"
)

// CA-TERM-15
// The "agent account" of a launcher that is not a service is the account of the
// process itself, for the tests. In a service it would be LocalSystem, and the
// agents would run as SYSTEM: the account refuses to log on, and the launcher
// refuses to be built over it, instead of starting the terminals as the service.
func TestTheAccountOfTheLauncherItselfIsRefusedInAService(t *testing.T) {
	t.Parallel()
	inService := SelfAccount{service: func() bool { return true }}
	if _, err := inService.Logon("t1"); !errors.Is(err, ErrSelfAccountInService) {
		t.Errorf("Logon in a service = %v, want ErrSelfAccountInService", err)
	}
	if _, err := inService.Adopt("t1", 1); !errors.Is(err, ErrSelfAccountInService) {
		t.Errorf("Adopt in a service = %v, want ErrSelfAccountInService", err)
	}
	if _, err := NewLauncher(LauncherConfig{Account: inService}); !errors.Is(err, ErrSelfAccountInService) {
		t.Errorf("NewLauncher over it in a service = %v, want ErrSelfAccountInService", err)
	}

	// not a service, as in the tests: the token of the process, released once
	var released int
	outside := SelfAccount{service: func() bool { return false }, Released: func(string) { released++ }}
	s, err := outside.Logon("t1")
	if err != nil {
		t.Fatalf("Logon outside a service = %v", err)
	}
	s.Release()
	s.Release()
	if released != 1 {
		t.Errorf("the session was released %d times, want once", released)
	}
}
