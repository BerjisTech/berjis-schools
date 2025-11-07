-- Library management system (catalog, copies, loans)

CREATE TABLE IF NOT EXISTS library_books (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  school_id UUID NOT NULL REFERENCES schools(id) ON DELETE CASCADE,
  isbn TEXT,
  title TEXT NOT NULL,
  author TEXT,
  subject TEXT,
  tags JSONB,
  description TEXT,
  cover_url TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_library_books_school ON library_books(school_id);
CREATE INDEX IF NOT EXISTS idx_library_books_title ON library_books USING GIN (to_tsvector('english', coalesce(title,'') || ' ' || coalesce(author,'')));

CREATE TABLE IF NOT EXISTS library_copies (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  book_id UUID NOT NULL REFERENCES library_books(id) ON DELETE CASCADE,
  barcode TEXT,
  status TEXT NOT NULL DEFAULT 'available' CHECK (status IN ('available','loaned','maintenance','lost')),
  UNIQUE(barcode)
);

CREATE INDEX IF NOT EXISTS idx_library_copies_book ON library_copies(book_id);

CREATE TABLE IF NOT EXISTS library_loans (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  copy_id UUID NOT NULL REFERENCES library_copies(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','returned','overdue')),
  loaned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  due_at TIMESTAMPTZ NOT NULL,
  returned_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_library_loans_user ON library_loans(user_id, status);
CREATE INDEX IF NOT EXISTS idx_library_loans_copy ON library_loans(copy_id, status);

