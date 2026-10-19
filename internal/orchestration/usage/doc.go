// Package usage keeps the use of the providers and the limits on it
// (docs/plataforma/limites-de-uso.md). Every piece of use a character makes is
// a row (time and turns always; tokens and cost when the provider gives
// them); the rows are summed by character, quest, table, workspace, account
// and day, and checked against the limits of the quest, the table, the
// workspace and the day: a warning at 80%, and at 100% the pause of what the
// limit covers and a custo_extra request in the chain of approval. The
// windows of the plan of each provider (5 h, 7 days) hold back the start of
// new work before the cut, and a character the provider cut waits for the
// quota (blocked, reason quota) until it is back.
//
// The Service is told what the characters do: OnEvent takes the events of the
// hooks (turn start and end, the usage of the statusLine) and Reading the
// normalized usage of ReadUsage. The pieces it acts on (the quests, the
// tables, the workspaces, the chain, the inbox) come in as small interfaces.
package usage
