-- Configurable grading scales (percentage -> letter mapping) per school

CREATE TABLE IF NOT EXISTS grading_scales (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  school_id UUID NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  is_default BOOLEAN NOT NULL DEFAULT FALSE,
  active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS grading_scale_entries (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  scale_id UUID NOT NULL REFERENCES grading_scales(id) ON DELETE CASCADE,
  min_percent NUMERIC(5,2) NOT NULL,
  max_percent NUMERIC(5,2) NOT NULL,
  letter TEXT NOT NULL,
  points NUMERIC(4,2),
  CONSTRAINT ck_percent_range CHECK (min_percent >= 0 AND max_percent <= 100 AND min_percent < max_percent)
);

CREATE INDEX IF NOT EXISTS idx_grading_scales_school ON grading_scales(school_id);
CREATE INDEX IF NOT EXISTS idx_grading_entries_scale ON grading_scale_entries(scale_id);

