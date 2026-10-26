-- The language of the panel a user chose (docs/painel/design.md). Empty means
-- not chosen yet: the panel then follows the language of the browser.
ALTER TABLE users ADD COLUMN language TEXT NOT NULL DEFAULT '';
