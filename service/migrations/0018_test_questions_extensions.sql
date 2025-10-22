-- Extend test question types to support richer formats
ALTER TABLE test_questions DROP CONSTRAINT IF EXISTS test_questions_qtype_check;
ALTER TABLE test_questions ADD CONSTRAINT test_questions_qtype_check CHECK (
  qtype IN (
    'mcq',         -- multiple choice
    'truefalse',   -- true/false
    'short',       -- short/sentence answer
    'long',        -- long/essay answer
    'essay',       -- alias for long
    'sentence',    -- alias for short
    'numeric',     -- numeric response
    'formula',     -- math formula, structured
    'code',        -- code snippet answer
    'match',       -- matching items
    'ordering',    -- order items
    'fillblank',   -- fill-in-the-blank
    'hotspot',     -- image hotspot click areas
    'dragdrop'     -- drag-and-drop labeling/sorting
  )
);
