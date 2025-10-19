-- Ratings for tutors and schools, plus indexes for leaderboards
CREATE TABLE IF NOT EXISTS tutor_ratings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tutor_user_id TEXT NOT NULL,
  rater_user_id TEXT NOT NULL,
  rating INTEGER NOT NULL CHECK (rating BETWEEN 1 AND 5),
  comment TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tutor_user_id, rater_user_id)
);

CREATE TABLE IF NOT EXISTS school_ratings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  school_id UUID NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
  rater_user_id TEXT NOT NULL,
  rating INTEGER NOT NULL CHECK (rating BETWEEN 1 AND 5),
  comment TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (school_id, rater_user_id)
);

-- Helpful indexes
CREATE INDEX IF NOT EXISTS idx_tutor_ratings_tutor ON tutor_ratings(tutor_user_id);
CREATE INDEX IF NOT EXISTS idx_school_ratings_school ON school_ratings(school_id);

-- Leaderboards are computed from test_attempts for tests with public visibility
CREATE INDEX IF NOT EXISTS idx_test_attempts_status ON test_attempts(status);
