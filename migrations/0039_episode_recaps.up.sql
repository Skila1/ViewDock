-- Detected "Previously on" recap of an episode.
ALTER TABLE episodes ADD COLUMN recap_start_ms INTEGER;
ALTER TABLE episodes ADD COLUMN recap_end_ms INTEGER;
ALTER TABLE episodes ADD COLUMN recap_source TEXT NOT NULL DEFAULT '';
