-- The texts the engine keeps for people to read later are kept as a message (the
-- JSON of an i18n.Msg: a code and its parameters), and each reader translates
-- it in his language when he reads it (docs/plataforma/mensagens-por-codigo.md#textos-guardados).
-- A row written before this, with only text, is shown as it was written.
ALTER TABLE panel_notices ADD COLUMN msg TEXT NOT NULL DEFAULT '';
ALTER TABLE blocking_notices ADD COLUMN msg TEXT NOT NULL DEFAULT '';
ALTER TABLE approval_flow ADD COLUMN msg TEXT NOT NULL DEFAULT '';
ALTER TABLE panel_chat_progress ADD COLUMN msg TEXT NOT NULL DEFAULT '';
