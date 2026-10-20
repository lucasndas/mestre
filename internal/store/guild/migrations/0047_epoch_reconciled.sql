-- When the Citadel of an epoch finished reconciling GitHub after taking it
-- (docs/guilda/cidadela-e-torres.md#github-depois-da-troca): 0 until it did, so
-- a promoted Citadel that restarts before it did reconciles again. Unix
-- milliseconds in UTC.
ALTER TABLE epochs ADD COLUMN reconciled_at INTEGER NOT NULL DEFAULT 0;
