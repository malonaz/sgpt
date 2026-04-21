-- Add the text searchable_content column
ALTER TABLE chat
ADD COLUMN searchable_content TEXT;

-- Add the tsvector column (generated automatically from searchable_content)
ALTER TABLE chat
ADD COLUMN search_vector tsvector
GENERATED ALWAYS AS (
  to_tsvector('simple', coalesce(searchable_content, ''))
) STORED;

-- Create GIN index for fast full-text search
CREATE INDEX idx_chat_search_vector ON chat USING GIN(search_vector);
