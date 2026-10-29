package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/mestre-ia/mestre/internal/i18n"

	"github.com/mestre-ia/mestre/internal/blocking"
	"github.com/mestre-ia/mestre/internal/notices"
	"github.com/mestre-ia/mestre/internal/users"
)

// BlockDetailsAction is the button "Ver detalhes" of a message that blocks; its
// argument is the number of the notice.
const BlockDetailsAction = "block_details"

// BlockChannel sends the messages that ask something of the users because a table
// can not go on without it (docs/aprovacoes/pedido-que-bloqueia.md): to the Owner
// and to the Administrators of the workspace, each with the buttons his level
// allows. The whole text, masked, is kept for "Ver detalhes" and for the panel.
type BlockChannel struct {
	Users *users.Service
	// PanelURL is where "Abrir no painel" goes; empty: the message says the step
	// is done in the panel and has no link.
	PanelURL string
	// Panel is the feed of the bell of the panel: what is sent here is kept there too
	// (nil: only the Telegram). The panel reads the notices that block from
	// blocking_notices, which Notify always writes.
	Panel *notices.Feed

	bot *Bot
}

// Bind ties the channel to the bot.
func (c *BlockChannel) Bind(bot *Bot) { c.bot = bot }

// Actions are the buttons, for Options.Actions.
func (c *BlockChannel) Actions() map[string]Action {
	return map[string]Action{BlockDetailsAction: c.details}
}

// Notify keeps the notice and sends it to the users who act on it, each message
// built for his level. It returns the number of the notice and how many users it
// was sent to.
func (c *BlockChannel) Notify(ctx context.Context, n blocking.Notice) (id int64, sent int, err error) {
	if c.bot == nil {
		return 0, 0, errors.New("the bot of the Telegram is not running")
	}
	if _, err := blocking.ParseMotive(string(n.Motive)); err != nil {
		return 0, 0, err
	}
	for _, e := range n.BlockedBy {
		if _, err := strconv.Atoi(e); err != nil {
			return 0, 0, fmt.Errorf("blocked-by takes numbers of epics, not %q", e)
		}
	}
	mask := c.bot.maskText // looks through the marks that could hide a secret
	summary := n.Stopped
	if summary == "" {
		summary = string(n.Motive)
	}
	id, err = (&blocking.Store{DB: c.bot.o.DB, Clock: c.bot.clk}).Save(ctx, n, mask)
	if err != nil {
		return 0, 0, err
	}
	if c.Panel != nil {
		text := i18n.Text(n.Motive.Icon() + " " + blocking.Line(summary, mask))
		if _, seen, err := c.Panel.Add(ctx, notices.Notice{Kind: notices.Blocked, Workspace: n.Workspace, Mesa: n.Mesa, Audience: notices.Admins, Msg: text,
			LinkKind: notices.LinkBlocking, LinkRef: strconv.FormatInt(id, 10)}); err != nil {
			c.bot.log.Warn("telegram: a notice was not kept for the panel", "err", err)
		} else {
			blocking.Stored(ctx, seen)
		}
	}
	all, err := c.Users.List(ctx)
	if err != nil {
		return id, 0, err
	}
	for _, u := range all {
		if u.State != users.Active || u.TelegramID == 0 || !u.Acts(n.Workspace) || (u.Level != users.Owner && u.Level != users.Administrator) {
			continue
		}
		m := blocking.Build(n, blocking.Viewer{Level: u.Level}, mask)
		var rows [][]Button
		for _, b := range m.Buttons {
			switch {
			case b.Panel:
				if panelURL(c.PanelURL) {
					rows = append(rows, []Button{{Msg: b.Text, URL: c.PanelURL}})
				}
			case b.Details:
				rows = append(rows, []Button{{Msg: b.Text, Action: BlockDetailsAction, Arg: strconv.FormatInt(id, 10)}})
			}
		}
		if _, err := c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Msg: m.Msg, Workspace: n.Workspace, Mesa: n.Mesa, Origin: "bloqueio", Buttons: rows}); err != nil {
			c.bot.log.Warn("telegram: a notice was not queued", "user", u.ID, "err", err)
			continue
		}
		sent++
	}
	return id, sent, nil
}

// details sends the whole text of the notice, masked, to a user who acts in its
// workspace.
func (c *BlockChannel) details(ctx context.Context, k Click) (i18n.Msg, i18n.Msg, error) {
	id, err := strconv.ParseInt(k.Arg, 10, 64)
	if err != nil {
		return i18n.Msg{}, i18n.Msg{}, errInvalidButton
	}
	saved, err := (&blocking.Store{DB: c.bot.o.DB, Clock: c.bot.clk}).Get(ctx, id)
	if err != nil {
		return i18n.Msg{}, i18n.Msg{}, i18n.Err("bot.notice_gone")
	}
	u, err := c.Users.Get(ctx, k.User.ID) // the workspaces as stored now
	if err != nil || !u.Acts(saved.Workspace) {
		return i18n.Msg{}, i18n.Msg{}, i18n.Err("bot.notice_not_workspace")
	}
	// the frame is said in the language of who reads; a notice from before the messages is shown as written
	full := saved.Msg.Full
	if full.IsZero() {
		full = i18n.Text(saved.Full)
	}
	if _, err := c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Msg: i18n.M("bot.notice_details", "id", id, "text", full), Workspace: saved.Workspace, Mesa: saved.Mesa, Origin: "bloqueio"}); err != nil {
		return i18n.Msg{}, i18n.Msg{}, err
	}
	return i18n.M("bot.details_sent"), i18n.Msg{}, nil
}

// panelURL says the link of the panel is one the Telegram takes in a button: a
// plain http(s) URL with a host and no credentials. A bad one costs the button,
// never the notice.
func panelURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}

// maxSay is the longest message of the Grão-Mestre that is sent, in characters.
const maxSay = 1500

// Say tells the users of the workspace a plain message of the Grão-Mestre
// (`mestre notify "<message>"`): to who is active, has the Telegram, acts in the
// workspace and has the level to be notified. The text is the text of an agent: it
// is masked, cut to one safe line per line of the message and sent under a title of
// the engine that says so. It returns how many were told.
func (c *BlockChannel) Say(ctx context.Context, workspace, mesa, text string) (int, error) {
	if c.bot == nil {
		return 0, errors.New("the bot of the Telegram is not running")
	}
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if l = blocking.Line(l, c.bot.maskText); strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	// each line is quoted, so no line of the message imitates a line of the engine
	for i := range lines {
		lines[i] = "| " + lines[i]
	}
	body := strings.Join(lines, "\n")
	if r := []rune(body); len(r) > maxSay {
		body = string(r[:maxSay]) + "…"
	}
	if body == "" {
		return 0, errors.New("the message is empty")
	}
	all, err := c.Users.List(ctx)
	if err != nil {
		return 0, err
	}
	if c.Panel != nil {
		n := notices.Notice{Kind: notices.Notify, Workspace: workspace, Mesa: mesa, Audience: notices.Workspace, Msg: i18n.M("bot.gm_notice", "body", body)}
		if mesa != "" {
			n.LinkKind, n.LinkRef = notices.LinkTable, mesa
		}
		if _, seen, err := c.Panel.Add(ctx, n); err != nil {
			c.bot.log.Warn("telegram: a notice was not kept for the panel", "err", err)
		} else {
			blocking.Stored(ctx, seen)
		}
	}
	sent := 0
	for _, u := range all {
		if u.State != users.Active || u.TelegramID == 0 || !u.Acts(workspace) || !users.Can(u.Level, users.Notifications) {
			continue
		}
		out := Out{ChatID: u.TelegramID, UserID: u.ID, Msg: i18n.M("bot.gm_notice", "body", body), Workspace: workspace, Mesa: mesa, Origin: "aviso"}
		if _, err := c.bot.Send(ctx, out); err != nil {
			c.bot.log.Warn("telegram: a notice was not queued", "user", u.ID, "err", err)
			continue
		}
		sent++
	}
	return sent, nil
}
