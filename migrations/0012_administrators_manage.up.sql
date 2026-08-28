INSERT OR IGNORE INTO permissions(id, name, description) VALUES
    ('administrators.manage', 'administrators.manage', 'Create and manage administrators');

INSERT OR IGNORE INTO role_permissions(role_id, permission_id)
VALUES ('sys-administrator', 'administrators.manage');
