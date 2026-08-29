CREATE TABLE api_keys (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    scopes       TEXT NOT NULL,
    created_by   TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at   TEXT
);
CREATE INDEX api_keys_hash ON api_keys(token_hash);

CREATE TABLE operational_logs (
    id         TEXT PRIMARY KEY,
    created_at TEXT NOT NULL,
    level      TEXT NOT NULL,
    category   TEXT NOT NULL DEFAULT '',
    message    TEXT NOT NULL,
    details    TEXT NOT NULL DEFAULT '{}',
    actor_id   TEXT
);
CREATE INDEX operational_logs_created ON operational_logs(created_at DESC);
CREATE INDEX operational_logs_level ON operational_logs(level, created_at DESC);

INSERT OR IGNORE INTO permissions(id, name, description) VALUES
    ('perm-logs-read', 'logs.read', 'Read operational logs');

INSERT OR IGNORE INTO role_permissions(role_id, permission_id)
SELECT 'sys-administrator', 'perm-logs-read'
WHERE EXISTS (SELECT 1 FROM roles WHERE id = 'sys-administrator');
