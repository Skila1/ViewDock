CREATE VIRTUAL TABLE media_fts USING fts5(
    item_kind,
    item_id,
    title,
    year,
    extra
);

CREATE TABLE collections (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE collection_items (
    collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    item_kind     TEXT NOT NULL,
    item_id       TEXT NOT NULL,
    position      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (collection_id, item_kind, item_id)
);

CREATE TABLE uploads (
    id            TEXT PRIMARY KEY,
    library_id    TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    filename      TEXT NOT NULL,
    staging_path  TEXT NOT NULL,
    size_bytes    INTEGER NOT NULL,
    offset_bytes  INTEGER NOT NULL DEFAULT 0,
    created_by    TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'open'
);

CREATE TABLE duplicate_groups (
    id         TEXT PRIMARY KEY,
    library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    fingerprint TEXT NOT NULL,
    created_at TEXT NOT NULL
);
