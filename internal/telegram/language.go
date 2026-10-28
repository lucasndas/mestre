package telegram

import (
	"context"
	"errors"
	"slices"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/users"
)

// LanguageAction is the button of /idioma; its argument is the language ("pt-BR" or "en").
const LanguageAction = "language"

// LanguageChannel is /idioma and /language (docs/plataforma/idioma-por-canal.md#telegram):
// it says the language in use and gives a button for each language of the panel. The touch
// writes the language of the user who touched it, as the selector of the panel does, and
// answers already in the new language.
type LanguageChannel struct {
	Users *users.Service

	bot *Bot
}

// Bind ties the channel to the bot.
func (c *LanguageChannel) Bind(bot *Bot) { c.bot = bot }

// Commands are /idioma and /language, for Options.Commands. They only change the preference
// of the one who asks, so they are answered while the Mestre is locked.
func (c *LanguageChannel) Commands() []Command {
	return []Command{
		{Name: "idioma", Description: i18n.M("bot.cmd_language"), Query: true, Run: c.ask},
		{Name: "language", Description: i18n.M("bot.cmd_language"), Query: true, Run: c.ask},
	}
}

// Actions are the buttons, for Options.Actions.
func (c *LanguageChannel) Actions() map[string]Action {
	return map[string]Action{LanguageAction: c.click}
}

// languageName is the name of a language in the language itself.
func languageName(lang string) i18n.Msg {
	if lang == i18n.En {
		return i18n.M("bot.language_en")
	}
	return i18n.M("bot.language_pt")
}

func (c *LanguageChannel) ask(ctx context.Context, call Call) (i18n.Msg, error) {
	in := call.User.LanguageFor(call.User.TelegramLanguage)
	var row []Button
	for _, l := range users.Languages {
		row = append(row, Button{Msg: languageName(l), Action: LanguageAction, Arg: l})
	}
	_, err := c.bot.Send(ctx, Out{ChatID: call.ChatID, UserID: call.User.ID, Origin: "motor",
		Msg: i18n.M("bot.language_current", "language", languageName(in)), Buttons: [][]Button{row}})
	return i18n.Msg{}, err
}

// click writes the language of the user who touched the button, and of nobody else
// (the bot already refused a button of another person or an old one).
func (c *LanguageChannel) click(ctx context.Context, k Click) (i18n.Msg, i18n.Msg, error) {
	if !slices.Contains(users.Languages, k.Arg) {
		return i18n.Msg{}, i18n.Msg{}, errInvalidButton
	}
	if err := c.Users.SetLanguage(ctx, k.User.ID, k.Arg); err != nil {
		if errors.Is(err, users.ErrBadLanguage) {
			return i18n.Msg{}, i18n.Msg{}, errInvalidButton
		}
		return i18n.Msg{}, i18n.Msg{}, err
	}
	// said in the language just chosen (the bot reads it again after the action)
	return i18n.M("bot.language_set_toast", "language", languageName(k.Arg)), i18n.M("bot.language_set", "language", languageName(k.Arg)), nil
}
