// Package approvals is the flow of approval over the people
// (docs/aprovacoes/fluxo.md): it puts a request of the chain to the users who
// may answer it, takes their answers (level, workspace, PIN, quorum), keeps the
// question of each recipient up to date and ends it when the request ends.
// The decision itself is the chain's (chain.Service.DecideByPerson); the
// channel (the Telegram bot) is a Messenger.
package approvals
