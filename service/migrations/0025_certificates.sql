-- Certificate templates and issued certificates

CREATE TABLE IF NOT EXISTS certificate_templates (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  scope TEXT NOT NULL CHECK (scope IN ('platform','school')),
  school_id UUID REFERENCES schools(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  body JSONB,           -- template variables and content
  style JSONB,          -- optional style information
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived')),
  created_by_user_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_cert_templates_scope ON certificate_templates(scope);
CREATE INDEX IF NOT EXISTS idx_cert_templates_school ON certificate_templates(school_id);

CREATE TABLE IF NOT EXISTS certificates (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  template_id UUID NOT NULL REFERENCES certificate_templates(id) ON DELETE RESTRICT,
  recipient_user_id TEXT NOT NULL,
  class_id UUID REFERENCES classes(id) ON DELETE SET NULL,
  data JSONB,
  code TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL DEFAULT 'issued' CHECK (status IN ('issued','revoked')),
  issued_by_user_id TEXT NOT NULL,
  issued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at TIMESTAMPTZ,
  verify_url TEXT
);

CREATE INDEX IF NOT EXISTS idx_certificates_recipient ON certificates(recipient_user_id);
CREATE INDEX IF NOT EXISTS idx_certificates_template ON certificates(template_id);

