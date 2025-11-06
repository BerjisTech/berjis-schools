-- School Management System core: academic years/terms, sections, students, timetable, attendance

-- Student records per school
CREATE TABLE IF NOT EXISTS student_records (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  school_id UUID NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
  student_user_id TEXT NOT NULL,
  admission_no TEXT,
  grade_level TEXT,
  status TEXT NOT NULL DEFAULT 'active',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (school_id, student_user_id)
);

CREATE INDEX IF NOT EXISTS idx_student_records_school ON student_records(school_id);

-- Academic years and terms
CREATE TABLE IF NOT EXISTS academic_years (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  school_id UUID NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  start_date DATE,
  end_date DATE,
  status TEXT NOT NULL DEFAULT 'planned' CHECK (status IN ('planned','active','completed','archived')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_academic_years_school ON academic_years(school_id);

CREATE TABLE IF NOT EXISTS school_terms (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  year_id UUID NOT NULL REFERENCES academic_years(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  start_date DATE,
  end_date DATE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_school_terms_year ON school_terms(year_id);

-- Sections and membership
CREATE TABLE IF NOT EXISTS school_sections (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  school_id UUID NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  grade_level TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_school_sections_school ON school_sections(school_id);

CREATE TABLE IF NOT EXISTS section_members (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  section_id UUID NOT NULL REFERENCES school_sections(id) ON DELETE CASCADE,
  student_user_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (section_id, student_user_id)
);

CREATE INDEX IF NOT EXISTS idx_section_members_section ON section_members(section_id);

-- Timetable entries
CREATE TABLE IF NOT EXISTS timetable_entries (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  school_id UUID NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
  class_id UUID REFERENCES classes(id) ON DELETE SET NULL,
  section_id UUID REFERENCES school_sections(id) ON DELETE SET NULL,
  subject_id UUID REFERENCES subjects(id) ON DELETE SET NULL,
  day_of_week SMALLINT NOT NULL CHECK (day_of_week BETWEEN 0 AND 6),
  start_time TIME NOT NULL,
  end_time TIME NOT NULL,
  room TEXT,
  created_by_user_id TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_timetable_school_day ON timetable_entries(school_id, day_of_week);

-- Attendance
CREATE TABLE IF NOT EXISTS attendance_records (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  school_id UUID NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
  class_id UUID REFERENCES classes(id) ON DELETE SET NULL,
  section_id UUID REFERENCES school_sections(id) ON DELETE SET NULL,
  day DATE NOT NULL,
  student_user_id TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('present','absent','late','excused')),
  recorded_by_user_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (class_id IS NOT NULL OR section_id IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_attendance_class_day ON attendance_records(class_id, day);
CREATE INDEX IF NOT EXISTS idx_attendance_section_day ON attendance_records(section_id, day);

-- Prevent duplicates within scope
CREATE UNIQUE INDEX IF NOT EXISTS uq_attendance_class ON attendance_records(day, student_user_id, class_id) WHERE class_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_attendance_section ON attendance_records(day, student_user_id, section_id) WHERE section_id IS NOT NULL;

