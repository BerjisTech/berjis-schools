-- Configurable grade/year levels per school

CREATE TABLE IF NOT EXISTS grade_levels (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  school_id UUID NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
  code TEXT NOT NULL,                -- e.g., K, 1, 2, 11, Year 1, S3
  name TEXT NOT NULL,                -- human label
  order_index INTEGER NOT NULL DEFAULT 0,
  min_age INTEGER,                   -- optional guidance
  max_age INTEGER,                   -- optional guidance
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (school_id, code)
);

-- Classes can optionally map to a grade level
ALTER TABLE classes ADD COLUMN IF NOT EXISTS grade_level_id UUID REFERENCES grade_levels(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_grade_levels_school ON grade_levels(school_id);

