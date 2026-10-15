// Package quests conducts the quests (missões) of the tables
// (docs/orquestracao/missoes.md): a flow of steps in which a character hands
// the work to the next. The engine conducts it, in a deterministic way; nothing
// depends on an agent remembering to call the next one.
//
// A Definition (YAML or JSON) is validated by itself (Validate) and against the
// sheets, the characters and the ceiling (Service.Create). The Service keeps the
// quests, their steps and every delivery of a step in the database, and moves
// them:
//
//   - Run puts the quest in running; the steps whose previous steps ended (the
//     join says how) are prepared: the approval before, the character (the one
//     named, a persistent one that is free, or one the engine makes, within the
//     limits), the synchronization of the worktree on a Tower; then the item
//     `quest` goes to the inbox of the character and the step runs;
//   - Finish is the `mestre done` of the character: the result (the summary and
//     the files the branch changed since the step began) goes to the scroll of
//     the quest and becomes the input of the next steps. A done with --return
//     hands the work back, up to max_returns; past it the quest pauses and the
//     Master of the table gets the event return_limit;
//   - the points of approval go through the Approvals interface (milestone 5)
//     and come back in Decided, which never lets the designer of the quest
//     decide them;
//   - Sweep finds the steps whose character is idle for too long (stuck) and
//     tells the Master, and later the Grand Master and the users; the quests of
//     a table that ended are cancelled; the mirror on GitHub catches up.
//
// The Service is a characters.Observer: a character interrupted puts its step
// back in preparing, to be delivered again when it is back; one made for a step
// (after a vacancy, or taking another's place) gets the step. Pause, Resume and
// Cancel never interrupt a busy character. Handlers are the commands `done` and
// `quest …` for the local forwarder of the Citadel.
package quests
