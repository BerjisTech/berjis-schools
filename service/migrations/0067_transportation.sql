-- Transportation management (routes, stops, trips, assignments)

CREATE TABLE IF NOT EXISTS transport_routes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  school_id UUID NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS transport_stops (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  route_id UUID NOT NULL REFERENCES transport_routes(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  lat NUMERIC(9,6),
  lng NUMERIC(9,6),
  order_index INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_transport_routes_school ON transport_routes(school_id);
CREATE INDEX IF NOT EXISTS idx_transport_stops_route ON transport_stops(route_id, order_index);

CREATE TABLE IF NOT EXISTS transport_assignments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  route_id UUID NOT NULL REFERENCES transport_routes(id) ON DELETE CASCADE,
  stop_id UUID NOT NULL REFERENCES transport_stops(id) ON DELETE RESTRICT,
  student_user_id TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(route_id, student_user_id, status) WHERE status='active'
);

CREATE INDEX IF NOT EXISTS idx_transport_assign_student ON transport_assignments(student_user_id, status);

CREATE TABLE IF NOT EXISTS transport_trips (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  route_id UUID NOT NULL REFERENCES transport_routes(id) ON DELETE CASCADE,
  start_time TIMESTAMPTZ NOT NULL,
  status TEXT NOT NULL DEFAULT 'planned' CHECK (status IN ('planned','started','completed','canceled')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_transport_trips_route ON transport_trips(route_id, start_time);

CREATE TABLE IF NOT EXISTS transport_checkins (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  trip_id UUID NOT NULL REFERENCES transport_trips(id) ON DELETE CASCADE,
  student_user_id TEXT NOT NULL,
  event TEXT NOT NULL CHECK (event IN ('pickup','dropoff')),
  at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_transport_checkins_trip ON transport_checkins(trip_id, student_user_id);

