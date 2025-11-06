-- Peer reviews for classes
CREATE TABLE IF NOT EXISTS peer_reviews (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  class_id UUID NOT NULL REFERENCES classes(id) ON DELETE CASCADE,
  reviewer_user_id TEXT NOT NULL,
  rating INTEGER CHECK (rating BETWEEN 1 AND 5),
  comment TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (class_id, reviewer_user_id)
);

CREATE INDEX IF NOT EXISTS idx_peer_reviews_class ON peer_reviews(class_id);

