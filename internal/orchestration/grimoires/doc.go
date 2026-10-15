// Package grimoires saves a piece of the canvas of a table (the team, its
// classes, scrolls, connections and quests) and restores it in a table of any
// workspace (docs/orquestracao/grimorios.md). A grimoire is the recipe, never
// the state: no process, session, inbox, wait, use or history is in it.
//
// Save takes the selection of the panel and writes a Document (version 1) in
// the database of the Citadel. Restoring is two calls, as the panel needs
// them: Plan validates and says what would be made (nothing is created), and
// Restore does it:
//
//   - the sheets cited exist; every class is resolved in the destination (a
//     global one is the same, one that the destination has by name is used, one
//     of the table is copied, and one of the workspace of origin that the
//     destination does not have waits for the user to copy it as a class of the
//     table, map it to another class or cancel); a copied class must fit in the
//     ceiling of the destination; the limits of characters must hold the team;
//     names that exist get " (2)", " (3)"…, shown in the plan;
//   - then, in this order: the copied classes, the scrolls and tomes, the
//     characters, the connections and the quests (as drafts, with their issue
//     under the epic of the table); the Master of the table gets an item
//     `system` with what was restored.
//
// A character that could not be made does not undo the rest: it goes in the
// report (an interrupted one, or a pending one that waits for a vacancy).
package grimoires
