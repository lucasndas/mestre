package winsvc

import (
	"errors"
	"testing"
)

// CA-TERM-15
// The identity of a service is judged by the SID, not only by who started the
// process: a LocalSystem process that a tool started (psexec -s, a task as
// SYSTEM) has no service manager for a parent, and would start the agents as
// SYSTEM all the same. The ones of a person, of an administrator, or of a group
// are not.
func TestTheIdentitiesOfAServiceAreTheOnesThatOnlyAServiceRunsAs(t *testing.T) {
	t.Parallel()
	for sid, want := range map[string]bool{
		"S-1-5-18": true, "S-1-5-19": true, "S-1-5-20": true,
		"S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464": true,  // NT SERVICE\TrustedInstaller
		"S-1-5-21-1004336348-1177238915-682003330-500":                   false, // Administrator
		"S-1-5-21-1004336348-1177238915-682003330-1001":                  false, // a user
		"S-1-5-32-544": false, // Administrators
		"S-1-1-0":      false, // Everyone
		"S-1-5-8":      false, // Proxy
		"S-1-5-180":    false, // not S-1-5-18
		"S-1-5-8-0":    false,
	} {
		if got := isServiceSID(sid); got != want {
			t.Errorf("isServiceSID(%s) = %v, want %v", sid, got, want)
		}
	}
}

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
	for name, account := range map[string]Account{"by value": inService, "by pointer": &inService} {
		if _, err := NewLauncher(LauncherConfig{Account: account}); !errors.Is(err, ErrSelfAccountInService) {
			t.Errorf("NewLauncher over it in a service, %s = %v, want ErrSelfAccountInService", name, err)
		}
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
