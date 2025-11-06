-- Retention policies (configurable; enforcement done by maintenance jobs)

CREATE TABLE IF NOT EXISTS retention_policies (
  key TEXT PRIMARY KEY,
  value_days INTEGER NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Example defaults (optional)
INSERT INTO retention_policies(key, value_days) VALUES
  ('direct_messages.archive_after_days', 365)
ON CONFLICT (key) DO NOTHING;

