-- Track progress percentage for tutor applications
ALTER TABLE private_tutors
  ADD COLUMN IF NOT EXISTS progress_pct INTEGER NOT NULL DEFAULT 0 CHECK (progress_pct >= 0 AND progress_pct <= 100);
