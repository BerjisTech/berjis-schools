CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Canonical external resources tracked by Schools
CREATE TABLE IF NOT EXISTS resources (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  type TEXT NOT NULL CHECK (type IN ('docs','sheets','notes','pdf','slides')),
  external_id TEXT NULL,
  url TEXT NOT NULL,
  title TEXT NULL,
  owner_user_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_resources_type ON resources(type);
CREATE INDEX IF NOT EXISTS idx_resources_owner ON resources(owner_user_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_resources_type_external ON resources(type, external_id) WHERE external_id IS NOT NULL;

-- Attach resources to scopes (lesson, group, class, school, user)
CREATE TABLE IF NOT EXISTS resource_links (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  resource_id UUID NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  scope TEXT NOT NULL CHECK (scope IN ('lesson','group','class','school','user')),
  scope_id TEXT NOT NULL,
  -- Optional default role for the scope attachment (used by UI as fallback)
  default_role TEXT NULL CHECK (default_role IN ('owner','editor','commenter','viewer')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(resource_id, scope, scope_id)
);

CREATE INDEX IF NOT EXISTS idx_resource_links_scope ON resource_links(scope, scope_id);

-- Explicit ACL entries (principal can be a user, class, group, or school)
CREATE TABLE IF NOT EXISTS resource_acl (
  id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  resource_id UUID NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  principal_type TEXT NOT NULL CHECK (principal_type IN ('user','class','group','school')),
  principal_id TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('owner','editor','commenter','viewer')),
  created_by_user_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(resource_id, principal_type, principal_id)
);

CREATE INDEX IF NOT EXISTS idx_resource_acl_resource ON resource_acl(resource_id);
CREATE INDEX IF NOT EXISTS idx_resource_acl_principal ON resource_acl(principal_type, principal_id);

