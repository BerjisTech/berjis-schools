-- Simple experiments bucketing
CREATE TABLE IF NOT EXISTS experiments (
  key TEXT PRIMARY KEY,
  variants JSONB NOT NULL DEFAULT '[]'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS experiment_assignments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  exp_key TEXT NOT NULL REFERENCES experiments(key) ON DELETE CASCADE,
  user_id TEXT NOT NULL,
  variant TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (exp_key, user_id)
);

