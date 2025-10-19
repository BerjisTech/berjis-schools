-- School applications storage
CREATE TABLE IF NOT EXISTS school_applications (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_user_id TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('draft','pending','approved','rejected')),
  info JSONB,
  verify JSONB,
  staff JSONB,
  finance JSONB,
  curriculum JSONB,
  agreements JSONB,
  extras JSONB,
  progress_pct INTEGER NOT NULL DEFAULT 0,
  reviewed_by_user_id TEXT,
  reviewed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_school_applications_status ON school_applications(status);
CREATE INDEX IF NOT EXISTS idx_school_applications_owner ON school_applications(owner_user_id);
