DROP INDEX IF EXISTS users_temporary_expiry;
ALTER TABLE users DROP COLUMN created_by;
ALTER TABLE users DROP COLUMN party_only;
ALTER TABLE users DROP COLUMN max_sessions;
ALTER TABLE users DROP COLUMN expires_at;
ALTER TABLE users DROP COLUMN is_temporary;
