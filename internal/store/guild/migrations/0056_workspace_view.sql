-- What the workspace view of the panel keeps (docs/painel/visao-do-workspace.md).
--
-- The workspace a user opened last in the panel: it is remembered on the Citadel, not
-- in the browser, so it follows the user to any device. Empty means none opened yet.
ALTER TABLE users ADD COLUMN last_workspace TEXT NOT NULL DEFAULT '';

-- The failure that paused a quest for "mesma falha repetida" (docs/orquestracao/prova-e-devolucao.md):
-- the count of the step starts again at the pause, so the workspace view keeps the
-- fingerprint and the places here.
CREATE TABLE quest_paused_failure (
    quest       TEXT PRIMARY KEY,
    fingerprint TEXT NOT NULL,
    count       INTEGER NOT NULL,
    places      TEXT NOT NULL DEFAULT ''
) STRICT;
