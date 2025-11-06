-- AI settings per user and guardian controls
CREATE TABLE IF NOT EXISTS ai_settings (
  user_id TEXT PRIMARY KEY,
  ai_enabled BOOLEAN NOT NULL DEFAULT TRUE,
  content_level TEXT NOT NULL DEFAULT 'general' CHECK (content_level IN ('general','teen','mature')),
  guardian_controlled BOOLEAN NOT NULL DEFAULT FALSE,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

