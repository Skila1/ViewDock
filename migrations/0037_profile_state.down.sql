DROP TABLE IF EXISTS user_playlist_items;
DROP TABLE IF EXISTS user_playlists;
DROP TABLE IF EXISTS title_track_prefs;
ALTER TABLE user_preferences DROP COLUMN home_rows;
ALTER TABLE user_preferences DROP COLUMN source_pref;
ALTER TABLE user_preferences DROP COLUMN upnext_seconds;
ALTER TABLE user_preferences DROP COLUMN quality;
ALTER TABLE user_preferences DROP COLUMN playback_rate;
ALTER TABLE playback_progress DROP COLUMN dismissed;
