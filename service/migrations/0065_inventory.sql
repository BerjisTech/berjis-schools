-- Inventory tracking (books, equipment)

CREATE TABLE IF NOT EXISTS inventory_items (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  school_id UUID NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
  sku TEXT,
  name TEXT NOT NULL,
  category TEXT,
  attributes JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_inventory_items_school ON inventory_items(school_id);
CREATE INDEX IF NOT EXISTS idx_inventory_items_search ON inventory_items USING GIN (to_tsvector('english', coalesce(name,'') || ' ' || coalesce(category,'')));

CREATE TABLE IF NOT EXISTS inventory_movements (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  item_id UUID NOT NULL REFERENCES inventory_items(id) ON DELETE CASCADE,
  location TEXT,
  change INTEGER NOT NULL,
  reason TEXT,
  user_id TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_inventory_movements_item ON inventory_movements(item_id);
CREATE INDEX IF NOT EXISTS idx_inventory_movements_loc ON inventory_movements(item_id, location);

