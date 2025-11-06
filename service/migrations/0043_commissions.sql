-- Commission and revenue share settings

CREATE TABLE IF NOT EXISTS commission_settings (
  id SMALLINT PRIMARY KEY DEFAULT 1,
  platform_percent INTEGER NOT NULL DEFAULT 20,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO commission_settings(id, platform_percent)
  VALUES (1, 20)
ON CONFLICT (id) DO NOTHING;

-- Per-class overrides
CREATE TABLE IF NOT EXISTS class_commissions (
  class_id UUID PRIMARY KEY REFERENCES classes(id) ON DELETE CASCADE,
  platform_percent INTEGER NOT NULL CHECK (platform_percent >= 0 AND platform_percent <= 100),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Optional partner revenue shares (percent of gross, before platform cut)
CREATE TABLE IF NOT EXISTS class_revenue_shares (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  class_id UUID NOT NULL REFERENCES classes(id) ON DELETE CASCADE,
  partner_user_id TEXT NOT NULL,
  percent INTEGER NOT NULL CHECK (percent >= 0 AND percent <= 100),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(class_id, partner_user_id)
);

