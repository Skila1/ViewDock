-- Administrator overrides for source reliability ranking. Scores themselves
-- are kept in memory and decay; only explicit admin decisions are durable.
CREATE TABLE source_reliability_overrides (
    source      TEXT PRIMARY KEY,
    mode        TEXT NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    actor_id    TEXT NOT NULL DEFAULT '',
    updated_at  TEXT NOT NULL
);
