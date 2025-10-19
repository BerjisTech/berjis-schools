# Berjis Schools – Learning Platform (MVP Scope)

Berjis Schools enables tutors, students, and parents/guardians to participate in structured or independent learning. It supports school-managed classes and private tutoring, with rich lesson formats (text, video, audio, live) and collaborative learning experiences.

## Roles
- Tutors: Teach under a school and/or host private classes and publish content.
- Students: Attend school classes, join private tutoring, or self-study published content.
- Parents/Guardians: Manage and track children’s learning progress.

## Classes & Content
- Classes contain subjects; subjects contain lessons with chapters and tests.
- Tests can be organized by schools or privately by tutors.
- Lessons: text, video, audio, or live (audio-only or audio+video).
- Live modes: teacher-only broadcast, interactive group, or "learn together" rooms.
- Peer-to-peer study rooms for students (under home supervision) without teacher presence.
- Content may be free or paid (school or tutor posted).

## Initial Frontend
- Angular SPA using shared auth via core `api`.
- Surfaces dashboards for Tutors, Students, Parents.
- Stubs pages: Classes, Subjects, Lessons, Tests, Live Rooms, Library.

## Backend (Service)
- Go/Fiber service for domain APIs: schools, classes, subjects, lessons, tests, rooms.
- Integrates with core `api` for auth/session.
- Postgres database with migrations.

This document outlines the MVP scope to guide incremental implementation.