-- The extra names of the panel (docs/arquitetura/seguranca.md#nomes-dos-certificados):
-- a JSON array the Owner sets for the Guild, never a machine. Only the
-- certificate of the acting Citadel carries them, with mestre.local. The
-- names of machines.names are now only {"hostname", "ips"}; an "extra" key
-- left there is ignored.
ALTER TABLE guild ADD COLUMN extra_names TEXT NOT NULL DEFAULT '[]';
