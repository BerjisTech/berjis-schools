-- Class versions (simple snapshots)
CREATE TABLE IF NOT EXISTS class_versions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  class_id UUID NOT NULL REFERENCES classes(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  title TEXT,
  description TEXT,
  created_by_user_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (class_id, version)
);

