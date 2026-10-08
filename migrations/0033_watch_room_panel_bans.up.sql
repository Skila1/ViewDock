ALTER TABLE watch_rooms ADD COLUMN panel TEXT NOT NULL DEFAULT 'everyone';
ALTER TABLE watch_rooms ADD COLUMN banned_json TEXT NOT NULL DEFAULT '[]';
