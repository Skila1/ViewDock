-- Catalogue + scan_runs. No TMDB / streams yet.

CREATE TABLE libraries (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    root_path        TEXT NOT NULL,
    content_type     TEXT NOT NULL CHECK (content_type IN ('movies', 'tv', 'mixed')),
    uploads_enabled  INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE TABLE movies (
    id               TEXT PRIMARY KEY,
    library_id       TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    title            TEXT NOT NULL,
    year             INTEGER,
    sort_title       TEXT NOT NULL DEFAULT '',
    overview         TEXT NOT NULL DEFAULT '',
    metadata_source  TEXT NOT NULL DEFAULT 'filename',
    unmatched        INTEGER NOT NULL DEFAULT 1,
    needs_review     INTEGER NOT NULL DEFAULT 0,
    hint_mismatch    INTEGER NOT NULL DEFAULT 0,
    tmdb_id          INTEGER,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);
CREATE INDEX movies_library ON movies(library_id);

CREATE TABLE series (
    id               TEXT PRIMARY KEY,
    library_id       TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    title            TEXT NOT NULL,
    year             INTEGER,
    sort_title       TEXT NOT NULL DEFAULT '',
    overview         TEXT NOT NULL DEFAULT '',
    metadata_source  TEXT NOT NULL DEFAULT 'filename',
    unmatched        INTEGER NOT NULL DEFAULT 1,
    needs_review     INTEGER NOT NULL DEFAULT 0,
    hint_mismatch    INTEGER NOT NULL DEFAULT 0,
    tmdb_id          INTEGER,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);
CREATE INDEX series_library ON series(library_id);

CREATE TABLE seasons (
    id         TEXT PRIMARY KEY,
    series_id  TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    number     INTEGER NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    UNIQUE (series_id, number)
);

CREATE TABLE episodes (
    id            TEXT PRIMARY KEY,
    series_id     TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    season_id     TEXT NOT NULL REFERENCES seasons(id) ON DELETE CASCADE,
    season        INTEGER NOT NULL,
    number        INTEGER NOT NULL,
    title         TEXT NOT NULL DEFAULT '',
    overview      TEXT NOT NULL DEFAULT '',
    intro_start_ms INTEGER,
    intro_end_ms   INTEGER,
    intro_source  TEXT NOT NULL DEFAULT '',
    UNIQUE (series_id, season, number)
);
CREATE INDEX episodes_series ON episodes(series_id);

CREATE TABLE media_files (
    id            TEXT PRIMARY KEY,
    library_id    TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    rel_path      TEXT NOT NULL,
    abs_path      TEXT NOT NULL,
    size_bytes    INTEGER NOT NULL DEFAULT 0,
    inode         TEXT NOT NULL DEFAULT '',
    mtime         TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL DEFAULT 'unknown', -- movie|episode|extra|unknown
    movie_id      TEXT REFERENCES movies(id) ON DELETE SET NULL,
    extra_kind    TEXT NOT NULL DEFAULT '',
    probe_status  TEXT NOT NULL DEFAULT 'pending', -- pending|ok|failed|offline
    probe_error   TEXT NOT NULL DEFAULT '',
    probed_at     TEXT NOT NULL DEFAULT '',
    availability  TEXT NOT NULL DEFAULT 'online', -- online|offline|missing
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    container     TEXT NOT NULL DEFAULT '',
    video_codec   TEXT NOT NULL DEFAULT '',
    audio_codec   TEXT NOT NULL DEFAULT '',
    width         INTEGER NOT NULL DEFAULT 0,
    height        INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    UNIQUE (library_id, rel_path)
);
CREATE INDEX media_files_movie ON media_files(movie_id);

CREATE TABLE media_file_episodes (
    media_file_id TEXT NOT NULL REFERENCES media_files(id) ON DELETE CASCADE,
    episode_id    TEXT NOT NULL REFERENCES episodes(id) ON DELETE CASCADE,
    PRIMARY KEY (media_file_id, episode_id)
);

CREATE TABLE scan_runs (
    id            TEXT PRIMARY KEY,
    library_id    TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    status        TEXT NOT NULL DEFAULT 'running', -- running|ok|failed
    started_at    TEXT NOT NULL,
    finished_at   TEXT,
    files_seen    INTEGER NOT NULL DEFAULT 0,
    files_added   INTEGER NOT NULL DEFAULT 0,
    error         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX scan_runs_library ON scan_runs(library_id);
