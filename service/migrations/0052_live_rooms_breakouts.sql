-- Breakout rooms: parent linkage
ALTER TABLE live_rooms ADD COLUMN IF NOT EXISTS parent_room_id UUID REFERENCES live_rooms(id) ON DELETE SET NULL;
ALTER TABLE live_rooms ADD COLUMN IF NOT EXISTS breakout_index INTEGER;
CREATE INDEX IF NOT EXISTS idx_live_rooms_parent ON live_rooms(parent_room_id);

