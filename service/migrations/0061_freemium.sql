-- Freemium features configuration (per school and per user overrides)

CREATE TABLE IF NOT EXISTS school_feature_flags (
  school_id UUID NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
  feature_key TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  PRIMARY KEY (school_id, feature_key)
);

CREATE TABLE IF NOT EXISTS user_feature_flags (
  user_id TEXT NOT NULL,
  feature_key TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  PRIMARY KEY (user_id, feature_key)
);

