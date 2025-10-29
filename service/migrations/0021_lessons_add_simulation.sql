-- Allow simulations as a lesson type
ALTER TABLE lessons DROP CONSTRAINT IF EXISTS lessons_type_check;
ALTER TABLE lessons
  ADD CONSTRAINT lessons_type_check CHECK (type IN ('text','video','audio','live','simulation'));
