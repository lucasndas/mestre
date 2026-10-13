package gitx_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/logging"
)

const token = "ghs_16C7e42F292c6912E7710c838347Ae178B4a"

// A credential in a URL would be in the arguments of git, readable by every
// local account, and Clone would keep it in remote.origin.url.
func TestCredentialsInURLsAreRefused(t *testing.T) {
	t.Parallel()
	for _, u := range []string{
		"https://x-access-token:" + token + "@github.com/o/r.git",
		"https://" + token + "@github.com/o/r.git",
		"ssh://git@github.com/o/r.git",
	} {
		err := gitx.CheckURL(u)
		if !errors.Is(err, gitx.ErrCredentialsInURL) {
			t.Errorf("CheckURL(%s) = %v, want ErrCredentialsInURL", gitx.RedactURL(u), err)
		}
		if err != nil && strings.Contains(err.Error(), token) {
			t.Errorf("error carries the token: %v", err)
		}
	}
	for _, ok := range []string{"https://github.com/o/r.git", "/srv/espelho.git", `C:\mestre\espelho.git`, "git@github.com:o/r.git", "https://h/a@b"} {
		if err := gitx.CheckURL(ok); err != nil {
			t.Errorf("CheckURL(%q) = %v", ok, err)
		}
	}
	if err := gitx.CheckConfig("remote.origin.url", "https://u:p@h/x"); !errors.Is(err, gitx.ErrCredentialsInURL) {
		t.Errorf("CheckConfig(remote.origin.url) = %v", err)
	}
	if err := gitx.CheckConfig("remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
		t.Errorf("CheckConfig(remote.origin.fetch) = %v", err)
	}

	// The real Clone refuses before starting any process.
	g := newGit(t, gitx.Options{Suite: "fast"})
	_, err := g.Clone(context.Background(), "https://x-access-token:"+token+"@github.com/o/r.git", filepath.Join(t.TempDir(), "r.git"), gitx.CloneOptions{Bare: true})
	if !errors.Is(err, gitx.ErrCredentialsInURL) || strings.Contains(err.Error(), token) {
		t.Fatalf("Clone = %v, want ErrCredentialsInURL without the token", err)
	}
}

// Auth reaches git only through its environment (GIT_CONFIG_COUNT/KEY/VALUE),
// is never printed and is registered in the masker.
func TestAuthGoesThroughTheEnvironment(t *testing.T) {
	t.Parallel()
	a := gitx.BasicAuth("https://github.com/", "x-access-token", token)
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	env := gitx.AuthEnv(a)
	want := []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://github.com/.extraHeader", "GIT_CONFIG_VALUE_0=" + a.Header}
	if !slices.Equal(env, want) {
		t.Errorf("env = %q, want %q", env, want)
	}
	if !strings.HasPrefix(a.Header, "Authorization: Basic ") || strings.Contains(a.Header, token) {
		t.Errorf("header = %q, want Basic with the token encoded", a.Header)
	}
	for _, s := range []string{fmt.Sprint(a), fmt.Sprintf("%v %+v %#v %s", a, a, a, a), fmt.Sprint(&a)} {
		if strings.Contains(s, a.Header) || strings.Contains(s, "Basic") {
			t.Errorf("printing the auth shows it: %q", s)
		}
	}

	m := logging.NewMasker()
	g := newGit(t, gitx.Options{Suite: "fast", Masker: m})
	_, err := g.Clone(context.Background(), "https://github.com/o/r.git", filepath.Join(t.TempDir(), "r.git"), gitx.CloneOptions{Bare: true, Auth: &a})
	if !errors.Is(err, gitx.ErrExternalProcessInFastSuite) {
		t.Fatalf("Clone = %v", err)
	}
	if got := m.Mask("token " + token + " in a log"); strings.Contains(got, token) {
		t.Errorf("token not registered in the masker: %q", got)
	}

	for _, bad := range []gitx.Auth{
		{URL: "https://u:p@github.com/", Header: "Authorization: Basic eA=="},
		{URL: "ftp://github.com/", Header: "Authorization: Basic eA=="},
		{URL: "https://github.com/?x=1", Header: "Authorization: Basic eA=="},
		{URL: "https://github.com/", Header: "Authorization: Basic eA==\r\nX-Evil: 1"},
		{URL: "https://github.com/", Header: "no colon"},
		{URL: "https://github.com/", Header: "Authorization:   "},
	} {
		if err := bad.Validate(); !errors.Is(err, gitx.ErrInvalidArgument) {
			t.Errorf("Validate(%s) = %v, want ErrInvalidArgument", bad.URL, err)
		}
	}
}

// Every git call overrides on its command line the settings with which the
// configuration of a repository could make git run a command as the engine.
func TestGlobalArgsNeutralizeCommandRunningSettings(t *testing.T) {
	t.Parallel()
	args := gitx.GlobalArgs()
	set := map[string]string{}
	for i := 0; i+1 < len(args); i += 2 {
		if args[i] != "-c" {
			t.Fatalf("argument %d = %q, want -c", i, args[i])
		}
		k, v, _ := strings.Cut(args[i+1], "=")
		set[strings.ToLower(k)] = v
	}
	for k, want := range map[string]string{
		"core.fsmonitor": "false", "core.sshcommand": "ssh", "core.askpass": "", "credential.helper": "",
		"log.showsignature": "false", "merge.verifysignatures": "false", "commit.gpgsign": "false",
		"protocol.ext.allow": "never", "protocol.git.allow": "never", "submodule.recurse": "false",
		"fetch.recursesubmodules": "false", "http.sslverify": "true",
	} {
		if got, ok := set[k]; !ok || got != want {
			t.Errorf("-c %s = %q (set %v), want %q", k, got, ok, want)
		}
	}
	if _, ok := set["core.hookspath"]; !ok {
		t.Error("core.hooksPath is not overridden")
	}
}
