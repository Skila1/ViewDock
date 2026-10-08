DROP TABLE IF EXISTS media_source_events;
ALTER TABLE media_sources DROP COLUMN policy;
ALTER TABLE media_sources DROP COLUMN remote_user_name;
ALTER TABLE media_sources DROP COLUMN api_key_enc;
ALTER TABLE media_sources DROP COLUMN auth_mode;
