-- Moving titles between libraries. A job moves one or more titles into one
-- destination library; each title is one item. Items double as the move
-- journal: the planned filesystem operations are written before anything on
-- disk changes, so a move interrupted by a crash or restart is finished or
-- rolled back at the next start instead of leaving the catalogue and the
-- disk disagreeing.

CREATE TABLE library_move_jobs (
    id                      TEXT PRIMARY KEY,
    source_library_id       TEXT NOT NULL DEFAULT '',
    destination_library_id  TEXT NOT NULL,
    status                  TEXT NOT NULL DEFAULT 'running', -- running|done|failed|interrupted
    total                   INTEGER NOT NULL DEFAULT 0,
    moved                   INTEGER NOT NULL DEFAULT 0,
    skipped                 INTEGER NOT NULL DEFAULT 0,
    failed                  INTEGER NOT NULL DEFAULT 0,
    created_by              TEXT NOT NULL DEFAULT '',
    created_at              TEXT NOT NULL,
    finished_at             TEXT NOT NULL DEFAULT ''
);

CREATE TABLE library_move_items (
    id                      TEXT PRIMARY KEY,
    job_id                  TEXT NOT NULL REFERENCES library_move_jobs(id) ON DELETE CASCADE,
    position                INTEGER NOT NULL DEFAULT 0,
    item_kind               TEXT NOT NULL,
    item_id                 TEXT NOT NULL,
    title                   TEXT NOT NULL DEFAULT '',
    source_library_id       TEXT NOT NULL DEFAULT '',
    destination_library_id  TEXT NOT NULL,
    status                  TEXT NOT NULL DEFAULT 'pending', -- pending|moving|fs_done|moved|skipped|failed|rolled_back
    reason                  TEXT NOT NULL DEFAULT '',
    message                 TEXT NOT NULL DEFAULT '',
    target                  TEXT NOT NULL DEFAULT '',
    ops_json                TEXT NOT NULL DEFAULT '',
    files_json              TEXT NOT NULL DEFAULT '',
    updated_at              TEXT NOT NULL
);
CREATE INDEX library_move_items_job ON library_move_items(job_id);
CREATE INDEX library_move_items_status ON library_move_items(status);
