-- Feed icons, one per site host. An empty data column records a failed download.
CREATE TABLE favicons (
    host         TEXT PRIMARY KEY,
    data         BLOB NOT NULL,
    content_type TEXT NOT NULL,
    fetched_at   INTEGER NOT NULL
);
