-- genres_json is '' until metadata has been looked up, then a JSON array of names.
-- collection names the source library a title came from (a Jellyfin library for
-- external sources), used to tell anime apart from other shows.
ALTER TABLE movies ADD COLUMN genres_json TEXT NOT NULL DEFAULT '';
ALTER TABLE movies ADD COLUMN collection TEXT NOT NULL DEFAULT '';
ALTER TABLE series ADD COLUMN genres_json TEXT NOT NULL DEFAULT '';
ALTER TABLE series ADD COLUMN collection TEXT NOT NULL DEFAULT '';
