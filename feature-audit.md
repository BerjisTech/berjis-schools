FEATURE AUDIT REPORT
===================

Date: 2025-11-06
Scope: schools (frontend Angular, backend Go) integrated with core `api` for auth.

✅ IMPLEMENTED: Unified auth (verify/refresh, login redirect)
   - Location: schools/frontend/src/app/util.ts; schools/service/internal/server/server.go
   - Status: Fully functional
   - Notes: Uses core API endpoints `/v1/auth/verify` and `/v1/auth/refresh` with cookies; `loginUrl()` points to landing `/auth/login`.

✅ IMPLEMENTED: Roles (student, tutor, parent/guardian, school admin, platform admin)
   - Location: schools/service/migrations/0002_schema.sql; 0012_moderation.sql
   - Status: Fully functional
   - Notes: RBAC enforced via helpers in `internal/auth/auth.go` and route checks.

✅ IMPLEMENTED: Multi‑role support per user
   - Location: schools/service/migrations/0002_schema.sql (UNIQUE (school_id, user_id, role))
   - Status: Fully functional

✅ IMPLEMENTED: Vetting & approvals (tutors and schools)
   - Location: schools/service/internal/server/server.go (/v1/tutors/*, /v1/schools/*app*);
              schools/frontend/src/app/pages/become-tutor.page.* and create-school.page.*
   - Status: Fully functional
   - Notes: Draft → pending → approved/rejected; re‑apply supported. Uploads via `/v1/uploads` with size/type guards.

✅ IMPLEMENTED: Notifications queue on approvals/rejections
   - Location: migrations/0023_notifications.sql; server inserts into `notifications_queue`
   - Status: Fully functional (queue level)
   - Notes: Outbound delivery (email/SMS/webhooks) can consume the queue; not included in this change.

✅ IMPLEMENTED: Parent/guardian linking
   - Location: schools/service/internal/server/server.go (/v1/guardians/*)
   - Status: Fully functional
   - Notes: Guardians can link children and view progress.

✅ IMPLEMENTED: Dual‑mode (independent vs school)
   - Location: classes.school_id NULLable; visibility flags
   - Status: Fully functional
   - Notes: Users can belong to multiple schools; tutors can be independent and school‑employed.

✅ IMPLEMENTED: Primary/secondary affiliation and transfer
   - Location: migrations/0024_affiliations_primary.sql; server /v1/affiliations[/*]
   - Status: Fully functional
   - Notes: Unique primary per user+role enforced via partial index; endpoints to list, set primary, and transfer.

✅ IMPLEMENTED: Courses, subjects, lessons CRUD
   - Location: schools/service/internal/server/server.go; migrations 0002, 0021, 0022
   - Status: Fully functional
   - Notes: Lesson types: text, audio, video, live, simulation; progress tracking present.

✅ IMPLEMENTED: Assessments with rich question types
   - Location: migrations/0018_test_questions_extensions.sql; server routes /v1/tests/*
   - Status: Fully functional
   - Notes: Auto scoring for objective types; grading JSON stored with attempts.

✅ IMPLEMENTED: Manual grading API
   - Location: server /v1/tests/:id/attempts (list), /v1/tests/:id/attempts/:attemptId/grade
   - Status: Fully functional (API level)
   - Notes: Tutor/school admin only; sets grading JSON, score, and status (graded/returned).

✅ IMPLEMENTED: Class grade book (API + UI)
   - Location: server /v1/classes/:id/gradebook and /v1/classes/:id/gradebook.csv; frontend pages/gradebook
   - Status: Fully functional
   - Notes: Aggregates students, tests, earned/max, percent; CSV export.

✅ IMPLEMENTED: Student transcripts (JSON + PDF)
   - Location: server /v1/transcripts/:userId and /v1/transcripts/:userId.pdf; frontend pages/transcript
   - Status: Fully functional
   - Notes: Permissions support self, guardian, platform admin; school admin when school_id provided. PDF generated via gofpdf.

❌ MISSING: Certification issuance/verification
   - Required for: Course completion recognition (students, schools)
   - Priority: High
   - Implementation needed: Certificate templates, issuance API, verification endpoint, UI
   - Estimated effort: Medium–Large

❌ MISSING: Collaboration (forums, messaging, group calls)
   - Required for: Sections 4.1–4.3
   - Priority: High
   - Implementation needed: Integrate Communities app modules or add scoped discussions per class/school; private messaging; group announcements
   - Estimated effort: Medium–Large

Notes
-----
- Email/phone verification and registration flows are provided by the shared Landing + Core API and are integrated here (login redirect + session verify).
- Styles follow `ui-guidelines.md` (light‑first, `.dark` toggling in `app.component`). Build warnings in `styles.css` about `@import` ordering do not affect functionality.

Roadmap (near‑term)
-------------------
1) Add notifications (email/webhook) for tutor/school approvals (High)
2) Manual grading endpoints + UI, class grade book (High)
3) Certificates: templates, issuance, verification (High)
4) Primary affiliation flag + transfer workflow (Medium)
5) Collaboration MVP: class announcements + discussion threads (High)
✅ IMPLEMENTED: Certificates (templates, issue, verify, revoke)
   - Location: migrations/0025_certificates.sql; server /v1/certificates/*; frontend /certificates
   - Status: Fully functional (manual issuance)
   - Notes: Platform or school-scoped templates; code-based verification; PDF download. Automation on course completion is not yet implemented.

✅ IMPLEMENTED: Automated issuance on completion (rule-based)
   - Location: migrations/0026_certificate_rules.sql; server evaluateCerts() + triggers on submit/grade/progress; frontend rules in /certificates
   - Status: Fully functional
   - Notes: Per-class or per-school rules with conditions (minPercent, requireAllTestsCompleted, minLessonsCompleted); enable/disable controls.

✅ IMPLEMENTED: 1:1 messaging (teacher–student, parent–teacher)
   - Location: migrations/0028_messages.sql; server /v1/messages/*; frontend /messages
   - Status: Fully functional
   - Notes: Threads scoped by class for permission checks; notifications enqueued; file sharing via upload URLs in attachments.

✅ IMPLEMENTED: Appointments / Office Hours
   - Location: migrations/0031_appointments.sql; server /v1/classes/:id/appointments/* and /v1/appointments/*; frontend /class/:id/appointments
   - Status: Fully functional
   - Notes: Tutor creates slots; students/guardians book; cancel and my bookings endpoints available.

? IMPLEMENTED: Adaptive testing (optional)
   - Location: migrations/0070_adaptive_tests.sql; server /v1/tests/:id/start-adaptive, /v1/adaptive/:attemptId/{next,answer,finish}
   - Status: Fully functional (simple difficulty ladder)
   - Notes: 1–5 difficulty bands; naive theta update +/-0.5; targets 15 questions by default.

? IMPLEMENTED: AI utilities (summarize/translate/practice/learning path/TTS)
   - Location: server /v1/ai/{summarize,translate,practice,path,tts}; frontend AiService methods
   - Status: Fully functional (proxied via /v1/ai/chat with safety checks)
   - Notes: Honors AI settings + guardian controls; language forwarded via Accept-Language.

? IMPLEMENTED: Grade/year levels
   - Location: migrations/0071_grade_levels.sql; server /v1/schools/:id/grade-levels, PATCH /v1/grade-levels/:id
   - Status: Fully functional
   - Notes: Provides code/name/order and min/max age for regional schemes.

? IMPLEMENTED: Curriculum frameworks and outcomes
   - Location: migrations/0072_curriculum_frameworks.sql; server frameworks/outcomes endpoints
   - Status: Fully functional (CRUD + mapping to lessons/tests)

? IMPLEMENTED: Regional accreditations
   - Location: migrations/0073_accreditation.sql; server /v1/schools/:id/accreditations
   - Status: Fully functional

? IMPLEMENTED: Legal docs, user/parental consents
   - Location: migrations/0074_consents.sql; server /v1/legal/*
   - Status: Fully functional (records consents; COPPA workflow for guardian approval)
   - Notes: GDPR/COPPA policy enforcement beyond data capture remains broader org compliance.

? IMPLEMENTED: API rate limiting + security headers
   - Location: server middleware (helmet + limiter; optional CSRF via env)
   - Status: Fully functional
   - Notes: TLS/2FA/password strength handled by Core API and infra; CSRF opt-in to avoid breaking cross-origin SPA until clients set header.
