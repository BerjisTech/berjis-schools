-- Hostel/boarding management

CREATE TABLE IF NOT EXISTS hostels (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  school_id UUID NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  location TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS hostel_rooms (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  hostel_id UUID NOT NULL REFERENCES hostels(id) ON DELETE CASCADE,
  room_no TEXT NOT NULL,
  capacity INTEGER NOT NULL DEFAULT 1,
  gender TEXT,
  UNIQUE(hostel_id, room_no)
);

CREATE INDEX IF NOT EXISTS idx_hostels_school ON hostels(school_id);
CREATE INDEX IF NOT EXISTS idx_rooms_hostel ON hostel_rooms(hostel_id);

CREATE TABLE IF NOT EXISTS hostel_allocations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  room_id UUID NOT NULL REFERENCES hostel_rooms(id) ON DELETE CASCADE,
  student_user_id TEXT NOT NULL,
  start_date DATE NOT NULL DEFAULT CURRENT_DATE,
  end_date DATE,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','checked_out','canceled')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(room_id, student_user_id, status) WHERE status='active'
);

CREATE INDEX IF NOT EXISTS idx_allocations_room ON hostel_allocations(room_id, status);
CREATE INDEX IF NOT EXISTS idx_allocations_student ON hostel_allocations(student_user_id, status);

