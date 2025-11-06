-- Track primary affiliation per user and role

ALTER TABLE school_members
  ADD COLUMN IF NOT EXISTS is_primary BOOLEAN NOT NULL DEFAULT false;

-- Only one active primary per user+role
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes WHERE schemaname = 'public' AND indexname = 'uq_primary_affiliation'
  ) THEN
    EXECUTE 'CREATE UNIQUE INDEX uq_primary_affiliation ON school_members(user_id, role) WHERE status = ''active'' AND is_primary = true';
  END IF;
END$$;

