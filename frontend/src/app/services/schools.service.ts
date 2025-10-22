import { Injectable } from '@angular/core';
import { urlFor } from '../../app/util';
import { Course, Lesson, Subject, TestItem, TestQuestion } from '../interfaces/course';

interface ClassItemApi { id: string; title: string; description?: string | null; tutor_user_id?: string; visibility?: string; is_paid?: boolean; price_cents?: number }
interface SubjectApi { id: string; class_id: string; title: string; description?: string | null; order_index: number }
interface LessonApi { id: string; subject_id: string; title: string; type: string; content?: any; order_index: number; is_free: boolean }
interface TestApi { id: string; school_id?: string | null; subject_id?: string | null; lesson_id?: string | null; title: string; description?: string | null; visibility: string; created_by_user_id?: string; created_at?: string }
interface TestWithQuestionsApi { test: TestApi; questions: any[] }

@Injectable({ providedIn: 'root' })
export class SchoolsService {
  private api = urlFor('schools-api');

  async listCourses(): Promise<Course[]> {
    const res = await fetch(`${this.api}/v1/classes`, { credentials: 'include' });
    const j = await res.json();
    const rows: ClassItemApi[] = j?.data ?? [];
    return rows.map(this.mapClassToCourse);
  }

  async getCourse(id: string): Promise<Course | null> {
    // There is no dedicated "get class by id" endpoint; fetch list and filter for now.
    const list = await this.listCourses();
    return list.find(c => `${c.id}` === id) ?? null;
  }

  async createCourse(input: { title: string; description?: string; isPaid?: boolean; priceCents?: number; schoolId?: string | null }): Promise<Course> {
    const body = {
      title: input.title,
      description: input.description ?? null,
      isPaid: !!input.isPaid,
      priceCents: input.priceCents ?? 0,
      schoolId: input.schoolId ?? null,
      visibility: 'public',
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

  async listSubjects(classId: string): Promise<Subject[]> {
    const res = await fetch(`${this.api}/v1/subjects?class_id=${encodeURIComponent(classId)}`, { credentials: 'include' });
    const j = await res.json();
    const rows: SubjectApi[] = j?.data ?? [];
    return rows.map(r => ({ id: r.id, classId: r.class_id, title: r.title, description: r.description ?? undefined, orderIndex: r.order_index }));
  }

  async createSubject(input: { classId: string; title: string; description?: string; orderIndex?: number }): Promise<Subject> {
    const body = { classId: input.classId, title: input.title, description: input.description ?? null, orderIndex: input.orderIndex ?? 0 };
    const res = await fetch(`${this.api}/v1/subjects`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    const j = await res.json();
    const r: SubjectApi = j?.data;
    return { id: r.id, classId: r.class_id, title: r.title, description: r.description ?? undefined, orderIndex: r.order_index };
  }

  async listLessons(subjectId: string): Promise<Lesson[]> {
    const res = await fetch(`${this.api}/v1/lessons?subject_id=${encodeURIComponent(subjectId)}`, { credentials: 'include' });
    const j = await res.json();
    const rows: LessonApi[] = j?.data ?? [];
    return rows.map(this.mapLesson);
  }

  async createLesson(input: { subjectId: string; title: string; type: 'text'|'audio'|'video'|'live'; content?: any; orderIndex?: number; isFree?: boolean }): Promise<Lesson> {
    const body = { subjectId: input.subjectId, title: input.title, type: input.type, content: input.content ?? null, orderIndex: input.orderIndex ?? 0, isFree: !!input.isFree };
    const res = await fetch(`${this.api}/v1/lessons`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    const j = await res.json();
    const r: LessonApi = j?.data;
    return this.mapLesson(r);
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
    isPaid: !!r.is_paid,
    priceCents: r.price_cents ?? 0,
  });

  private mapLesson = (r: LessonApi): Lesson => {
    const estimatedMinutes = this.estimateDuration(r.type, r.content);
    return {
      id: r.id,
      title: r.title,
      description: undefined,
      subjectId: r.subject_id,
      type: r.type as any,
      content: r.content,
      orderIndex: r.order_index,
      isFree: !!r.is_free,
      estimatedMinutes,
    };
  };

  private mapTest = (r: TestApi): TestItem => ({
    id: r.id,
    schoolId: r.school_id ?? undefined,
    subjectId: r.subject_id ?? undefined,
    lessonId: r.lesson_id ?? undefined,
    title: r.title,
    description: r.description ?? undefined,
    visibility: (r.visibility as any) ?? 'private',
    createdByUserId: r.created_by_user_id,
    createdAt: r.created_at,
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

  async search(type: 'user'|'school'|'class'|'subject'|'lesson'|'test', q: string): Promise<any[]> {
    const url = new URL(`${this.api}/v1/search`);
    url.searchParams.set('type', type);
    url.searchParams.set('q', q.trim());
    const res = await fetch(url.toString(), { credentials: 'include' });
    const j = await res.json();
    return j?.data ?? [];
  }

  async searchTutors(q: string): Promise<{ userId: string; bio?: string }[]> {
    // Fallback: list approved tutors and filter client-side by userId (server has no q for tutors list)
    const url = new URL(`${this.api}/v1/tutors`);
    url.searchParams.set('status', 'approved');
    const res = await fetch(url.toString(), { credentials: 'include' });
    const j = await res.json();
    const list: { user_id: string; bio?: string }[] = j?.data ?? [];
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
}
