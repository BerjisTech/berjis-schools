ALTER TABLE test_attempts ADD COLUMN IF NOT EXISTS responses JSONB;
ALTER TABLE test_attempts ADD COLUMN IF NOT EXISTS grading JSONB;
-- Optional index if querying by status frequently
CREATE INDEX IF NOT EXISTS idx_test_attempts_status ON test_attempts(status);

