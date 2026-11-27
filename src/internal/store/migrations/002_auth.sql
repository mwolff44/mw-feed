CREATE TABLE user (
  id INTEGER PRIMARY KEY CHECK(id=1),
  login TEXT NOT NULL,
  password_hash TEXT NOT NULL
);

CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  csrf_token TEXT NOT NULL,
  authenticated INTEGER NOT NULL,
  persistent INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL
);

CREATE TABLE login_failures (
  ip TEXT PRIMARY KEY,
  count INTEGER NOT NULL,
  last_failure_at INTEGER NOT NULL,
  banned_until INTEGER
);
