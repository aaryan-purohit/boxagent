CREATE TABLE IF NOT EXISTS versions (
    path       TEXT    NOT NULL,
    version    INTEGER NOT NULL,
    hash       TEXT    NOT NULL,
    size       INTEGER NOT NULL,
    session_id TEXT    NOT NULL,
    created_at TEXT    NOT NULL,   -- RFC3339 UTC
    PRIMARY KEY (path, version)
);
