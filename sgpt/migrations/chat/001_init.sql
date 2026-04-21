CREATE TABLE chat (
  request_id UUID NOT NULL,
  chat_id TEXT NOT NULL PRIMARY KEY,
  create_time TIMESTAMP NOT NULL,
  update_time TIMESTAMP NOT NULL,
  delete_time TIMESTAMP,
  tags TEXT[] NOT NULL,
  files TEXT[] NOT NULL,
  metadata BYTEA NOT NULL
);
