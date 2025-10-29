-- Add status columns for soft-deletes and archiving

ALTER TABLE classes
  ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE classes
  DROP CONSTRAINT IF EXISTS classes_status_check;
ALTER TABLE classes
  ADD CONSTRAINT classes_status_check CHECK (status IN ('active','archived','deleted'));

ALTER TABLE subjects
  ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE subjects
  DROP CONSTRAINT IF EXISTS subjects_status_check;
ALTER TABLE subjects
  ADD CONSTRAINT subjects_status_check CHECK (status IN ('active','archived','deleted'));

ALTER TABLE lessons
  ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE lessons
  DROP CONSTRAINT IF EXISTS lessons_status_check_flag;
ALTER TABLE lessons
  ADD CONSTRAINT lessons_status_check_flag CHECK (status IN ('active','archived','deleted'));

ALTER TABLE tests
  ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE tests
  DROP CONSTRAINT IF EXISTS tests_status_check;
ALTER TABLE tests
  ADD CONSTRAINT tests_status_check CHECK (status IN ('active','archived','deleted'));
