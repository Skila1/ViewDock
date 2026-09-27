DELETE FROM role_permissions WHERE permission_id = 'perm-logs-read';
DELETE FROM permissions WHERE id = 'perm-logs-read';
DROP TABLE IF EXISTS operational_logs;
DROP TABLE IF EXISTS api_keys;
