package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/lucasndas/mestre/internal/blocking"
	"github.com/lucasndas/mestre/internal/users"
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
	full := blocking.Full(n, mask)
	summary := n.Stopped
	if summary == "" {
		summary = string(n.Motive)
	}
	err = c.bot.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO blocking_notices (workspace, mesa, motive, summary, full, machine, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			n.Workspace, n.Mesa, string(n.Motive), blocking.Line(summary, mask), full, n.MachineLine(), ms(c.bot.clk.Now()))
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return 0, 0, err
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
					rows = append(rows, []Button{{Text: b.Text, URL: c.PanelURL}})
				}
			case b.Details:
				rows = append(rows, []Button{{Text: b.Text, Action: BlockDetailsAction, Arg: strconv.FormatInt(id, 10)}})
			}
		}
		if _, err := c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Text: m.Text, Workspace: n.Workspace, Mesa: n.Mesa, Origin: "bloqueio", Buttons: rows}); err != nil {
			c.bot.log.Warn("telegram: a notice was not queued", "user", u.ID, "err", err)
			continue
		}
		sent++
	}
	return id, sent, nil
}

// details sends the whole text of the notice, masked, to a user who acts in its
// workspace.
func (c *BlockChannel) details(ctx context.Context, k Click) (string, string, error) {
	id, err := strconv.ParseInt(k.Arg, 10, 64)
	if err != nil {
		return "", "", errors.New("botão inválido")
	}
	var ws, mesa, full string
	err = c.bot.o.DB.Reader().QueryRowContext(ctx, `SELECT workspace, mesa, full FROM blocking_notices WHERE id = ?`, id).Scan(&ws, &mesa, &full)
	if err != nil {
		return "", "", errors.New("esse aviso não existe mais")
	}
	u, err := c.Users.Get(ctx, k.User.ID) // the workspaces as stored now
	if err != nil || !u.Acts(ws) {
		return "", "", errors.New("esse aviso não é de um workspace seu")
	}
	if _, err := c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Text: fmt.Sprintf("Detalhes do aviso %d:\n%s", id, full), Workspace: ws, Mesa: mesa, Origin: "bloqueio"}); err != nil {
		return "", "", err
	}
	return "Enviei os detalhes.", "", nil
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
	sent := 0
	for _, u := range all {
		if u.State != users.Active || u.TelegramID == 0 || !u.Acts(workspace) || !users.Can(u.Level, users.Notifications) {
			continue
		}
		out := Out{ChatID: u.TelegramID, UserID: u.ID, Text: "Aviso do Grão-Mestre (texto do agente, não verificado):\n" + body, Workspace: workspace, Mesa: mesa, Origin: "aviso"}
		if _, err := c.bot.Send(ctx, out); err != nil {
			c.bot.log.Warn("telegram: a notice was not queued", "user", u.ID, "err", err)
			continue
		}
		sent++
	}
	return sent, nil
}
