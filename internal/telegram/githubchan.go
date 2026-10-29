package telegram

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// PersonalGitHub is what the bot needs of the personal credentials of GitHub
// (*github.Personal): the device flow of the App "Grão-Mestre". The token never
// passes through the bot: it goes from GitHub to the vault.
type PersonalGitHub interface {
	Begin(ctx context.Context, userID string) (github.Challenge, error)
	Poll(ctx context.Context, userID string) (github.PollResult, error)
}

// GitHubChannel is /github over the bot (docs/github/aprovacao-no-github.md#credencial-pessoal-user-access-token-pelo-device-flow):
// it shows the user the code and the address of the device flow, and asks GitHub
// whether he authorized, as often as GitHub allows, until it is connected, he
// refuses or the code expires. Only a user who acts in a workspace connects.
type GitHubChannel struct {
	Users    *users.Service
	Personal PersonalGitHub
	Clock    clock.Clock
	Logger   *slog.Logger
	// Moment puts an instant in the time zone of the Guild (decrees.Service.InGuildZone);
	// nil: UTC.
	Moment func(ctx context.Context, t time.Time) time.Time

	bot  *Bot
	base context.Context

	mu       sync.Mutex
	watching map[string]*githubWatch // the connection each user waits for
	wg       sync.WaitGroup
}

// githubWatch is one wait for an authorization; the map holds the pointer, so a
// wait that ends removes only its own entry, never the one that replaced it.
type githubWatch struct{ cancel context.CancelFunc }

// Cancel ends the wait of a user for his authorization (his right to a personal
// credential ended: a code he was given must not connect him).
func (c *GitHubChannel) Cancel(userID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	w := c.watching[userID]
	delete(c.watching, userID)
	c.mu.Unlock()
	if w != nil {
		w.cancel()
	}
}

// Bind ties the channel to the bot; ctx ends the waits (the period of the Citadel).
func (c *GitHubChannel) Bind(bot *Bot, ctx context.Context) {
	c.mu.Lock()
	c.bot, c.base = bot, ctx
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
	c.mu.Unlock()
}

// moment is when the code expires, in the time zone of the Guild (Moment), or in
// UTC, saying so, when none is given: never the zone of the server.
func (c *GitHubChannel) moment(ctx context.Context, t time.Time) time.Time {
	if c.Moment != nil {
		return c.Moment(ctx, t)
	}
	return t.UTC()
}

// Commands is /github, for Options.Commands. It begins something, so the lock of
// the Mestre holds it.
func (c *GitHubChannel) Commands() []Command {
	return []Command{{Name: "github", Description: i18n.M("bot.cmd_github"), Capability: users.ConnectOwnGitHub, Run: c.begin}}
}

// Wait returns when no wait for an authorization is running (for the tests).
func (c *GitHubChannel) Wait() { c.wg.Wait() }

func (c *GitHubChannel) say(ctx context.Context, chat int64, userID string, m i18n.Msg) {
	c.mu.Lock()
	bot := c.bot
	c.mu.Unlock()
	if bot == nil {
		return
	}
	if _, err := bot.Send(ctx, Out{ChatID: chat, UserID: userID, Origin: "motor", Msg: m}); err != nil {
		c.Logger.Warn("telegram: a message about the GitHub connection was not queued", "user", userID, "err", bot.maskText(err.Error()))
	}
}

func (c *GitHubChannel) begin(ctx context.Context, call Call) (i18n.Msg, error) {
	u := call.User
	// the Owner acts in every workspace; the others in the ones of their list
	if u.Level != users.Owner && len(u.Workspaces) == 0 {
		return i18n.M("bot.github_no_workspace"), nil
	}
	ch, err := c.Personal.Begin(ctx, u.ID)
	if err != nil {
		return c.explain(err), nil
	}
	c.mu.Lock()
	base := c.base
	if base == nil || c.bot == nil {
		c.mu.Unlock()
		return i18n.M("bot.github_not_ready"), nil
	}
	if old := c.watching[u.ID]; old != nil {
		old.cancel() // a new code replaces the one he was waiting for
	}
	wctx, cancel := context.WithCancel(base)
	if c.watching == nil {
		c.watching = map[string]*githubWatch{}
	}
	w := &githubWatch{cancel: cancel}
	c.watching[u.ID] = w
	c.wg.Add(1)
	c.mu.Unlock()
	go c.watch(wctx, w, u.ID, call.ChatID, ch)
	return i18n.M("bot.github_connect", "uri", ch.VerificationURI, "code", ch.UserCode, "until", c.moment(ctx, ch.ExpiresAt)), nil
}

// watch asks GitHub at the pace it allows, until the end of the connection.
func (c *GitHubChannel) watch(ctx context.Context, w *githubWatch, userID string, chat int64, ch github.Challenge) {
	defer c.wg.Done()
	defer func() {
		w.cancel()
		c.mu.Lock()
		if c.watching[userID] == w { // not the entry of the wait that replaced this one
			delete(c.watching, userID)
		}
		c.mu.Unlock()
	}()
	interval := ch.Interval
	if interval <= 0 {
		interval = DefaultGitHubPoll
	}
	t := c.Clock.NewTimer(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
		}
		res, err := c.Personal.Poll(ctx, userID)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			c.say(ctx, chat, userID, c.explain(err))
			return
		case res == github.PollConnected:
			c.say(ctx, chat, userID, i18n.M("bot.github_connected"))
			return
		}
		t.Reset(interval)
	}
}

// DefaultGitHubPoll is the pace of the poll when GitHub did not say one.
const DefaultGitHubPoll = 5 * time.Second

// explain is the answer for each refusal, in plain words; what GitHub said is
// never repeated.
func (c *GitHubChannel) explain(err error) i18n.Msg {
	switch {
	case errors.Is(err, github.ErrNoCredential):
		return i18n.M("bot.github_no_credential")
	case errors.Is(err, github.ErrNoGitHubLogin):
		return i18n.M("bot.github_no_login")
	case errors.Is(err, github.ErrDeviceFlowOff):
		return i18n.M("bot.github_device_flow_off")
	case errors.Is(err, github.ErrAppNotReady):
		return i18n.M("bot.github_app_not_ready")
	case errors.Is(err, github.ErrDeviceDenied):
		return i18n.M("bot.github_denied")
	case errors.Is(err, github.ErrDeviceExpired):
		return i18n.M("bot.github_expired")
	case errors.Is(err, github.ErrLoginMismatch):
		return i18n.M("bot.github_mismatch")
	case errors.Is(err, github.ErrNoClientSecret):
		return i18n.M("bot.github_no_secret")
	}
	c.Logger.Warn("telegram: the GitHub connection failed", "err", err)
	return i18n.M("bot.github_failed")
}
