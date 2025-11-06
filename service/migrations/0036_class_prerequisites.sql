-- Course prerequisites

CREATE TABLE IF NOT EXISTS class_prerequisites (
  class_id UUID NOT NULL REFERENCES classes(id) ON DELETE CASCADE,
  required_class_id UUID NOT NULL REFERENCES classes(id) ON DELETE CASCADE,
  PRIMARY KEY (class_id, required_class_id)
);

CREATE INDEX IF NOT EXISTS idx_class_prereq_required ON class_prerequisites(required_class_id);

