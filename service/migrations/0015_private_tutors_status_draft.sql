-- Allow draft status for private_tutors
ALTER TABLE private_tutors DROP CONSTRAINT IF EXISTS private_tutors_status_check;
ALTER TABLE private_tutors ADD CONSTRAINT private_tutors_status_check CHECK (status IN ('draft','pending','approved','rejected'));
