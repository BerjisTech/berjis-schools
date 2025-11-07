-- Legal documents and user consents, including parental consent

CREATE TABLE IF NOT EXISTS legal_documents (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  region TEXT NOT NULL,              -- e.g., default, EU, US, US-CA
  kind TEXT NOT NULL CHECK (kind IN ('terms','privacy','coppa','gdpr')), -- document type
  version TEXT NOT NULL,
  title TEXT NOT NULL,
  url TEXT,
  effective_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (region, kind, version)
);

CREATE TABLE IF NOT EXISTS user_consents (
  user_id TEXT NOT NULL,
  region TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('terms','privacy','coppa','gdpr')),
  version TEXT NOT NULL,
  consented_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, region, kind)
);

-- Parental consent requests for minors (identified by guardian-child linkage)
CREATE TABLE IF NOT EXISTS parental_consents (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  child_user_id TEXT NOT NULL,
  guardian_user_id TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','denied','revoked')),
  requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  responded_at TIMESTAMPTZ,
  notes TEXT,
  UNIQUE (child_user_id, guardian_user_id)
);

CREATE INDEX IF NOT EXISTS idx_user_consents_user ON user_consents(user_id);
CREATE INDEX IF NOT EXISTS idx_parental_consents_child ON parental_consents(child_user_id);

