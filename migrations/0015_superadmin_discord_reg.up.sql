-- Superadmin inherits Administrator. Original admin is protected.
-- Discord registration can require a guild and/or role.

INSERT OR IGNORE INTO permissions(id, name, description) VALUES
    ('superadmin', 'superadmin', 'Protected Superadmin. Inherits every Administrator permission.');

INSERT OR IGNORE INTO roles(id, name, description, is_system, created_at) VALUES
    ('sys-superadmin', 'Superadmin', 'Inherits Administrator. The original admin cannot be deleted.', 1, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));

INSERT OR IGNORE INTO role_permissions(role_id, permission_id)
SELECT 'sys-superadmin', id FROM permissions;

INSERT OR IGNORE INTO server_settings(key, value)
SELECT 'setup.original_admin_id', id FROM users
WHERE is_admin = 1
ORDER BY created_at ASC
LIMIT 1;

INSERT OR IGNORE INTO user_roles(user_id, role_id)
SELECT value, 'sys-superadmin' FROM server_settings WHERE key = 'setup.original_admin_id';

ALTER TABLE discord_settings ADD COLUMN registration_guild_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE discord_settings ADD COLUMN registration_guild_id TEXT NOT NULL DEFAULT '';
ALTER TABLE discord_settings ADD COLUMN registration_role_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE discord_settings ADD COLUMN registration_role_id TEXT NOT NULL DEFAULT '';
