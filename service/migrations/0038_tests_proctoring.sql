-- Test scheduling and proctoring metadata

ALTER TABLE tests
  ADD COLUMN IF NOT EXISTS proctoring JSONB,
  ADD COLUMN IF NOT EXISTS proctor_code TEXT;

CREATE INDEX IF NOT EXISTS idx_tests_proctor_code ON tests(proctor_code);

