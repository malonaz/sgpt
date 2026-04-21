ALTER TABLE chat
  ALTER COLUMN metadata TYPE jsonb USING metadata::text::jsonb;
