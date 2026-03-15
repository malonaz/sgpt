DROP INDEX IF EXISTS idx_chat_search_vector;

ALTER TABLE chat
DROP COLUMN IF EXISTS search_vector;

CREATE INDEX idx_chat_searchable_content_trgm
ON chat USING GIN(searchable_content gin_trgm_ops);
