-- Direct messaging (one-on-one) scoped by class

CREATE TABLE IF NOT EXISTS direct_threads (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  class_id UUID NOT NULL REFERENCES classes(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS direct_participants (
  thread_id UUID NOT NULL REFERENCES direct_threads(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL,
  role TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (thread_id, user_id)
);

CREATE TABLE IF NOT EXISTS direct_messages (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  thread_id UUID NOT NULL REFERENCES direct_threads(id) ON DELETE CASCADE,
  sender_user_id TEXT NOT NULL,
  body TEXT,
  attachments JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  read_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_direct_messages_thread ON direct_messages(thread_id, created_at);
CREATE INDEX IF NOT EXISTS idx_direct_participants_user ON direct_participants(user_id);
