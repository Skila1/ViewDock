-- RBAC (roles + permissions) and optional Discord OAuth.
-- library_grants stays user-scoped; library_role_grants adds role-scoped ACL.

CREATE TABLE roles (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE COLLATE NOCASE,
    description TEXT NOT NULL DEFAULT '',
    is_system   INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL
);

CREATE TABLE permissions (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT ''
);

CREATE TABLE role_permissions (
    role_id       TEXT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id TEXT NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE user_roles (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id TEXT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

CREATE TABLE library_role_grants (
    role_id      TEXT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    library_id   TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    can_download INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (role_id, library_id)
);
CREATE INDEX library_role_grants_library ON library_role_grants(library_id);

CREATE TABLE discord_settings (
    id                    INTEGER PRIMARY KEY CHECK (id = 1),
    login_enabled         INTEGER NOT NULL DEFAULT 0,
    client_id             TEXT NOT NULL DEFAULT '',
    client_secret         TEXT NOT NULL DEFAULT '',
    registration_enabled  INTEGER NOT NULL DEFAULT 0,
    admin_discord_ids     TEXT NOT NULL DEFAULT '',
    updated_at            TEXT NOT NULL
);

CREATE TABLE user_identities (
    user_id            TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider           TEXT NOT NULL,
    provider_user_id   TEXT NOT NULL,
    provider_username  TEXT NOT NULL DEFAULT '',
    avatar_hash        TEXT NOT NULL DEFAULT '',
    linked_at          TEXT NOT NULL,
    PRIMARY KEY (user_id, provider)
);
CREATE UNIQUE INDEX user_identities_provider_uid ON user_identities(provider, provider_user_id);

CREATE TABLE login_states (
    state          TEXT PRIMARY KEY,
    provider       TEXT NOT NULL,
    code_verifier  TEXT NOT NULL,
    link_user_id   TEXT NOT NULL DEFAULT '',
    expires_at     TEXT NOT NULL
);

ALTER TABLE users ADD COLUMN has_password INTEGER NOT NULL DEFAULT 1;

INSERT INTO permissions(id, name, description) VALUES
    ('admin', 'admin', 'Full administrator'),
    ('users.manage', 'users.manage', 'Create and manage users'),
    ('roles.manage', 'roles.manage', 'Create and manage groups'),
    ('libraries.manage', 'libraries.manage', 'Create and edit libraries'),
    ('media.upload', 'media.upload', 'Upload media'),
    ('media.delete', 'media.delete', 'Delete media'),
    ('shares.create', 'shares.create', 'Create share links'),
    ('shares.manage', 'shares.manage', 'List and revoke shares'),
    ('streams.inspect', 'streams.inspect', 'View live streams and inspector'),
    ('settings.manage', 'settings.manage', 'Configure integrations');

INSERT INTO roles(id, name, description, is_system, created_at) VALUES
    ('sys-administrator', 'Administrator', 'Full access to every library and setting', 1, strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    ('sys-user', 'User', 'Browse, stream, upload, and share granted libraries', 1, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));

INSERT INTO role_permissions(role_id, permission_id)
SELECT 'sys-administrator', id FROM permissions;

INSERT INTO role_permissions(role_id, permission_id) VALUES
    ('sys-user', 'media.upload'),
    ('sys-user', 'shares.create');

INSERT INTO user_roles(user_id, role_id)
SELECT id, 'sys-administrator' FROM users WHERE is_admin = 1;

INSERT INTO user_roles(user_id, role_id)
SELECT id, 'sys-user' FROM users WHERE is_admin = 0;

INSERT INTO library_role_grants(role_id, library_id, can_download)
SELECT 'sys-user', id, 0 FROM libraries;

INSERT INTO discord_settings(id, login_enabled, client_id, client_secret, registration_enabled, admin_discord_ids, updated_at)
VALUES (1, 0, '', '', 0, '', strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));
