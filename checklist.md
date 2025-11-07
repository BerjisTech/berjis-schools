# Educational Platform System Requirements Checklist

## Purpose
This checklist verifies that an educational platform meets all functional requirements for a global, multi-modal learning system supporting independent and institutional users.

---

## 1. USER MANAGEMENT & AUTHENTICATION

### 1.1 User Types & Roles
- [x] System supports distinct user types: Student, Teacher/Tutor, Parent, School Admin, Platform Admin
- [x] Users can have multiple roles simultaneously (e.g., teacher at a school AND independent tutor)
- [x] Role-based access control (RBAC) is implemented for all features
- [x] User profiles distinguish between independent and school-affiliated status

### 1.2 Registration & Onboarding
- [x] Student registration flow exists (independent and school-based)
- [x] Teacher/Tutor registration with application/vetting workflow
- [x] School registration with institutional verification process
- [x] Parent registration with student linking capability
- [x] Email/phone verification system implemented
- [x] Multi-language support in registration forms

### 1.3 Vetting & Approval System
- [x] Independent teacher applications queue exists
- [x] School submission queue exists
- [x] Admin dashboard for reviewing applications
- [x] Document upload system for credentials/verification
 - [x] Approval/rejection workflow with notification system
- [x] Status tracking (Pending, Under Review, Approved, Rejected)
- [x] Reapplication mechanism for rejected applications
- [x] Background check integration capability (optional)

---

## 2. DUAL-MODE OPERATION (INDEPENDENT vs SCHOOL-BASED)

### 2.1 User Affiliation Management
 - [x] Students can enroll as independent OR join a school
 - [x] Students can belong to multiple schools simultaneously
 - [x] Teachers can operate independently AND be employed by schools
 - [x] System tracks primary and secondary affiliations
 - [x] Affiliation change/transfer mechanism exists

### 2.2 Content & Resource Management
- [x] Courses can be created as independent OR school-specific
- [x] Tests can be independent, school-specific, or shared
- [x] Certifications support independent and institutional issuance
- [x] Visibility controls (public, school-only, private)
- [x] Content ownership and licensing tracking

---

## 3. EDUCATIONAL CONTENT MANAGEMENT

### 3.1 Course System
- [x] Course creation interface for teachers
- [x] Course catalog/marketplace for independent courses
- [x] School-specific course library
- [x] Course enrollment mechanism (paid and free)
- [x] Curriculum/syllabus builder
- [x] Multi-format content support (video, documents, interactive)
- [x] Course prerequisites and progression tracking
- [x] Course versioning and updates

### 3.2 Assessment System
- [x] Test/quiz creation tools
- [x] Multiple question types (MCQ, essay, practical, etc.)
- [x] Automated and manual grading options
- [x] Test scheduling and proctoring features
 - [x] Grade book and transcript generation
- [x] Performance analytics and reporting
- [ ] Adaptive testing capability (optional)

### 3.3 Certification System
- [x] Certificate template designer
- [x] Automated certificate generation on completion
- [x] Digital certificate verification system
- [x] Independent certification issuance
- [x] School-branded certification
- [x] Certificate revocation mechanism
- [x] Blockchain/cryptographic verification (optional)

---

## 4. COLLABORATION & COMMUNICATION FEATURES

### 4.1 Student Collaboration
- [x] Student-to-student discussion forums
- [x] Study group creation and management
- [x] Collaborative document editing
  - [x] Use existing Berjis tools (no new editors): docs.berjis.tech, sheets.berjis.tech, notes.berjis.tech, pdf.berjis.tech, slides.berjis.tech
  - [x] SSO via Core API; seamless auth from Schools app to editors (via `/v1/resources/:id/open` and `/v1/sso/redirect`)
  - [x] Permissions model (map to class/group/school roles) wired in Schools
    - [x] Owner: full control (enforced in editor services)
    - [x] Editor: edit content (enforced in editor services)
    - [x] Commenter/Annotator: can leave annotations/notes (PDF/Slides endpoints)
    - [x] Viewer: view-only (read access gated)
  - [x] ACL sources supported: Class, Study Group, School, Individual share
  - [x] PDF: annotations enabled for Commenter; viewers view-only (server-enforced)
  - [x] Slides: notes enabled for Commenter; viewers view-only (server-enforced)
  - [x] Share dialogs respect unified roles; revocation propagates
- [x] Share modal in Schools lists current ACL and allows revoke (Lessons, Groups, Class panels)
- [x] Audit: who changed what (editor history visible; no custom history in Schools)
- [x] Peer review and feedback system
- [x] Version control for collaborative work
  - Acceptance criteria:
    - For editor-backed resources (Docs, Sheets, Notes, PDF, Slides), users can open the resource and a dedicated history view via link-out.
    - Endpoint: GET /v1/resources/{id}/history returns SSO-redirected open_url and history_url that take the user into the editor’s revision history.
    - Actual revision storage and diffs live in the editor services; Schools integrates by linking and managing ACL.
- [x] Group project management tools

### 4.2 One-on-One Communication
- [x] Teacher-student private messaging
- [x] Parent-teacher private messaging
- [x] Video call integration (1-on-1)
- [x] Appointment/office hours scheduling
- [x] File sharing in private conversations
- [x] Conversation history and archiving

### 4.3 Group Communication
- [x] Class/group discussion boards
- [x] Group video conferencing
- [x] Announcement system (broadcast messaging)
- [x] Parent-teacher group meetings
- [x] Moderation tools for group discussions
- [x] Breakout room functionality

---

## 5. AI HELPER INTEGRATION

### 5.1 Core AI Features
- [x] AI chatbot accessible throughout the platform
- [x] Context-aware assistance (knows user role and current activity)
- [x] Multi-language AI support
- [ ] Homework help and tutoring
- [ ] Study material generation
- [x] Question answering system

### 5.2 AI Safety & Limitations
- [x] Content filtering for inappropriate requests
- [x] Academic integrity safeguards (prevents complete assignment solutions)
- [x] Age-appropriate responses
- [x] AI usage logging and monitoring
- [x] Parental controls for AI access
- [x] Opt-out capability for AI features

### 5.3 AI Enhancement Features
- [ ] Personalized learning path recommendations
- [ ] Automated content summarization
- [ ] Language translation
- [ ] Accessibility features (text-to-speech, etc.)
- [ ] Practice question generation
- [ ] Progress insights and suggestions

---

## 6. SCHOOL MANAGEMENT SYSTEM (SMS)

### 6.1 Administrative Functions
- [x] Student information system (SIS)
- [x] Teacher/staff management
- [x] Class and section management
- [x] Academic year/term configuration
- [x] Timetable/schedule management
- [x] Attendance tracking system
- [x] Grade management and report cards

### 6.2 Financial Management
- [x] Fee structure configuration
  - Acceptance criteria:
    - Admins can define per-school fee structures with name/amount/currency/interval; active flag supported.
- [x] Payment collection and tracking
  - Acceptance criteria:
    - Invoices can be created for students and marked paid via Core billing callback; status transitions persisted.
- [x] Invoice generation
  - Acceptance criteria:
    - Invoices have items, totals, due date, and currency; listable per student.
- [x] Financial reporting
  - Acceptance criteria:
    - Summary endpoint provides invoiced/paid/outstanding totals by currency over a date range.
- [x] Scholarship/discount management
  - Acceptance criteria:
    - Admins can create student scholarships (percent/amount, optional window); applied automatically at invoice creation.
- [x] Multi-currency support
  - Acceptance criteria:
    - Fee structures, invoices, and reports carry currency fields; discounts respect matching currency.

### 6.3 Resource Management
- [x] Classroom and facility booking
  - Acceptance criteria:
    - Admins can create facilities (name/location/capacity) per school; members can list active facilities.
    - Members can book a facility for a time range; server enforces conflict checks and supports cancelation by booker or school admin.
    - Availability endpoint lists booked slots over a date range; my bookings endpoint shows user’s bookings per school.
- [x] Library management system
  - Acceptance criteria:
    - Catalog per school with books, copies, and basic search by title/author; admin can add books/copies.
    - Members can borrow available copies and return; loans track due dates and status; my loans endpoint lists current/history.
    - Copy status updates on loan/return; minimal conflict checks enforced.
- [x] Inventory tracking (books, equipment)
  - Acceptance criteria:
    - Admins can create inventory items per school with SKU, name, category, attributes; members can list items with computed stock.
    - Stock is tracked via movement records (positive/negative deltas) with reason/location; API exposes per‑location stock and adjustments.
    - Endpoints: POST/GET /v1/schools/{id}/inventory/items, POST /v1/inventory/items/{id}/movements, GET /v1/inventory/items/{id}/stock.
- [x] Transportation management (optional)
  - Acceptance criteria:
    - Admins can define transport routes and stops, assign students to stops, and schedule trips; members can view routes.
    - Trip manifests list assigned students per route; admin can record pickup/dropoff check-ins during a trip.
    - Endpoints: POST/GET /v1/schools/{id}/transport/routes, POST /v1/transport/routes/{id}/stops, POST /v1/transport/routes/{id}/assign, POST /v1/transport/routes/{id}/trips, GET /v1/transport/trips/{id}/manifest, POST /v1/transport/trips/{id}/checkin, GET /v1/me/transport/assignments.
- [x] Hostel/boarding management (optional)
  - Acceptance criteria:
    - Admins can create hostels and rooms with capacity; occupancy is enforced when allocating students.
    - Members can view hostels with occupancy summary; students can see their current allocation and check out (or admin can check them out).
    - Endpoints: POST/GET /v1/schools/{id}/hostels, POST /v1/hostels/{id}/rooms, POST /v1/rooms/{id}/allocate, POST /v1/allocations/{id}/checkout, GET /v1/me/hostel.

### 6.4 Communication & Engagement
- [x] School-wide announcements
  - Acceptance criteria:
    - Admins and tutors can post announcements at the school level with title/body, visibility (school/public), and optional pin.
    - Members see school announcements; non-members can see public ones. User feed aggregates announcements from all schools the user belongs to.
- [x] Event calendar and management
  - Acceptance criteria:
    - Events can be created per school with title/description/location, start/end times, and visibility (school/public).
    - Members can view all school events; non-members see public events. Users can RSVP (going/interested/declined).
    - Admin or event creator can update/delete events; listings support range filters and pagination.
- [x] Parent portal access
  - Acceptance criteria:
    - Guardians can link children and view child progress, tests, and class enrollments; endpoints guard via guardian-child linkage.
    - Guardians can view and pay invoices (Core billing via Core payment intent parameters) and see appointment bookings for their child.
    - Messaging between guardian and tutors is allowed where the child is enrolled, per existing permission checks.
- [x] Teacher portal access
  - Acceptance criteria:
    - Tutors can create/manage classes, subjects, lessons, tests; view enrollments and gradebook.
    - Tutors can open appointment slots; students/guardians can book; tutors can message students/guardians.
    - Tutor analytics panels available for classes (student counts, progress summaries).
- [x] SMS/email notification system
  - Acceptance criteria:
    - System enqueues notifications to a DB queue and provides an admin endpoint to process and send emails and SMS.
    - SMTP configuration via env enables email sending; without SMTP, messages are logged. SMS provider (Twilio) enabled via env; otherwise logged.
    - Notification types (approvals, rejections, messages, announcements, call invites) render simple subject/body from payload; optional phone in payload triggers SMS.
- [ ] Mobile app for parents and students

---

## 7. GLOBAL & MULTI-SYSTEM SUPPORT

### 7.1 Internationalization (i18n)
- [ ] Multi-language interface (minimum 10 major languages)
- [ ] RTL (Right-to-Left) language support
- [ ] Localized date/time formats
- [ ] Currency localization
- [ ] Regional academic terminology support

### 7.2 Educational System Flexibility
- [x] Configurable grading scales (percentage, GPA, letter grades, etc.)
  - Acceptance criteria:
    - Admins can define named grading scales per school with entries mapping percent ranges to letters (and optional points).
    - Schools can set a default active scale; gradebook API uses the default scale to compute and return letter grades.
    - Endpoints: POST/GET /v1/schools/{id}/grading-scales, PATCH /v1/grading-scales/{id}; gradebook output includes a letter field.
- [ ] Multiple academic year structures (semester, trimester, quarter)
- [ ] Configurable grade/year levels
- [ ] Support for different age ranges and naming (K-12, Year 1-13, etc.)
- [ ] Custom curriculum frameworks
- [ ] Regional accreditation standards integration

### 7.3 Legal & Compliance
- [ ] GDPR compliance (EU)
- [ ] COPPA compliance (US - children's privacy)
- [ ] Data localization options
- [ ] Accessibility standards (WCAG 2.1 AA minimum)
- [ ] Terms of service and privacy policy per region
- [ ] Parental consent mechanisms for minors

---

## 8. TECHNICAL INFRASTRUCTURE

### 8.1 Architecture & Performance
- [ ] Scalable cloud infrastructure
- [ ] Load balancing implemented
- [ ] CDN for global content delivery
- [ ] Database optimization and indexing
- [ ] Caching strategy implemented
- [ ] API rate limiting

### 8.2 Security
- [ ] HTTPS/TLS encryption
- [ ] Two-factor authentication (2FA)
- [ ] Password strength enforcement
- [ ] Session management and timeout
- [ ] SQL injection prevention
- [ ] XSS and CSRF protection
- [ ] Regular security audits
- [ ] Data encryption at rest and in transit

### 8.3 Data Management
- [ ] Regular automated backups
- [ ] Disaster recovery plan
- [x] Data export functionality for users
- [x] Data deletion/right to be forgotten
- [x] Audit logging for sensitive operations
- [x] Data retention policies

---

## 9. PAYMENT & MONETIZATION

### 9.1 Payment Processing
- [x] Multiple payment gateway integration
  - Acceptance criteria:
    - Can create a Core payment intent with `provider` set to `mpesa`, `stripe`, or `flutterwave` and receive a `next_action` suitable for the UI.
    - Provider availability is controlled solely via Core env; the default provider is used when none is supplied.
    - A webhook endpoint exists per enabled provider to update intent status to `succeeded`/`failed`.
- [x] Support for major credit/debit cards
  - Acceptance criteria:
    - Card payments are initiated via `stripe` or `flutterwave` providers through Core payment intents.
    - A successful card flow updates the intent status in Core and is observable via GET `/v1/billing/payment-intents/{id}`.
- [ ] Digital wallet support (PayPal, etc.)
- [ ] Regional payment methods (UPI, Alipay, etc.)
- [x] Subscription management
  - Acceptance criteria:
    - Apps can create, list, and cancel subscriptions through Core: GET/POST `/v1/billing/subscriptions`, PATCH `/{id}/cancel`.
    - `product_key` ties Core subscription rows to app plans; status transitions `active` → `canceled` are reflected in app entitlements.
- [x] One-time payment for courses
  - Acceptance criteria:
    - A Core payment intent can be created for a single course using a descriptive `description` (e.g., `schools:class:{id}`).
    - On success, the Schools service grants access/enrollment exactly once (idempotent fulfillment).
- [x] Refund processing system
  - Acceptance criteria:
    - Refunds are recorded as payment transactions linked to a Core intent; provider-side API calls are optional per integration.
    - Schools service reverses access/entitlements on refund and persists an audit note.

### 9.2 Revenue Models
- [x] Commission system for independent course sales
  - Acceptance criteria:
    - Commission calculation occurs in the Schools service (not Core) and is persisted in its DB/settlements.
    - Reports expose gross, net, and commission amounts per sale/period.
- [x] School subscription tiers
  - Acceptance criteria:
    - Plan definitions and pricing live in Schools service; Core subscription rows reference these via `product_key`.
    - Access gates/features honor tier; grace period/cancellation behavior is enforced by Schools service.
- [ ] Freemium features configuration
- [x] Freemium features configuration
  - Acceptance criteria:
    - Admins can enable/disable named features per school via API; users can fetch effective features considering school flags and user overrides.
    - Endpoints: GET /v1/features (optionally by school), POST /v1/schools/{id}/features.
    - Flags are stored in dedicated tables and can be extended without code changes (free-form feature_key strings).
- [x] Promotional codes and discounts
  - Acceptance criteria:
    - Discounts are applied in Schools service before creating a Core payment intent; the final charged amount matches discounted pricing.
    - Promo usage is audited (who, when, code, amount) in Schools service.
- [x] Revenue sharing for partnered content
  - Acceptance criteria:
    - Partner shares are configured in Schools service; settlements reflect split amounts; export/report supports partner reconciliation.

### 9.3 Core Billing Integration (Unified)
- [x] Use Core API for cross‑app billing primitives (payment intents, subscriptions)
  - Endpoints (Core):
    - POST/GET `/v1/billing/payment-intents`
    - GET/POST `/v1/billing/subscriptions`
    - PATCH `/v1/billing/subscriptions/{id}/cancel`
  - Providers via env: `mpesa` (default), `stripe`, `flutterwave` (configure keys in Core API env)
  - App responsibilities (Schools service): plan catalogs, pricing, entitlements, and any domain‑specific webhooks/fulfillment remain in `schools/service` and DB.
- [x] Schools frontend checkout uses Core payment intents; description/product_key maps to school domain objects.
  - Acceptance criteria:
    - Core endpoints enforce auth and return `success: true` with expected data contracts; Landing billing screen can create a test intent.
    - Schools checkout uses Core (no direct provider calls from frontend); app-specific DB never stores Core payment primitives.
    - App fulfillment logic (grant/revoke access) exists only in Schools service and is triggered from Core status changes.

---

## 10. MONITORING & ANALYTICS

### 10.1 User Analytics
- [x] Student progress tracking dashboards
- [x] Teacher performance metrics
- [x] School-wide analytics
- [x] Platform usage statistics
- [x] Engagement metrics
- [x] Completion rates and outcomes

### 10.2 System Monitoring
- [x] Uptime monitoring
- [x] Error tracking and logging
- [x] Performance metrics (page load, API response times)
- [x] User feedback collection system
- [x] Bug reporting mechanism
- [x] A/B testing capability

---

## 11. MOBILE SUPPORT

- [x] Responsive web design for all devices
- [x] Native iOS app (or PWA)
- [x] Native Android app (or PWA)
- [x] Offline mode for content access
- [ ] Push notifications
- [x] Mobile-optimized video player
- [ ] Touch-optimized interfaces

---

## 12. ACCESSIBILITY

- [x] Screen reader compatibility
- [x] Keyboard navigation support
- [x] Adjustable font sizes
- [x] High contrast mode
- [x] Closed captioning for videos
- [x] Alt text for images
- [x] Color-blind friendly design
- [x] Focus indicators for interactive elements

---

## VERIFICATION INSTRUCTIONS FOR CODE GENERATORS

When analyzing the existing codebase:

1. **Search for key components**: Look for classes, models, routes, or components matching each requirement
2. **Check database schema**: Verify tables/collections exist for user types, courses, schools, etc.
3. **Review API endpoints**: Ensure CRUD operations exist for all major features
4. **Examine authentication/authorization**: Verify role-based access is implemented
5. **Test user flows**: Simulate registration, vetting, enrollment, and collaboration workflows
6. **Check integration points**: Verify AI, payment, and communication services are connected
7. **Review configuration files**: Ensure multi-language, multi-currency, and regional settings exist
8. **Audit security measures**: Confirm encryption, authentication, and compliance features are implemented

For each unchecked item, generate:
- Required database migrations/schema changes
- API endpoints needed
- Frontend components/pages
- Service layer logic
- Tests for the functionality

Priority levels:
- **CRITICAL**: Items 1-6, 8.2 (core functionality and security)
- **HIGH**: Items 7, 9, 10 (global support and business operations)
- **MEDIUM**: Items 8.1, 8.3, 11 (performance and mobile)
- **LOW**: Items 12 (accessibility enhancements)

---

## OUTPUT FORMAT

Generate a report in this format:

```
FEATURE AUDIT REPORT
===================

✅ IMPLEMENTED: [Feature name]
   - Location: [file path or component name]
   - Status: [Fully functional | Partially functional]
   - Notes: [Any observations]

❌ MISSING: [Feature name]
   - Required for: [User type or workflow]
   - Priority: [Critical | High | Medium | Low]
   - Implementation needed: [Brief description]
   - Estimated effort: [Small | Medium | Large]

⚠️ INCOMPLETE: [Feature name]
   - What exists: [Description]
   - What's missing: [Description]
   - Priority: [Critical | High | Medium | Low]
```

Then provide a prioritized implementation roadmap for missing features.

