-- What the panel adds to the workspaces (docs/painel/administracao.md): the sheet
-- of the Grand Master chosen when the workspace is created, which the stage files
-- uses to start it, and the hash of the outside edit of verificacao.yaml the
-- Administrators were told of (so one edit is told once).
ALTER TABLE session_zero ADD COLUMN grand_master_sheet TEXT NOT NULL DEFAULT '';
ALTER TABLE verification_commands ADD COLUMN outside_hash TEXT NOT NULL DEFAULT '';
