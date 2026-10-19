// Package telegram is the bot of the Mestre (docs/telegram/bot.md): the long
// polling of the Citadel, the filter of who may talk to it, the quick commands,
// the queue of what it sends (HTML, parts, buttons, the first explanation of a
// term) and the answers it captures for itself (a PIN, a second factor) so that
// they never reach an agent, the inbox or a log. Only the Citadel that is not
// suspended calls the Telegram: every call goes through the Gate.
package telegram
