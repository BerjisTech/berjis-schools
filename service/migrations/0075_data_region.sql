-- Data region marker for schools (advisory; infra-level enforcement is external)

ALTER TABLE schools ADD COLUMN IF NOT EXISTS data_region TEXT NOT NULL DEFAULT 'us';

