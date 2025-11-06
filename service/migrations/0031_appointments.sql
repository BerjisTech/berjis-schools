-- Appointment slots and bookings for office hours

CREATE TABLE IF NOT EXISTS appointment_slots (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  class_id UUID NOT NULL REFERENCES classes(id) ON DELETE CASCADE,
  tutor_user_id TEXT NOT NULL,
  start_at TIMESTAMPTZ NOT NULL,
  end_at TIMESTAMPTZ NOT NULL,
  capacity INTEGER NOT NULL DEFAULT 1 CHECK (capacity >= 1 AND capacity <= 50),
  location_url TEXT,
  notes TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_appt_slots_class_start ON appointment_slots(class_id, start_at);

CREATE TABLE IF NOT EXISTS appointment_bookings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  slot_id UUID NOT NULL REFERENCES appointment_slots(id) ON DELETE CASCADE,
  booker_user_id TEXT NOT NULL,
  for_user_id TEXT,
  status TEXT NOT NULL DEFAULT 'booked' CHECK (status IN ('booked','cancelled')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  cancelled_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_appt_bookings_slot ON appointment_bookings(slot_id);
CREATE INDEX IF NOT EXISTS idx_appt_bookings_booker ON appointment_bookings(booker_user_id);
