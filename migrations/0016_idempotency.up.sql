CREATE TABLE request_idempotency (
    scope_key    TEXT NOT NULL,
    request_key  TEXT NOT NULL,
    method       TEXT NOT NULL,
    path         TEXT NOT NULL,
    status_code  INTEGER NOT NULL,
    content_type TEXT NOT NULL DEFAULT 'application/json',
    body         BLOB NOT NULL,
    created_at   TEXT NOT NULL,
    PRIMARY KEY (scope_key, request_key)
);
CREATE INDEX request_idempotency_created ON request_idempotency(created_at);