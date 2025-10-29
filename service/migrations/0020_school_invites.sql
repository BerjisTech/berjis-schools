-- Support inviting external or existing users to join a school
CREATE TABLE IF NOT EXISTS school_invites (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  school_id UUID NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
  email TEXT,
  existing_user_id TEXT,
  role TEXT NOT NULL CHECK (role IN ('admin','tutor')),
  invited_by_user_id TEXT NOT NULL,
  token TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','expired','revoked')),
  message TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  responded_at TIMESTAMPTZ,
  CHECK (
    (email IS NOT NULL AND length(trim(email)) > 0)
    OR (existing_user_id IS NOT NULL AND length(trim(existing_user_id)) > 0)
  )
);

CREATE INDEX IF NOT EXISTS idx_school_invites_school ON school_invites(school_id);
CREATE INDEX IF NOT EXISTS idx_school_invites_token ON school_invites(token);
