CREATE TABLE chat (
  organization_id TEXT NOT NULL,
  user_id TEXT NOT NULL,
  chat_id TEXT NOT NULL,
  create_time TIMESTAMP NOT NULL,
  update_time TIMESTAMP NOT NULL,
  delete_time TIMESTAMP,
  external_id TEXT NOT NULL,
  contact TEXT NOT NULL,
  external_contact BOOLEAN NOT NULL,
  state SMALLINT NOT NULL,
  classification SMALLINT NOT NULL,
  metadata BYTEA NOT NULL,
  PRIMARY KEY (organization_id, user_id, chat_id)
);
