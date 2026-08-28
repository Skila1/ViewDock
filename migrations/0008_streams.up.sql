CREATE TABLE media_streams (
    id            TEXT PRIMARY KEY,
    media_file_id TEXT NOT NULL REFERENCES media_files(id) ON DELETE CASCADE,
    index_n       INTEGER NOT NULL,
    kind          TEXT NOT NULL, -- video|audio|subtitle|attachment
    codec         TEXT NOT NULL DEFAULT '',
    language      TEXT NOT NULL DEFAULT '',
    title         TEXT NOT NULL DEFAULT '',
    channels      INTEGER NOT NULL DEFAULT 0,
    default_flag  INTEGER NOT NULL DEFAULT 0,
    forced        INTEGER NOT NULL DEFAULT 0,
    sdh           INTEGER NOT NULL DEFAULT 0,
    width         INTEGER NOT NULL DEFAULT 0,
    height        INTEGER NOT NULL DEFAULT 0,
    bit_depth     INTEGER NOT NULL DEFAULT 0,
    hdr           TEXT NOT NULL DEFAULT '',
    UNIQUE (media_file_id, index_n, kind)
);

ALTER TABLE media_files ADD COLUMN identity_hint TEXT NOT NULL DEFAULT '';
ALTER TABLE media_files ADD COLUMN parse_confidence TEXT NOT NULL DEFAULT 'medium';
