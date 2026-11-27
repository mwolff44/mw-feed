CREATE TABLE folders (
  id INTEGER PRIMARY KEY,
  title TEXT NOT NULL UNIQUE,
  position INTEGER NOT NULL,
  collapsed INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE feeds (
  id INTEGER PRIMARY KEY,
  xml_url TEXT NOT NULL UNIQUE,
  html_url TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  update_interval INTEGER,
  last_update_at INTEGER,
  last_error TEXT NOT NULL DEFAULT '',
  last_warning TEXT NOT NULL DEFAULT '',
  http_etag TEXT NOT NULL DEFAULT '',
  http_last_modified TEXT NOT NULL DEFAULT '',
  last_item_time INTEGER
);

CREATE TABLE feed_folders (
  feed_id INTEGER NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
  folder_id INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
  PRIMARY KEY (feed_id, folder_id)
);

CREATE TABLE items (
  id INTEGER PRIMARY KEY,
  feed_id INTEGER REFERENCES feeds(id) ON DELETE SET NULL,
  feed_title TEXT NOT NULL,
  link TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  via TEXT NOT NULL DEFAULT '',
  author TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL DEFAULT '',
  published_at INTEGER NOT NULL,
  is_read INTEGER NOT NULL DEFAULT 0,
  starred_at INTEGER,
  UNIQUE (feed_id, link)
);

CREATE INDEX items_feed_read_published ON items (feed_id, is_read, published_at);
CREATE INDEX items_starred ON items (starred_at);
