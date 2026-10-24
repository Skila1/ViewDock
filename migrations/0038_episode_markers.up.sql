-- Detected end credits of an episode, beside the intro columns from 0003.
-- markers_key records the file version the markers were detected from, so
-- a replaced file is analysed again.
ALTER TABLE episodes ADD COLUMN credits_start_ms INTEGER;
ALTER TABLE episodes ADD COLUMN credits_source TEXT NOT NULL DEFAULT '';
ALTER TABLE episodes ADD COLUMN markers_key TEXT NOT NULL DEFAULT '';
