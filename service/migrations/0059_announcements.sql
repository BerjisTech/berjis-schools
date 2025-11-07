-- School-wide announcements

CREATE TABLE IF NOT EXISTS announcements (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  school_id UUID NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
  author_user_id TEXT NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  visibility TEXT NOT NULL DEFAULT 'school' CHECK (visibility IN ('school','public')),
  pinned BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_announcements_school ON announcements(school_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_announcements_visibility ON announcements(visibility);

