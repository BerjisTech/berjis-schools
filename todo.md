Schools TODO
============

Error Tracking
--------------
- Integrate error tracking for frontend and backend.
  - Backend (Go): add Sentry (github.com/getsentry/sentry-go) or OpenTelemetry + an exporter (OTLP to Grafana Tempo, or Sentry Performance) for traces + errors.
  - Frontend (Angular): add Sentry SDK (@sentry/angular-ivy) with source maps in CI; capture router errors and user context (role, school/class where applicable).
  - Anonymize PII and include only UUID user IDs in error context; add allowlist-based extra context (school_id, class_id, route).
  - Wire alerting to Slack/Email for `critical` severity.

Multilanguage / i18n
--------------------
- Expand UI translations from 2 to 10+ languages; target: en, fr, es, de, pt, ar (RTL), hi, sw, zh-Hans, ja.
- RTL support
  - Ensure `.dark` + Tailwind utilities use logical properties; validate layout with `dir="rtl"`.
  - Add direction switch based on chosen language; audit components for iconography/margins that assume LTR.
- Localization
  - Date/time/number/currency formatting via Angular i18n pipes; set locale dynamically.
  - Currency display per school/region; align with payments currency.
- Backend awareness
  - Accept `Accept-Language` on APIs where text is returned (e.g., error messages, certificate templates); default to English.
  - Store user language preference; already loaded from `localStorage`, add server profile fallback.

Dev Tasks
---------
- Add language packs under `schools/frontend/src/assets/i18n/` for the target list.
- Add runtime locale loader and set `registerLocaleData` dynamically.
- Add e2e smoke tests for RTL flows and date/currency formatting.
- CI: generate and upload source maps for error tracking; configure DSN via env.

