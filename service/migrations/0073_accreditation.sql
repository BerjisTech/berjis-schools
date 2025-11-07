-- Regional accreditation records per school

CREATE TABLE IF NOT EXISTS school_accreditations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  school_id UUID NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
  region TEXT NOT NULL,                 -- e.g., US-CA
  accreditor_name TEXT NOT NULL,        -- e.g., WASC
  accreditation_id TEXT,                -- reference/certificate id
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','expired','revoked','pending')),
  issued_at TIMESTAMPTZ,
  expires_at TIMESTAMPTZ,
  evidence_url TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_accreditations_school ON school_accreditations(school_id);
CREATE INDEX IF NOT EXISTS idx_accreditations_region ON school_accreditations(region);

