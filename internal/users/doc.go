// Package users keeps the users of the Mestre and their levels
// (docs/telegram/usuarios-e-niveis.md): the table of what each level can do,
// the registration, the pairing of the Telegram by code, the PIN with its
// locks, the suspension and the removal, and the transfer of the post of Owner.
// The bot (M5, stage 2) and the panel (M6) call it; nothing here talks to
// Telegram. Every check of level is the engine's, never the agent's.
package users
