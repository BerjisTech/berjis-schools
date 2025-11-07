-- Targeted indexes for common queries

-- Class enrollments by class and status
CREATE INDEX IF NOT EXISTS idx_class_enrollments_class_status ON class_enrollments(class_id, status);

-- Lessons by subject and status
CREATE INDEX IF NOT EXISTS idx_lessons_subject_status ON lessons(subject_id, status);

-- Lesson progress by lesson, student, status, updated_at
CREATE INDEX IF NOT EXISTS idx_lesson_progress_lesson_status ON lesson_progress(lesson_id, status);
CREATE INDEX IF NOT EXISTS idx_lesson_progress_student_updated ON lesson_progress(student_user_id, updated_at DESC);

-- Tests by subject/lesson and status
CREATE INDEX IF NOT EXISTS idx_tests_subject_status ON tests(subject_id, status);
CREATE INDEX IF NOT EXISTS idx_tests_lesson_status ON tests(lesson_id, status);

-- Test attempts by test and status
CREATE INDEX IF NOT EXISTS idx_test_attempts_test_status ON test_attempts(test_id, status);

