-- ViewDock 0001 — foundation only.
-- Dialect: SQLite. 0002 auth, 0003 libraries, 0004 grants, 0005 progress,
-- 0006 derivatives, 0007 shares, 0008 streams, 0009 metadata, 0010 search/upload.

CREATE TABLE server_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);
