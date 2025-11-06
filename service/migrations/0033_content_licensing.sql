-- Content ownership and licensing tracking

ALTER TABLE classes
  ADD COLUMN IF NOT EXISTS license TEXT;

ALTER TABLE lessons
  ADD COLUMN IF NOT EXISTS license TEXT;

