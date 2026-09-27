CREATE TABLE IF NOT EXISTS households (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS households_owner ON households(owner_id);
CREATE TABLE IF NOT EXISTS household_members (
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL DEFAULT 'member',
    age_limit INTEGER NOT NULL DEFAULT 0,
    expires_at TEXT,
    joined_at TEXT NOT NULL,
    PRIMARY KEY (household_id, user_id)
);
CREATE INDEX IF NOT EXISTS household_members_user ON household_members(user_id);
CREATE TABLE IF NOT EXISTS household_invites (
    id TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    role TEXT NOT NULL DEFAULT 'member',
    age_limit INTEGER NOT NULL DEFAULT 0,
    expires_at TEXT NOT NULL,
    used_by TEXT,
    used_at TEXT,
    created_at TEXT NOT NULL
);