-- The server renders the content of every item of a page, so nothing is left to preload.
ALTER TABLE settings DROP COLUMN preload;
