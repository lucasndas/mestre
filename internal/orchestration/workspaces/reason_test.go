package workspaces

import (
	"errors"
	"strings"
	"testing"
)

// The reason of a failure goes to the log and to the Administrators: a URL of git
// with credentials loses them.
func TestTheReasonOfAFailureHidesTheCredentialsOfAURL(t *testing.T) {
	t.Parallel()
	got := reasonOf(errors.New("fatal: unable to access 'https://x-access-token:s3cr3t@github.com/lucas/app.git/': Could not resolve host; ssh://git:pw@host/x"))
	if strings.Contains(got, "s3cr3t") || strings.Contains(got, "pw@") || !strings.Contains(got, "https://***@github.com/lucas/app.git") {
		t.Errorf("reason = %q", got)
	}
	if plain := reasonOf(errors.New("fatal: no such file https://github.com/lucas/app.git")); plain != "fatal: no such file https://github.com/lucas/app.git" {
		t.Errorf("a URL without credentials changed: %q", plain)
	}
}
