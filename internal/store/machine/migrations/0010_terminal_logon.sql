-- The logon SID of each terminal (Windows): the launcher reads it from the
-- token it made for the mestre-pty and the engine grants it on the socket of
-- the local channel while the terminal lives
-- (docs/arquitetura/windows.md#acls-do-socket-da-cli-e-da-pasta-pty). It is
-- kept so a restarted engine puts the grant back for the terminals that
-- survived. NULL where there is none (Linux, rows from before).
ALTER TABLE terminals ADD COLUMN logon_sid TEXT;
