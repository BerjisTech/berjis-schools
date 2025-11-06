import { Injectable } from '@angular/core';
import { urlFor, verifySession } from '../../app/util';
import { Course, Lesson, Subject, TestItem, TestQuestion, UserRef } from '../interfaces/course';
import { SchoolInvite, SchoolOverview, SchoolSummary } from '../interfaces/school';
type JSONObject = Record<string, unknown>;
type JSONRecord = Record<string, unknown>;

interface ClassItemApi {
  id: string;
  title: string;
  description?: string | null;
  tutorUserId?: string;
  visibility?: string;
  isPaid?: boolean;
  priceCents?: number;
  studentCount?: number;
  subjectCount?: number;
  lessonCount?: number;
  testCount?: number;
  schoolId?: string | null;
  status?: string;
  isEnrolled?: boolean;
}
interface SubjectApi { id: string; classId: string; title: string; description?: string | null; orderIndex: number; status?: string }
interface LessonApi { id: string; subjectId: string; title: string; type: string; content?: any; orderIndex: number; isFree: boolean; status?: string }
interface TestApi { id: string; schoolId?: string | null; subjectId?: string | null; lessonId?: string | null; title: string; description?: string | null; visibility: string; status?: string; createdByUserId?: string; createdAt?: string }
interface TestWithQuestionsApi { test: TestApi; questions: any[] }

@Injectable({ providedIn: 'root' })
export class SchoolsService {
  private api = urlFor('schools-api');
  private currentUserIdCache: string | null | undefined;

  async listCourses(): Promise<Course[]> {
    const res = await fetch(`${this.api}/v1/classes`, { credentials: 'include' });
    const j = await res.json();
    const rows: ClassItemApi[] = j?.data ?? [];
    return rows.map(this.mapClassToCourse);
  }

  async listMyCourses(): Promise<Course[]> {
    const res = await fetch(`${this.api}/v1/classes?mine=1`, { credentials: 'include' });
    const j = await res.json();
    const rows: ClassItemApi[] = j?.data ?? [];
    return rows.map(this.mapClassToCourse);
  }

  async getCourse(id: string): Promise<Course | null> {
    // There is no dedicated "get class by id" endpoint; fetch list and filter for now.
    const list = await this.listCourses();
    return list.find(c => `${c.id}` === id) ?? null;
  }

  async createCourse(input: { title: string; description?: string; isPaid?: boolean; priceCents?: number; schoolId?: string | null; visibility?: 'school'|'private'|'public' }): Promise<Course> {
    const body = {
      title: input.title,
      description: input.description ?? null,
      isPaid: !!input.isPaid,
      priceCents: input.priceCents ?? 0,
      schoolId: input.schoolId ?? null,
      visibility: input.visibility ?? 'public',
    } as any;
    const res = await fetch(`${this.api}/v1/classes`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    const j = await res.json();
    const row: ClassItemApi = j?.data;
    return this.mapClassToCourse(row);
  }

  async enrollInCourse(courseId: string): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/classes/${courseId}/enroll`, { method: 'POST', credentials: 'include' });
    return !!(await res.json())?.success;
  }

  async checkout(courseId: string, opts: { gateway?: 'stripe'|'flutterwave'|'mpesa'; mode?: 'hosted'; currency?: string; successUrl?: string; cancelUrl?: string; phone?: string } = {}): Promise<{ url?: string; paymentId?: string } | null> {
    const body: any = {
      classId: courseId,
      gateway: opts.gateway ?? (localStorage.getItem('payments.gateway') as any) ?? 'stripe',
      currency: opts.currency ?? 'USD',
      mode: opts.mode ?? 'hosted',
      successUrl: opts.successUrl ?? window.location.origin + '/classes',
      cancelUrl: opts.cancelUrl ?? window.location.href,
      phone: opts.phone ?? localStorage.getItem('payments.mpesaPhone') ?? undefined
    };
    const res = await fetch(`${this.api}/v1/payments/checkout`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    if (!res.ok) return null;
    const j = await res.json();
    return j?.data ?? null;
  }

  async listSubjects(classId: string): Promise<Subject[]> {
    const res = await fetch(`${this.api}/v1/subjects?class_id=${encodeURIComponent(classId)}`, { credentials: 'include' });
    const j = await res.json();
    const rows: SubjectApi[] = j?.data ?? [];
    return rows.map(r => ({ id: r.id, classId: r.classId, title: r.title, description: r.description ?? undefined, orderIndex: r.orderIndex, status: r.status as any }));
  }

  async createSubject(input: { classId: string; title: string; description?: string; orderIndex?: number }): Promise<Subject> {
    const body = { classId: input.classId, title: input.title, description: input.description ?? null, orderIndex: input.orderIndex ?? 0 };
    const res = await fetch(`${this.api}/v1/subjects`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    const j = await res.json();
    const r: SubjectApi = j?.data;
    return { id: r.id, classId: r.classId, title: r.title, description: r.description ?? undefined, orderIndex: r.orderIndex, status: r.status as any };
  }

  async listLessons(subjectId: string): Promise<Lesson[]> {
    const res = await fetch(`${this.api}/v1/lessons?subject_id=${encodeURIComponent(subjectId)}`, { credentials: 'include' });
    const j = await res.json();
    const rows: LessonApi[] = j?.data ?? [];
    return rows.map(this.mapLesson);
  }

  async createLesson(input: { subjectId: string; title: string; type: 'text'|'audio'|'video'|'live'|'simulation'; content?: any; orderIndex?: number; isFree?: boolean }): Promise<Lesson> {
    const body = { subjectId: input.subjectId, title: input.title, type: input.type, content: input.content ?? null, orderIndex: input.orderIndex ?? 0, isFree: !!input.isFree };
    const res = await fetch(`${this.api}/v1/lessons`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    const j = await res.json();
    const r: LessonApi = j?.data;
    return this.mapLesson(r);
  }

  // --- Gradebook ---
  async getClassGradebook(classId: string): Promise<{ tests: Array<{ id: string; title: string; max: number }>; students: JSONRecord[] }> {
    const res = await fetch(`${this.api}/v1/classes/${encodeURIComponent(classId)}/gradebook`, { credentials: 'include' });
    const j = await res.json();
    return j?.data ?? { tests: [], students: [] };
  }

  async downloadClassGradebookCSV(classId: string): Promise<Blob> {
    const res = await fetch(`${this.api}/v1/classes/${encodeURIComponent(classId)}/gradebook.csv`, { credentials: 'include' });
    const blob = await res.blob();
    return blob;
  }

  // --- Certificates ---
  async listCertificateTemplates(params: { scope?: 'platform'|'school'; schoolId?: string } = {}): Promise<any[]> {
    const q: string[] = [];
    if (params.scope) q.push(`scope=${encodeURIComponent(params.scope)}`);
    if (params.schoolId) q.push(`school_id=${encodeURIComponent(params.schoolId)}`);
    const res = await fetch(`${this.api}/v1/certificates/templates${q.length?'?'+q.join('&'):''}`, { credentials: 'include' });
    const j = await res.json();
    return j?.data ?? [];
  }

  async createCertificateTemplate(input: { scope: 'platform'|'school'; schoolId?: string; name: string; body?: any; style?: any }): Promise<any> {
    const res = await fetch(`${this.api}/v1/certificates/templates`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) });
    const j = await res.json();
    return j?.data;
  }

  async issueCertificate(input: { templateId: string; recipientUserId: string; classId?: string; data?: any }): Promise<{ id: string; code: string }> {
    const body = { templateId: input.templateId, recipientUserId: input.recipientUserId, classId: input.classId ?? '', data: input.data ?? {} } as any;
    const res = await fetch(`${this.api}/v1/certificates/issue`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    const j = await res.json();
    return j?.data;
  }

  async verifyCertificate(code: string): Promise<any> {
    const res = await fetch(`${this.api}/v1/certificates/verify?code=${encodeURIComponent(code)}`, { credentials: 'include' });
    const j = await res.json();
    return j?.data;
  }

  // --- Certificate Rules ---
  async listCertificateRules(params: { schoolId?: string; classId?: string }): Promise<any[]> {
    const q: string[] = [];
    if (params.schoolId) q.push(`school_id=${encodeURIComponent(params.schoolId)}`);
    if (params.classId) q.push(`class_id=${encodeURIComponent(params.classId)}`);
    const res = await fetch(`${this.api}/v1/certificates/rules${q.length?'?'+q.join('&'):''}`, { credentials: 'include' });
    const j = await res.json();
    return j?.data ?? [];
  }

  async createCertificateRule(input: { scope: 'class'|'school'; schoolId?: string; classId?: string; templateId: string; enabled?: boolean; conditions?: any }): Promise<any> {
    const res = await fetch(`${this.api}/v1/certificates/rules`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ...input }) });
    const j = await res.json();
    return j?.data;
  }

  async updateCertificateRule(id: string, input: { enabled?: boolean; conditions?: any }): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/certificates/rules/${encodeURIComponent(id)}`, { method: 'PATCH', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) });
    return !!(await res.json())?.success;
  }

  async listTests(filter: { subjectId?: string; lessonId?: string }): Promise<TestItem[]> {
    const url = new URL(`${this.api}/v1/tests`);
    if (filter.subjectId) url.searchParams.set('subject_id', filter.subjectId);
    if (filter.lessonId) url.searchParams.set('lesson_id', filter.lessonId);
    const res = await fetch(url.toString(), { credentials: 'include' });
    const j = await res.json();
    const rows: TestApi[] = j?.data ?? [];
    return rows.map(this.mapTest);
  }

  async createTest(input: { title: string; subjectId?: string; lessonId?: string; description?: string; visibility?: 'public'|'private'; schoolId?: string | null }): Promise<TestItem> {
    const body = { title: input.title, subjectId: input.subjectId ?? null, lessonId: input.lessonId ?? null, description: input.description ?? null, visibility: input.visibility ?? 'private', schoolId: input.schoolId ?? null };
    const res = await fetch(`${this.api}/v1/tests`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    const j = await res.json();
    const r: TestApi = j?.data;
    return this.mapTest(r);
  }

  private mapClassToCourse = (r: ClassItemApi): Course => ({
    id: r.id,
    name: r.title,
    title: r.title,
    description: r.description ?? '',
    isPaid: !!r.isPaid,
    priceCents: r.priceCents ?? 0,
    visibility: (r.visibility as any) ?? 'public',
    tutorUserId: r.tutorUserId?.trim(),
    studentCount: r.studentCount,
    subjectCount: r.subjectCount ?? undefined,
    lessonCount: r.lessonCount ?? undefined,
    testCount: r.testCount ?? undefined,
    schoolId: r.schoolId ?? null,
    isEnrolled: !!r.isEnrolled,
    status: (r.status as any) ?? 'active',
  });

  private mapLesson = (r: LessonApi): Lesson => {
    const estimatedMinutes = this.estimateDuration(r.type, r.content);
    return {
      id: r.id,
      title: r.title,
      description: undefined,
      subjectId: r.subjectId,
      type: r.type as any,
      content: r.content,
      orderIndex: r.orderIndex,
      isFree: !!r.isFree,
      estimatedMinutes,
      status: (r.status as any) ?? 'active',
    };
  };

  private mapTest = (r: TestApi): TestItem => ({
    id: r.id,
    schoolId: r.schoolId ?? undefined,
    subjectId: r.subjectId ?? undefined,
    lessonId: r.lessonId ?? undefined,
    title: r.title,
    description: r.description ?? undefined,
    visibility: (r.visibility as any) ?? 'private',
    createdByUserId: r.createdByUserId,
    createdAt: r.createdAt,
    status: (r.status as any) ?? 'active',
  });

  async getTestById(id: string): Promise<{ test: TestItem; questions: TestQuestion[]; classId?: string; canEdit?: boolean } | null> {
    const res = await fetch(`${this.api}/v1/tests/${id}`, { credentials: 'include' });
    if (!res.ok) return null;
    const j = await res.json();
    const d: any = j?.data;
    const test = this.mapTest(d.test);
    if (d.classId) (test as any).classId = d.classId;
    const questions: TestQuestion[] = (d.questions ?? []).map((q: any) => ({
      id: q.id,
      testId: q.testId,
      qtype: q.qtype,
      prompt: q.prompt,
      options: q.options,
      answer: q.answer,
      points: q.points,
      orderIndex: q.orderIndex,
    }));
    return { test, questions, classId: d.classId, canEdit: d.canEdit };
  }

  private estimateDuration(type: string, content: any): number | undefined {
    try {
      if (type === 'text') {
        const text = typeof content?.markdown === 'string' ? content.markdown : (typeof content?.html === 'string' ? content.html.replace(/<[^>]+>/g, ' ') : '');
        const words = (text || '').trim().split(/\s+/).filter(Boolean).length;
        const minutes = Math.max(1, Math.round(words / 200));
        return minutes;
      }
      if (type === 'audio' || type === 'video') {
        const sec = Number(content?.durationSec ?? 0);
        if (!isNaN(sec) && sec > 0) return Math.max(1, Math.round(sec / 60));
      }
      if (type === 'live') {
        const start = content?.startsAt ? Date.parse(content.startsAt) : 0;
        const end = content?.endsAt ? Date.parse(content.endsAt) : 0;
        if (end > start && start > 0) return Math.max(1, Math.round((end - start) / 60000));
      }
    } catch {}
    return undefined;
  }

  async search(type: 'user'|'school'|'class'|'subject'|'lesson'|'test', q: string, opts?: { scope?: 'related'|'global' }): Promise<any[]> {
    const url = new URL(`${this.api}/v1/search`);
    url.searchParams.set('type', type);
    url.searchParams.set('q', q.trim());
    if (opts?.scope) url.searchParams.set('scope', opts.scope);
    const res = await fetch(url.toString(), { credentials: 'include' });
    const j = await res.json();
    return j?.data ?? [];
  }

  async searchTutors(q: string): Promise<Array<{ userId: string; bio?: string }>> {
    // Fallback: list approved tutors and filter client-side by userId (server has no q for tutors list)
    const url = new URL(`${this.api}/v1/tutors`);
    url.searchParams.set('status', 'approved');
    const res = await fetch(url.toString(), { credentials: 'include' });
    const j = await res.json();
    const list: Array<{ user_id: string; bio?: string }> = j?.data ?? [];
    const ql = q.trim().toLowerCase();
    if (!ql) return list.map(t => ({ userId: t.user_id, bio: t.bio }));
    return list.filter(t => t.user_id?.toLowerCase().includes(ql)).map(t => ({ userId: t.user_id, bio: t.bio }));
  }

  // Attempts API
  async startTestAttempt(testId: string): Promise<any> {
    const res = await fetch(`${this.api}/v1/tests/${testId}/attempts/start`, { method: 'POST', credentials: 'include' });
    return (await res.json())?.data;
  }
  async saveTestAttempt(testId: string, responses: Record<string, any>): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/tests/${testId}/attempts/save`, { method: 'PATCH', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ responses }) });
    return !!(await res.json())?.success;
  }
  async submitTestAttempt(testId: string, responses: Record<string, any>): Promise<{ earned: number; max: number; score: number|null } | null> {
    const res = await fetch(`${this.api}/v1/tests/${testId}/attempts/submit`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ responses }) });
    if (!res.ok) return null;
    const j = await res.json();
    const d = j?.data || {};
    return { earned: d.earned ?? 0, max: d.max ?? 0, score: d.score ?? null };
  }

  async listAdminSchools(): Promise<SchoolSummary[]> {
    const res = await fetch(`${this.api}/v1/schools/mine?role=admin`, { credentials: 'include' });
    const j = await res.json();
    const rows: any[] = j?.data ?? [];
    return rows.map(r => ({
      id: r.id,
      name: r.name,
      description: r.description ?? null,
      isVerified: !!r.isVerified,
    }));
  }

  async addSchoolMember(schoolId: string, userId: string, role: 'admin'|'tutor'): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/schools/${schoolId}/members`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ userId, role }),
    });
    return !!(await res.json())?.success;
  }

  async getSchoolMembers(schoolId: string): Promise<Array<{ userId: string; role: string; status: string }>> {
    const res = await fetch(`${this.api}/v1/schools/${schoolId}/members`, { credentials: 'include' });
    const j = await res.json();
    return (j?.data ?? []) as any[];
  }

  async getSchoolInvites(schoolId: string): Promise<SchoolInvite[]> {
    const res = await fetch(`${this.api}/v1/schools/${schoolId}/invites`, { credentials: 'include' });
    const j = await res.json();
    const rows: any[] = j?.data ?? [];
    return rows.map(r => ({
      id: r.id,
      email: r.email ?? undefined,
      existingUserId: r.existingUserId ?? undefined,
      role: r.role,
      status: r.status,
      invitedByUserId: r.invitedByUserId,
      message: r.message ?? undefined,
      createdAt: r.createdAt,
      respondedAt: r.respondedAt ?? undefined,
    }));
  }

  async createSchoolInvite(schoolId: string, payload: { email?: string; existingUserId?: string; role: 'admin'|'tutor'; message?: string }): Promise<SchoolInvite | null> {
    const res = await fetch(`${this.api}/v1/schools/${schoolId}/invites`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    if (!res.ok) return null;
    const j = await res.json();
    const d = j?.data;
    if (!d) return null;
    return {
      id: d.id,
      email: d.email ?? undefined,
      existingUserId: d.existingUserId ?? undefined,
      role: d.role,
      status: d.status,
      invitedByUserId: d.invitedByUserId ?? undefined,
      message: d.message ?? undefined,
      createdAt: d.createdAt,
      token: d.token ?? undefined,
    };
  }

  async getSchoolOverview(schoolId: string): Promise<SchoolOverview | null> {
    const res = await fetch(`${this.api}/v1/schools/${schoolId}/overview`, { credentials: 'include' });
    if (!res.ok) return null;
    const j = await res.json();
    const d = j?.data;
    if (!d?.school) return null;
    const mapUser = (raw: any): UserRef => {
      const id = raw?.userId ?? raw?.id ?? '';
      const fallback = id || 'unknown';
      const name = raw?.displayName ?? raw?.name ?? (id || 'Unknown');
      return { id: fallback, name, avatar: raw?.avatar };
    };
    const classes = (d.classes ?? []).map((cls: any) => ({
      id: cls.id,
      title: cls.title,
      description: cls.description ?? null,
      tutor: mapUser(cls.tutor || {}),
      visibility: cls.visibility,
      isPaid: !!cls.isPaid,
      priceCents: cls.priceCents ?? 0,
      studentCount: cls.studentCount ?? (cls.students?.length ?? 0),
      students: (cls.students ?? []).map((st: any) => mapUser({ ...st, userId: st.userId ?? st.id })),
      subjects: (cls.subjects ?? []).map((sub: any) => ({
        id: sub.id,
        title: sub.title,
        description: sub.description ?? null,
        orderIndex: sub.orderIndex ?? 0,
        lessons: (sub.lessons ?? []).map((lesson: any) => ({
          id: lesson.id,
          title: lesson.title,
          type: lesson.type,
          content: lesson.content,
          orderIndex: lesson.orderIndex ?? 0,
          isFree: !!lesson.isFree,
          tests: (lesson.tests ?? []).map((t: any) => ({
            id: t.id,
            title: t.title,
            description: t.description ?? null,
            visibility: t.visibility,
            createdAt: t.createdAt,
          })),
        })),
        tests: (sub.tests ?? []).map((t: any) => ({
          id: t.id,
          title: t.title,
          description: t.description ?? null,
          visibility: t.visibility,
          createdAt: t.createdAt,
        })),
      })),
    }));
    return {
      school: {
        id: d.school.id,
        ownerUserId: d.school.ownerUserId,
        name: d.school.name,
        description: d.school.description ?? null,
        isVerified: !!d.school.isVerified,
        createdAt: d.school.createdAt,
        updatedAt: d.school.updatedAt,
      },
      classes,
    };
  }

  async searchUsersGlobal(q: string): Promise<UserRef[]> {
    const data = await this.search('user', q, { scope: 'global' });
    return (data ?? []).map((u: any) => ({
      id: u.userId,
      name: u.displayName || u.userId,
    }));
  }

  async getCurrentUserId(force = false): Promise<string | null> {
    if (!force && this.currentUserIdCache !== undefined) return this.currentUserIdCache;
    try {
      const result = await verifySession({ attemptRefresh: true });
      const data = result?.data;
      let uid: string | null = null;
      if (data?.uid != null) uid = String(data.uid);
      if (data?.userId) uid = data.userId;
      if (uid && uid.trim().length === 0) uid = null;
      this.currentUserIdCache = uid;
      return uid;
    } catch {
      this.currentUserIdCache = null;
      return null;
    }
  }

  async getTutorStatus(): Promise<string | null> {
    try {
      const res = await fetch(`${this.api}/v1/tutors/me`, { credentials: 'include' });
      if (!res.ok) return null;
      const j = await res.json();
      const status = j?.data?.status;
      if (typeof status === 'string' && status.trim().length > 0) {
        return status;
      }
      return null;
    } catch {
      return null;
    }
  }

  async isTutor(): Promise<boolean> {
    const status = (await this.getTutorStatus())?.toLowerCase() ?? '';
    if (!status) return false;
    return ['approved', 'active', 'onboarded', 'verified'].includes(status);
  }

  async updateCourse(courseId: string, payload: { title?: string; description?: string; visibility?: 'public'|'private'|'school'; isPaid?: boolean; priceCents?: number; status?: 'active'|'archived'|'deleted' }): Promise<boolean> {
    const body: any = {};
    if (payload.title !== undefined) body.title = payload.title;
    if (payload.description !== undefined) body.description = payload.description;
    if (payload.visibility !== undefined) body.visibility = payload.visibility;
    if (payload.isPaid !== undefined) body.isPaid = payload.isPaid;
    if (payload.priceCents !== undefined) body.priceCents = payload.priceCents;
    if (payload.status !== undefined) body.status = payload.status;
    const res = await fetch(`${this.api}/v1/classes/${courseId}`, {
      method: 'PATCH',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    });
    return res.ok;
  }

  async updateSubjectStatus(subjectId: string, status: 'active'|'archived'|'deleted'): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/subjects/${subjectId}`, {
      method: 'PATCH',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ status })
    });
    return res.ok;
  }

  async updateLessonStatus(lessonId: string, status: 'active'|'archived'|'deleted'): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/lessons/${lessonId}`, {
      method: 'PATCH',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ status })
    });
    return res.ok;
  }

  async updateTestStatus(testId: string, status: 'active'|'archived'|'deleted'): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/tests/${testId}`, {
      method: 'PATCH',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ status })
    });
    return res.ok;
  }
  // --- Discussions & Announcements ---
  async listDiscussions(classId: string): Promise<any[]> {
    const res = await fetch(`${this.api}/v1/classes/${encodeURIComponent(classId)}/discussions`, { credentials: 'include' });
    const j = await res.json();
    return j?.data ?? [];
  }
  async createDiscussion(classId: string, title: string): Promise<string | null> {
    const res = await fetch(`${this.api}/v1/classes/${encodeURIComponent(classId)}/discussions`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ title }) });
    const j = await res.json();
    return j?.data?.id ?? null;
  }
  async getDiscussion(id: string): Promise<{ discussion: any; posts: any[] }> {
    const res = await fetch(`${this.api}/v1/discussions/${encodeURIComponent(id)}`, { credentials: 'include' });
    const j = await res.json();
    return j?.data ?? { discussion: null, posts: [] };
  }
  async postToDiscussion(id: string, body: string): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/discussions/${encodeURIComponent(id)}/posts`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ body }) });
    return !!(await res.json())?.success;
  }
  async listAnnouncements(classId: string): Promise<any[]> {
    const res = await fetch(`${this.api}/v1/classes/${encodeURIComponent(classId)}/announcements`, { credentials: 'include' });
    const j = await res.json();
    return j?.data ?? [];
  }
  async createAnnouncement(classId: string, input: { title: string; body?: string; pinned?: boolean }): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/classes/${encodeURIComponent(classId)}/announcements`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) });
    return !!(await res.json())?.success;
  }

  // --- Direct Messages ---
  async listMessageThreads(): Promise<any[]> {
    const res = await fetch(`${this.api}/v1/messages/threads`, { credentials: 'include' });
    const j = await res.json();
    return j?.data ?? [];
  }
  async getThreadMessages(id: string): Promise<any[]> {
    const res = await fetch(`${this.api}/v1/messages/threads/${encodeURIComponent(id)}`, { credentials: 'include' });
    const j = await res.json();
    return j?.data ?? [];
  }
  async sendThreadMessage(id: string, body: string, attachments?: any): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/messages/threads/${encodeURIComponent(id)}`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ body, attachments }) });
    return !!(await res.json())?.success;
  }
  async startMessageThread(userId: string, classId: string): Promise<string | null> {
    const res = await fetch(`${this.api}/v1/messages/start`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ userId, classId }) });
    const j = await res.json();
    return j?.data?.threadId ?? null;
  }

  // --- Calls ---
  async startThreadCall(threadId: string, media: 'video'|'audio' = 'video'): Promise<{ url: string; roomCode: string } | null> {
    const res = await fetch(`${this.api}/v1/messages/threads/${encodeURIComponent(threadId)}/call/start`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ media }) });
    const j = await res.json();
    return j?.data ?? null;
  }
  async getThreadCall(threadId: string): Promise<{ url: string } | null> {
    const res = await fetch(`${this.api}/v1/messages/threads/${encodeURIComponent(threadId)}/call`, { credentials: 'include' });
    const j = await res.json();
    return j?.data ?? null;
  }

  // --- Study Groups ---
  async listStudyGroups(classId: string): Promise<any[]> {
    const res = await fetch(`${this.api}/v1/classes/${encodeURIComponent(classId)}/groups`, { credentials: 'include' });
    const j = await res.json();
    return j?.data ?? [];
  }
  async createStudyGroup(classId: string, title: string): Promise<string | null> {
    const res = await fetch(`${this.api}/v1/classes/${encodeURIComponent(classId)}/groups`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ title }) });
    const j = await res.json();
    return j?.data?.id ?? null;
  }
  async joinStudyGroup(groupId: string): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/groups/${encodeURIComponent(groupId)}/join`, { method: 'POST', credentials: 'include' });
    return !!(await res.json())?.success;
  }
  async leaveStudyGroup(groupId: string): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/groups/${encodeURIComponent(groupId)}/leave`, { method: 'POST', credentials: 'include' });
    return !!(await res.json())?.success;
  }
  async listGroupMessages(groupId: string): Promise<any[]> {
    const res = await fetch(`${this.api}/v1/groups/${encodeURIComponent(groupId)}/messages`, { credentials: 'include' });
    const j = await res.json();
    return j?.data ?? [];
  }
  async sendGroupMessage(groupId: string, body: string, attachments?: any): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/groups/${encodeURIComponent(groupId)}/messages`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ body, attachments }) });
    return !!(await res.json())?.success;
  }
  async startGroupCall(groupId: string): Promise<{ url: string; roomCode: string } | null> {
    const res = await fetch(`${this.api}/v1/groups/${encodeURIComponent(groupId)}/call/start`, { method: 'POST', credentials: 'include' });
    const j = await res.json();
    return j?.data ?? null;
  }
  // Moderation
  async listGroupMembers(groupId: string): Promise<any[]> {
    const res = await fetch(`${this.api}/v1/groups/${encodeURIComponent(groupId)}/members`, { credentials: 'include' });
    const j = await res.json();
    return j?.data ?? [];
  }
  async setGroupMemberRole(groupId: string, userId: string, role: 'member'|'moderator'|'owner'): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/groups/${encodeURIComponent(groupId)}/members/${encodeURIComponent(userId)}/role`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ role }) });
    return !!(await res.json())?.success;
  }
  async banGroupMember(groupId: string, userId: string): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/groups/${encodeURIComponent(groupId)}/members/${encodeURIComponent(userId)}/ban`, { method: 'POST', credentials: 'include' });
    return !!(await res.json())?.success;
  }
  async unbanGroupMember(groupId: string, userId: string): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/groups/${encodeURIComponent(groupId)}/members/${encodeURIComponent(userId)}/unban`, { method: 'POST', credentials: 'include' });
    return !!(await res.json())?.success;
  }
  async deleteGroupMessage(messageId: string): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/groups/messages/${encodeURIComponent(messageId)}`, { method: 'DELETE', credentials: 'include' });
    return !!(await res.json())?.success;
  }
  async deleteDiscussionPost(postId: string): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/discussions/posts/${encodeURIComponent(postId)}`, { method: 'DELETE', credentials: 'include' });
    return !!(await res.json())?.success;
  }

  // --- Appointments ---
  async listAppointmentSlots(classId: string): Promise<any[]> {
    const res = await fetch(`${this.api}/v1/classes/${encodeURIComponent(classId)}/appointments/slots`, { credentials: 'include' });
    const j = await res.json();
    return j?.data ?? [];
  }
  async createAppointmentSlot(classId: string, input: { startAt: string; endAt: string; capacity?: number; locationUrl?: string; notes?: string }): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/classes/${encodeURIComponent(classId)}/appointments/slots`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) });
    return !!(await res.json())?.success;
  }
  async bookAppointmentSlot(slotId: string, forUserId?: string): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/appointments/slots/${encodeURIComponent(slotId)}/book`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(forUserId?{ forUserId }:{}) });
    return !!(await res.json())?.success;
  }
  async listMyAppointments(): Promise<any[]> {
    const res = await fetch(`${this.api}/v1/appointments/my`, { credentials: 'include' });
    const j = await res.json();
    return j?.data ?? [];
  }

  // --- Resources (Docs/Sheets/Notes/PDF/Slides) ---
  async listResources(scope: 'lesson'|'group'|'class'|'school'|'user', scopeId: string): Promise<Array<{ id: string; type: 'docs'|'sheets'|'notes'|'pdf'|'slides'; url: string; title?: string }>> {
    const res = await fetch(`${this.api}/v1/resources?scope=${encodeURIComponent(scope)}&scope_id=${encodeURIComponent(scopeId)}`, { credentials: 'include' });
    const j = await res.json();
    const rows = (j?.data ?? []) as any[];
    return rows.map(r => ({ id: r.id, type: r.type, url: r.url, title: r.title ?? undefined }));
  }

  async attachResource(input: {
    mode: 'create'|'link';
    type: 'docs'|'sheets'|'notes'|'pdf'|'slides';
    title?: string;
    url?: string;
    externalId?: string;
    scope: 'lesson'|'group'|'class'|'school'|'user';
    scopeId: string;
  }): Promise<{ id: string; type: string; url: string; title?: string } | null> {
    const res = await fetch(`${this.api}/v1/resources`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input) });
    const j = await res.json();
    return j?.data ?? null;
  }

  async openResource(resourceId: string): Promise<void> {
    // Let server issue SSO-verified redirect
    const openUrl = `${this.api}/v1/resources/${encodeURIComponent(resourceId)}/open`;
    window.open(openUrl, '_blank');
  }

  async grantResourceRole(resourceId: string, principalType: 'user'|'class'|'group'|'school', principalId: string, role: 'owner'|'editor'|'commenter'|'viewer'): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/resources/${encodeURIComponent(resourceId)}/acl`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ principalType, principalId, role }) });
    return !!(await res.json())?.success;
  }

  async revokeResourceRole(resourceId: string, principalType: 'user'|'class'|'group'|'school', principalId: string): Promise<boolean> {
    const res = await fetch(`${this.api}/v1/resources/${encodeURIComponent(resourceId)}/acl?principal_type=${encodeURIComponent(principalType)}&principal_id=${encodeURIComponent(principalId)}`, { method: 'DELETE', credentials: 'include' });
    return !!(await res.json())?.success;
  }

  async listResourceAcl(resourceId: string): Promise<Array<{ id: string; principalType: 'user'|'class'|'group'|'school'; principalId: string; role: 'owner'|'editor'|'commenter'|'viewer'; createdBy?: string; createdAt?: string }>> {
    const res = await fetch(`${this.api}/v1/resources/${encodeURIComponent(resourceId)}/acl`, { credentials: 'include' });
    const j = await res.json();
    return (j?.data ?? []) as any[];
  }

  // --- Search (users by name/email/uid) ---
  async searchUsers(q: string, opts: { scope?: 'related'|'global' } = {}): Promise<Array<{ userId: string; displayName?: string }>> {
    const params = new URLSearchParams({ type: 'user', q: q || '' });
    if (opts.scope === 'global') params.set('scope', 'global');
    const res = await fetch(`${urlFor('schools-api')}/v1/search?${params.toString()}`, { credentials: 'include' });
    const j = await res.json();
    const rows = (j?.data ?? []) as any[];
    return rows.map(r => ({ userId: r.userId || r.user_id, displayName: r.displayName || r.display_name }));
  }
}
