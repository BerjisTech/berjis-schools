-- Certificate automation rules

CREATE TABLE IF NOT EXISTS certificate_rules (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  scope TEXT NOT NULL CHECK (scope IN ('class','school')),
  school_id UUID REFERENCES schools(id) ON DELETE CASCADE,
  class_id UUID REFERENCES classes(id) ON DELETE CASCADE,
  template_id UUID NOT NULL REFERENCES certificate_templates(id) ON DELETE RESTRICT,
  enabled BOOLEAN NOT NULL DEFAULT false,
  conditions JSONB, -- { minPercent: number, requireAllTestsCompleted: bool, minLessonsCompleted: int }
  created_by_user_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_cert_rules_scope_school ON certificate_rules(scope, school_id);
CREATE INDEX IF NOT EXISTS idx_cert_rules_class ON certificate_rules(class_id);
CREATE INDEX IF NOT EXISTS idx_cert_rules_template ON certificate_rules(template_id);

