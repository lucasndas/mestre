package telegram_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/telegram"
)

// mismatchPersonal is the personal credential of GitHub when the account that
// authorized is not the one registered for the user: the token is discarded in
// internal/github and Poll answers ErrLoginMismatch.
type mismatchPersonal struct{ now time.Time }

func (m mismatchPersonal) Begin(context.Context, string) (github.Challenge, error) {
	return github.Challenge{UserCode: "WDJB-MJHT", VerificationURI: "https://github.com/login/device", ExpiresAt: m.now.Add(15 * time.Minute), Interval: 5 * time.Second}, nil
}

func (mismatchPersonal) Poll(context.Context, string) (github.PollResult, error) {
	return github.PollPending, github.ErrLoginMismatch
}

// CA-GH-APROV-06, the half that shows the error: when the login that authorized
// is not the registered one (the token is discarded in internal/github, see
// personal_test.go), the user sees the error where he asked for the connection,
// and is never told that the GitHub was connected.
func TestAMismatchOfTheLoginIsShownToTheUserWhereHeAskedForTheConnection(t *testing.T) {
	t.Parallel()
	gc := &telegram.GitHubChannel{Personal: mismatchPersonal{now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}}
	w := newWorld(t, rigOpts{commands: gc.Commands()})
	gc.Clock = w.clk
	gc.Bind(w.bot, w.ctx)
	owner := w.pairOwnerTelegram()
	w.say(owner, "/github")
	w.clk.BlockUntil(1) // the wait for the authorization armed its timer
	w.clk.Advance(5 * time.Second)
	gc.Wait()
	w.drain()
	var texts []string
	for _, s := range w.sent("sendMessage") {
		texts = append(texts, s.Text)
	}
	all := strings.Join(texts, "\n---\n")
	if !strings.Contains(all, "WDJB-MJHT") {
		t.Fatalf("the user was not given the code:\n%s", all)
	}
	if !strings.Contains(all, "não é a cadastrada para você") || !strings.Contains(all, "Nada foi guardado") {
		t.Errorf("the mismatch was not shown:\n%s", all)
	}
	if strings.Contains(all, "GitHub conectado") {
		t.Errorf("the user was told it connected:\n%s", all)
	}
}
