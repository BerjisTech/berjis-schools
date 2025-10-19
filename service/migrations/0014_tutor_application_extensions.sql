-- Extend private_tutors with rich application JSON blobs
ALTER TABLE private_tutors
  ADD COLUMN IF NOT EXISTS profile JSONB,
  ADD COLUMN IF NOT EXISTS verification JSONB,
  ADD COLUMN IF NOT EXISTS education JSONB,
  ADD COLUMN IF NOT EXISTS teaching JSONB,
  ADD COLUMN IF NOT EXISTS media JSONB,
  ADD COLUMN IF NOT EXISTS payout JSONB,
  ADD COLUMN IF NOT EXISTS consents JSONB;

-- Optional review tracking
ALTER TABLE private_tutors
  ADD COLUMN IF NOT EXISTS reviewed_by_user_id TEXT,
  ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMPTZ;
