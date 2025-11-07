-- Academic year structure and term generation support
ALTER TABLE academic_years
  ADD COLUMN IF NOT EXISTS structure TEXT NOT NULL DEFAULT 'custom' CHECK (structure IN ('semester','trimester','quarter','custom'));

