package telegram

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lucasndas/mestre/internal/blocking"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/users"
)

// ChooseAction is the button of an option of a question of the Grão-Mestre; its
// argument is "<question>:<option>" (the option from 0).
const ChooseAction = "choose"

// MaxOptions and MinOptions are how many options a question has.
const (
	MinOptions = 2
	MaxOptions = 6
)

// Answered is told the answer of a user to a question: the engine puts it in the
// inbox of the Grão-Mestre as a message of that user.
type Answered func(ctx context.Context, q ChoiceQuestion, by users.User, option string) error

// ChoiceQuestion is a question with options for a user.
type ChoiceQuestion struct {
	ID              int64
	Workspace, Mesa string
	Asker, UserID   string
	Question        string
	Options         []string
}

// ChooseChannel asks a user a question with a button for each option and brings
// the answer back (docs/cli/grao-mestre.md#mestre-choose). Only the click of the
// user asked counts, and only once; nothing is passed on before it.
type ChooseChannel struct {
	Users    *users.Service
	Answered Answered

	bot *Bot
}

// Bind ties the channel to the bot.
func (c *ChooseChannel) Bind(bot *Bot) { c.bot = bot }

// Actions are the buttons, for Options.Actions.
func (c *ChooseChannel) Actions() map[string]Action {
	return map[string]Action{ChooseAction: c.click}
}

// Ask keeps the question and sends it to the user, with the recommended option
// (from 1; 0 or less is the first) shown first. It returns the number of the
// question.
func (c *ChooseChannel) Ask(ctx context.Context, q ChoiceQuestion, recommend int) (int64, error) {
	if c.bot == nil {
		return 0, errors.New("the bot of the Telegram is not running")
	}
	if n := len(q.Options); n < MinOptions || n > MaxOptions {
		return 0, fmt.Errorf("a question has %d to %d options, not %d", MinOptions, MaxOptions, n)
	}
	u, err := c.Users.Get(ctx, q.UserID)
	if err != nil {
		return 0, err
	}
	if !users.Can(u.Level, users.TalkToGrandMaster) {
		return 0, blocking.ErrNoLevel
	}
	if u.State != users.Active || u.TelegramID == 0 || !u.Acts(q.Workspace) {
		return 0, blocking.ErrNotReachable
	}
	order := make([]int, 0, len(q.Options)) // the indexes of the options, the recommended first
	if recommend < 1 || recommend > len(q.Options) {
		recommend = 1
	}
	order = append(order, recommend-1)
	for i := range q.Options {
		if i != recommend-1 {
			order = append(order, i)
		}
	}
	body, err := json.Marshal(q.Options)
	if err != nil {
		return 0, err
	}
	var id int64
	err = c.bot.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO choose_questions (workspace, mesa, asker, user_id, question, options, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			q.Workspace, q.Mesa, q.Asker, u.ID, q.Question, string(body), ms(c.bot.clk.Now()))
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return 0, err
	}
	var rows [][]Button
	for _, i := range order {
		rows = append(rows, []Button{{Text: q.Options[i], Action: ChooseAction, Arg: strconv.FormatInt(id, 10) + ":" + strconv.Itoa(i)}})
	}
	text := fmt.Sprintf("%s\n(pergunta %d do Grão-Mestre)", strings.TrimSpace(q.Question), id)
	if _, err := c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Text: text, Workspace: q.Workspace, Mesa: q.Mesa, Origin: "escolha", Buttons: rows}); err != nil {
		_ = c.bot.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM choose_questions WHERE id = ? AND state = 'open'`, id)
			return err
		})
		return 0, err
	}
	return id, nil
}

// click takes the answer of the user asked, once.
func (c *ChooseChannel) click(ctx context.Context, k Click) (string, string, error) {
	qid, opt, ok := strings.Cut(k.Arg, ":")
	id, err1 := strconv.ParseInt(qid, 10, 64)
	idx, err2 := strconv.Atoi(opt)
	if !ok || err1 != nil || err2 != nil {
		return "", "", errors.New("botão inválido")
	}
	var q ChoiceQuestion
	var options, state string
	err := c.bot.o.DB.Reader().QueryRowContext(ctx, `SELECT workspace, mesa, asker, user_id, question, options, state FROM choose_questions WHERE id = ?`, id).
		Scan(&q.Workspace, &q.Mesa, &q.Asker, &q.UserID, &q.Question, &options, &state)
	if err != nil {
		return "", "", errors.New("essa pergunta não existe mais")
	}
	if q.UserID != k.User.ID {
		return "", "", errors.New("essa pergunta não é sua")
	}
	if err := json.Unmarshal([]byte(options), &q.Options); err != nil || idx < 0 || idx >= len(q.Options) {
		return "", "", errors.New("botão inválido")
	}
	u, err := c.Users.Get(ctx, k.User.ID) // the workspaces as stored now
	if err != nil || u.State != users.Active || !u.Acts(q.Workspace) {
		return "", "", errors.New("essa pergunta não é de um workspace seu")
	}
	q.ID = id
	var claimed bool
	if err := c.bot.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE choose_questions SET state = 'answered', answer = ?, answered_at = ? WHERE id = ? AND state = 'open'`, q.Options[idx], ms(c.bot.clk.Now()), id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		claimed = n > 0
		return nil
	}); err != nil {
		return "", "", err
	}
	if !claimed {
		return "Essa pergunta já foi respondida.", "", nil
	}
	if err := c.Answered(ctx, q, u, q.Options[idx]); err != nil {
		// the answer is not lost to a failure of the inbox: the question is open again
		_ = c.bot.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE choose_questions SET state = 'open', answer = '', answered_at = 0 WHERE id = ? AND state = 'answered'`, id)
			return err
		})
		return "", "", errors.New("não consegui entregar a resposta ao Grão-Mestre; toque de novo")
	}
	return "Anotado: " + q.Options[idx], fmt.Sprintf("%s\nResposta: %s", strings.TrimSpace(q.Question), q.Options[idx]), nil
}

// AnswerItem is the message the answer becomes in the inbox of the Grão-Mestre: a
// message of the user who clicked, with the option and the number of the question.
func AnswerItem(q ChoiceQuestion, by users.User, option string) inbox.NewItem {
	return inbox.NewItem{Workspace: q.Workspace, Kind: inbox.KindUser, Sender: by.Name,
		Origin: &inbox.Origin{Channel: inbox.ChannelTelegram, User: by.ID, UserName: by.Name, Level: characters.UserLevel(by.Level), Table: q.Mesa, Choice: q.ID},
		Text:   fmt.Sprintf("Escolha %q (pergunta %d: %s)", option, q.ID, strings.Join(strings.Fields(q.Question), " "))}
}
