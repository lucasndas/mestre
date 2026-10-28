package telegram

import (
	"html"
	"regexp"
	"strings"

	"github.com/mestre-ia/mestre/internal/i18n"
)

// SystemHeader is the header of a message that belongs to no workspace.
const SystemHeader = "Sistema"

var mesaID = regexp.MustCompile(`^(\d+)-(.+)$`)

// Header is the first line of every message of the bot, in bold, saying where
// it comes from (docs/telegram/roteamento.md#cabeçalho): the workspace, the
// workspace and the mesa ("loja · Mesa #42 checkout") or "Sistema".
func Header(workspace, mesa string) string { return HeaderIn(i18n.PtBR, workspace, mesa) }

// HeaderIn is Header in a language ("System" and "Table #42 checkout" in English).
func HeaderIn(lang, workspace, mesa string) string {
	switch {
	case workspace == "":
		return i18n.Render(lang, i18n.M("bot.header_system"))
	case mesa == "":
		return workspace
	}
	if m := mesaID.FindStringSubmatch(mesa); m != nil {
		return i18n.Render(lang, i18n.M("bot.header_table_numbered", "workspace", workspace, "number", m[1], "name", strings.ReplaceAll(m[2], "-", " ")))
	}
	return i18n.Render(lang, i18n.M("bot.header_table", "workspace", workspace, "table", mesa))
}

// render makes the text of one part of a message for the Telegram, with the
// header in front of it (the first part only): the HTML, and the plain text for
// the fallback; ok is false when the Markdown does not convert, and then the
// plain text is what goes. The header is made of the names of the engine, never
// of text of an agent, and is always escaped.
func render(head, part string) (htmlText, plain string, ok bool) {
	plain = part
	if head != "" {
		plain = head + "\n" + part
	}
	h, err := ToHTML(part)
	if err != nil {
		return "", plain, false
	}
	if head != "" {
		h = "<b>" + html.EscapeString(head) + "</b>\n" + h
	}
	return h, plain, true
}

// headOf is the header of a message for a user, "" for a reply to someone who
// is not a user (a wrong code says nothing about the Mestre).
func headOf(lang, userID, workspace, mesa string) string {
	if userID == "" {
		return ""
	}
	return strings.NewReplacer("*", "", "`", "", "\n", " ").Replace(HeaderIn(lang, workspace, mesa))
}
