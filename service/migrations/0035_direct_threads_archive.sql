-- Conversation history and archiving

ALTER TABLE direct_threads
  ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_direct_threads_archived ON direct_threads(archived_at);

