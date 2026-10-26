-- A terminal id belongs to one living character: the panel and the Citadel
-- resolve a terminal to its character by this id, so two living rows with the
-- same one would let one machine pass for another's terminal.
CREATE UNIQUE INDEX characters_living_terminal ON characters (terminal_id) WHERE terminal_id <> '' AND state <> 'stopped';
