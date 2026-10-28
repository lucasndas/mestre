-- The last language_code the Telegram sent for a paired user (docs/plataforma/idioma-por-canal.md):
-- a hint kept with the link to the Telegram, never the language the user chose
-- (users.language). Empty until a message arrives.
ALTER TABLE users ADD COLUMN telegram_language TEXT NOT NULL DEFAULT '';
