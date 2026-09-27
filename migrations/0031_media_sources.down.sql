DELETE FROM libraries WHERE id IN (SELECT library_id FROM media_sources);
DROP TABLE IF EXISTS remote_items;
DROP TABLE IF EXISTS media_sources;
