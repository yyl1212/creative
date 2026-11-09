CREATE TABLE admins (
 id INTEGER PRIMARY KEY,
 username TEXT NOT NULL UNIQUE,
 password_hash TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
 auth_version INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE sessions (token TEXT PRIMARY KEY, data BLOB NOT NULL, expiry REAL NOT NULL);
CREATE INDEX sessions_expiry ON sessions(expiry);
CREATE TABLE login_limits (key TEXT PRIMARY KEY, attempts INTEGER NOT NULL, expires INTEGER NOT NULL);
CREATE TABLE audit_events (id INTEGER PRIMARY KEY, action TEXT NOT NULL, account TEXT NOT NULL, source TEXT NOT NULL, created_at INTEGER NOT NULL DEFAULT (unixepoch()));
CREATE TABLE needs (id INTEGER PRIMARY KEY, status TEXT NOT NULL CHECK(status IN ('pending','qualified','completed')), version INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL DEFAULT (unixepoch()));
