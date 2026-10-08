-- Append-only history of runtime configuration changes. Secret values are
-- stored encrypted exactly as in server_settings.
CREATE TABLE config_history (
    version     INTEGER NOT NULL,
    key         TEXT NOT NULL,
    value       TEXT NOT NULL,
    was_set     INTEGER NOT NULL DEFAULT 1,
    actor_id    TEXT NOT NULL DEFAULT '',
    note        TEXT NOT NULL DEFAULT '',
    changed_at  TEXT NOT NULL,
    PRIMARY KEY (version, key)
);
CREATE INDEX config_history_key ON config_history(key, version);

INSERT OR IGNORE INTO server_settings(key, value) VALUES ('config.version', '0');
