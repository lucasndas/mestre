// Package attach is `mestre attach` (docs/cli/attach.md): the command of people
// that shows, and with the level types into, the terminal of a character of the
// Guild in their own terminal. It is a client of the panel, as the browser is: it
// logs in with the rules of the panel (docs/painel/login.md), follows the
// terminal on /ws/ui and draws it with the emulator the machines use. It never
// uses the local channel, which is for agents.
package attach
