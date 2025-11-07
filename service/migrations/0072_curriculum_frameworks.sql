-- Curriculum frameworks and learning outcomes mapping

CREATE TABLE IF NOT EXISTS curriculum_frameworks (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  school_id UUID REFERENCES schools(id) ON DELETE SET NULL,
  region TEXT,                  -- e.g., US-CA, KE, UK, AU-NSW
  name TEXT NOT NULL,
  description TEXT,
  created_by_user_id TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS learning_outcomes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  framework_id UUID NOT NULL REFERENCES curriculum_frameworks(id) ON DELETE CASCADE,
  code TEXT,                   -- e.g., CCSS.MATH.CONTENT.3.OA.A.1
  description TEXT NOT NULL,
  grade_band TEXT,             -- optional: K-2, 3-5, etc.
  order_index INTEGER NOT NULL DEFAULT 0
);

-- Map lessons/tests to outcomes
CREATE TABLE IF NOT EXISTS lesson_outcomes (
  lesson_id UUID NOT NULL REFERENCES lessons(id) ON DELETE CASCADE,
  outcome_id UUID NOT NULL REFERENCES learning_outcomes(id) ON DELETE CASCADE,
  PRIMARY KEY (lesson_id, outcome_id)
);

CREATE TABLE IF NOT EXISTS test_outcomes (
  test_id UUID NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
  outcome_id UUID NOT NULL REFERENCES learning_outcomes(id) ON DELETE CASCADE,
  PRIMARY KEY (test_id, outcome_id)
);

