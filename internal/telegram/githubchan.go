package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

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
	// Moment writes an instant in the time zone of the Guild (decrees.Service.Moment);
	// nil: UTC.
	Moment func(ctx context.Context, t time.Time) string

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
func (c *GitHubChannel) moment(ctx context.Context, t time.Time) string {
	if c.Moment != nil {
		return c.Moment(ctx, t)
	}
	return t.UTC().Format("02/01/2006 15:04") + " UTC"
}

// Commands is /github, for Options.Commands. It begins something, so the lock of
// the Mestre holds it.
func (c *GitHubChannel) Commands() []Command {
	return []Command{{Name: "github", Description: "conecta o seu GitHub (credencial pessoal): mostra um código para digitar no GitHub", Capability: users.ConnectOwnGitHub, Run: c.begin}}
}

// Wait returns when no wait for an authorization is running (for the tests).
func (c *GitHubChannel) Wait() { c.wg.Wait() }

func (c *GitHubChannel) say(ctx context.Context, chat int64, userID, text string) {
	c.mu.Lock()
	bot := c.bot
	c.mu.Unlock()
	if bot == nil {
		return
	}
	if _, err := bot.Send(ctx, Out{ChatID: chat, UserID: userID, Origin: "motor", Text: text}); err != nil {
		c.Logger.Warn("telegram: a message about the GitHub connection was not queued", "user", userID, "err", bot.maskText(err.Error()))
	}
}

func (c *GitHubChannel) begin(ctx context.Context, call Call) (string, error) {
	u := call.User
	// the Owner acts in every workspace; the others in the ones of their list
	if u.Level != users.Owner && len(u.Workspaces) == 0 {
		return "Você ainda não atua em nenhuma campanha: peça a um Administrador. O GitHub pessoal é usado só nas campanhas em que você atua.", nil
	}
	ch, err := c.Personal.Begin(ctx, u.ID)
	if err != nil {
		return c.explain(err), nil
	}
	c.mu.Lock()
	base := c.base
	if base == nil || c.bot == nil {
		c.mu.Unlock()
		return "O bot ainda não está pronto; tente de novo em instantes.", nil
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
	return fmt.Sprintf("Para conectar o seu GitHub, abra %s e digite o código %s (vale até %s). Entre com a conta cadastrada para você. Eu aviso aqui quando a conexão estiver feita; o token vai direto para o cofre e não passa por esta conversa.",
		ch.VerificationURI, ch.UserCode, c.moment(ctx, ch.ExpiresAt)), nil
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
			c.say(ctx, chat, userID, "GitHub conectado. O Mestre passa a usar a sua credencial pessoal onde ela vale; você pode desconectar quando quiser.")
			return
		}
		t.Reset(interval)
	}
}

// DefaultGitHubPoll is the pace of the poll when GitHub did not say one.
const DefaultGitHubPoll = 5 * time.Second

// explain is the answer for each refusal, in plain words; what GitHub said is
// never repeated.
func (c *GitHubChannel) explain(err error) string {
	switch {
	case errors.Is(err, github.ErrNoCredential):
		return "O seu cadastro não pode conectar o GitHub agora (usuário suspenso, removido ou Observador)."
	case errors.Is(err, github.ErrNoGitHubLogin):
		return "O seu cadastro não tem login do GitHub: peça a um Administrador para preencher e tente de novo com /github."
	case errors.Is(err, github.ErrDeviceFlowOff):
		return "O App Grão-Mestre está com o device flow desligado no GitHub: o Senhor da Guilda precisa ligar a opção Enable Device Flow nas configurações do App."
	case errors.Is(err, github.ErrAppNotReady):
		return "O App Grão-Mestre ainda não está pronto: o Senhor da Guilda precisa terminar a criação dele."
	case errors.Is(err, github.ErrDeviceDenied):
		return "A autorização foi recusada no GitHub. Se foi engano, envie /github de novo."
	case errors.Is(err, github.ErrDeviceExpired):
		return "O código expirou antes da autorização. Envie /github para pedir outro."
	case errors.Is(err, github.ErrLoginMismatch):
		return "A conta do GitHub que autorizou não é a cadastrada para você. Nada foi guardado; entre com a conta certa e envie /github de novo."
	case errors.Is(err, github.ErrNoClientSecret):
		return "O segredo do App Grão-Mestre não está no cofre: o Senhor da Guilda precisa recriar o App (veja o painel)."
	}
	c.Logger.Warn("telegram: the GitHub connection failed", "err", err)
	return "Não consegui conectar o GitHub agora. Tente de novo em alguns minutos; o Senhor da Guilda recebe o detalhe no log."
}
