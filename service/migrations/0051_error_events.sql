-- Client and server error events for error tracking
CREATE TABLE IF NOT EXISTS error_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id TEXT,
  severity TEXT NOT NULL DEFAULT 'error',
  message TEXT,
  url TEXT,
  stack TEXT,
  context JSONB,
  user_agent TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_error_events_user ON error_events(user_id);
CREATE INDEX IF NOT EXISTS idx_error_events_created ON error_events(created_at);

