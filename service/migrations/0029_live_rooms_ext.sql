-- Extend live_rooms with provider metadata for 1:1 calls
ALTER TABLE live_rooms
  ADD COLUMN IF NOT EXISTS room_code TEXT,
  ADD COLUMN IF NOT EXISTS provider_url TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS uq_live_rooms_room_code ON live_rooms(room_code) WHERE room_code IS NOT NULL;
