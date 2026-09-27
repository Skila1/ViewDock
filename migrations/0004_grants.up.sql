CREATE TABLE library_grants (
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id   TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    can_download INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, library_id)
);
CREATE INDEX library_grants_library ON library_grants(library_id);
