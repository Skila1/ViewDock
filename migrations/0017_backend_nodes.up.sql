CREATE TABLE IF NOT EXISTS backend_nodes (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    host TEXT NOT NULL DEFAULT '',
    port INTEGER NOT NULL DEFAULT 0,
    scheme TEXT NOT NULL DEFAULT 'https',
    role TEXT NOT NULL DEFAULT 'media-worker',
    region TEXT NOT NULL DEFAULT '',
    capabilities TEXT NOT NULL DEFAULT '',
    priority INTEGER NOT NULL DEFAULT 0,
    weight INTEGER NOT NULL DEFAULT 0,
    capacity INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1,
    status TEXT NOT NULL DEFAULT 'unknown',
    latency_ms INTEGER NOT NULL DEFAULT 0,
    health TEXT NOT NULL DEFAULT '',
    draining INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS backend_nodes_route ON backend_nodes(enabled, draining, status, role, priority);