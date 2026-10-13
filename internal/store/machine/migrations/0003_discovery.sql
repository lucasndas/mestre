-- Addresses of the machines of the Guild (docs/conectividade/descoberta.md#cache):
-- where the Citadel answered last, and the machines that guild.config names.
-- addr is "host:port" of the WSS port of the engine; source is where it came
-- from the first time (configured, broadcast, mdns, guild.config);
-- machine_id is empty until someone answered there. last_success is NULL
-- until a connection worked. Timestamps are Unix milliseconds in UTC.
-- Addresses without use for 30 days are deleted.
CREATE TABLE known_addresses (
	addr         TEXT    PRIMARY KEY,
	machine_id   TEXT    NOT NULL DEFAULT '',
	source       TEXT    NOT NULL,
	added_at     INTEGER NOT NULL,
	last_success INTEGER
);
