DROP INDEX IF EXISTS config_history_key;
DROP TABLE IF EXISTS config_history;
DELETE FROM server_settings WHERE key = 'config.version';
