-- Adaptive testing support
-- Adds test mode and question difficulty; adaptive attempts state

-- tests.mode: 'standard' (default) or 'adaptive'
ALTER TABLE tests ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT 'standard' CHECK (mode IN ('standard','adaptive'));

-- question difficulty 1..5 (very easy .. very hard)
ALTER TABLE test_questions ADD COLUMN IF NOT EXISTS difficulty INTEGER NOT NULL DEFAULT 3 CHECK (difficulty BETWEEN 1 AND 5);

-- adaptive attempts track dynamic sequencing and scoring state
CREATE TABLE IF NOT EXISTS adaptive_attempts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  test_id UUID NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
  student_user_id TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'in_progress' CHECK (status IN ('in_progress','completed','abandoned')),
  total_questions INTEGER NOT NULL DEFAULT 15 CHECK (total_questions BETWEEN 5 AND 50),
  asked_question_ids UUID[] NOT NULL DEFAULT '{}',
  current_index INTEGER NOT NULL DEFAULT 0,
  current_difficulty INTEGER NOT NULL DEFAULT 3 CHECK (current_difficulty BETWEEN 1 AND 5),
  theta NUMERIC DEFAULT 0.0, -- simplistic ability estimate
  score NUMERIC,             -- final percent when completed
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (test_id, student_user_id)
);

CREATE INDEX IF NOT EXISTS idx_adaptive_attempts_user ON adaptive_attempts(student_user_id);
CREATE INDEX IF NOT EXISTS idx_adaptive_attempts_test ON adaptive_attempts(test_id);

