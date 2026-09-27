ALTER TABLE watch_rooms ADD COLUMN queue_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE watch_rooms ADD COLUMN intermission_until TEXT NOT NULL DEFAULT '';