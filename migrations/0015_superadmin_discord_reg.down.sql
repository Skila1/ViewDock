DELETE FROM user_roles WHERE role_id = 'sys-superadmin';
DELETE FROM role_permissions WHERE role_id = 'sys-superadmin';
DELETE FROM roles WHERE id = 'sys-superadmin';
DELETE FROM permissions WHERE id = 'superadmin';
DELETE FROM server_settings WHERE key = 'setup.original_admin_id';
