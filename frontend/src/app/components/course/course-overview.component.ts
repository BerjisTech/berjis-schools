import { Component, OnInit, computed, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { FormsModule } from '@angular/forms';
import { SchoolsService } from '../../services/schools.service';
import { Course, Lesson, Subject, TestItem, TestPlacementStrategy } from '../../interfaces/course';

interface LessonFormState {
  subjectId: string;
  title: string;
  type: 'text' | 'audio' | 'video' | 'live' | 'simulation';
  isFree: boolean;
  textHtml: string;
  audioUrl: string;
  audioDuration: string;
  videoUrl: string;
  videoDuration: string;
  liveMode: 'one_on_one' | 'group';
  liveStartsAt: string;
  liveEndsAt: string;
  simulationUrl: string;
  simulationInstructions: string;
}

interface TestFormState {
  subjectId: string;
  scope: 'subject' | 'lesson';
  lessonId: string | null;
  title: string;
  description: string;
  visibility: 'private' | 'public';
}

const createLessonFormDefaults = (): LessonFormState => ({
  subjectId: '',
  title: '',
  type: 'text',
  isFree: false,
  textHtml: '',
  audioUrl: '',
  audioDuration: '',
  videoUrl: '',
  videoDuration: '',
  liveMode: 'one_on_one',
  liveStartsAt: '',
  liveEndsAt: '',
  simulationUrl: '',
  simulationInstructions: '',
});

const createTestFormDefaults = (): TestFormState => ({
  subjectId: '',
  scope: 'subject',
  lessonId: null,
  title: '',
  description: '',
  visibility: 'private',
});

@Component({
  selector: 'app-course-overview',
  standalone: true,
  imports: [CommonModule, RouterLink, FormsModule],
  templateUrl: './course-overview.component.html'
})
export class CourseOverviewComponent implements OnInit {
  private courseId = '';

  course = signal<Course | null>(null);
  subjects = signal<Subject[]>([]);
  lessonsBySubject = signal<Record<string, Lesson[]>>({});
  testsByLesson = signal<Record<string, TestItem[]>>({});
  testsBySubject = signal<Record<string, TestItem[]>>({});
  loading = signal(true);
  enrolling = signal(false);
  placement = signal<TestPlacementStrategy>({ kind: 'manual' });
  currentUserId = signal<string | null>(null);

  subjectTitle = signal('');
  creatingSubject = signal(false);

  activeLessonSubjectId = signal<string | null>(null);
  lessonFormState: LessonFormState = createLessonFormDefaults();
  creatingLesson = signal(false);

  activeTestSubjectId = signal<string | null>(null);
  testFormState: TestFormState = createTestFormDefaults();
  creatingTest = signal(false);

  toast = signal<{ kind: 'success' | 'error'; text: string } | null>(null);

  totalMinutes = computed(() => {
    const lbs = this.lessonsBySubject();
    let sum = 0;
    for (const sid of Object.keys(lbs)) {
      for (const l of lbs[sid]) sum += l.estimatedMinutes ?? 0;
    }
    return sum;
  });

  isOwner = computed(() => {
    const c = this.course();
    const uid = this.currentUserId();
    if (!c || !uid) return false;
    return (c.tutorUserId ?? '') === uid;
  });

  constructor(private route: ActivatedRoute, private svc: SchoolsService) {}

  async ngOnInit() {
    this.courseId = this.route.snapshot.paramMap.get('id') ?? '';
    if (this.courseId) {
      const stored = localStorage.getItem(`course:${this.courseId}:testPlacement`);
      if (stored) {
        try {
          this.placement.set(JSON.parse(stored));
        } catch {
          /* ignore bad placement cache */
        }
      }
      await this.loadData(this.courseId);
    }
  }

  private async loadData(id: string) {
    this.loading.set(true);
    try {
      const [course, uid] = await Promise.all([
        this.svc.getCourse(id),
        this.svc.getCurrentUserId(),
      ]);
      this.currentUserId.set(uid);
      if (!course) {
        this.course.set(null);
        this.subjects.set([]);
        this.lessonsBySubject.set({});
        this.testsByLesson.set({});
        this.testsBySubject.set({});
        return;
      }
      this.course.set(course);
      const subs = await this.svc.listSubjects(`${course.id}`);
      this.subjects.set(subs);
      const lessonsBySubject: Record<string, Lesson[]> = {};
      const testsByLesson: Record<string, TestItem[]> = {};
      const testsBySubject: Record<string, TestItem[]> = {};
      for (const subject of subs) {
        const [lessons, subjectTests] = await Promise.all([
          this.svc.listLessons(subject.id),
          this.svc.listTests({ subjectId: subject.id })
        ]);
        lessonsBySubject[subject.id] = lessons;
        testsBySubject[subject.id] = subjectTests.filter(t => !t.lessonId);
        await Promise.all(lessons.map(async lesson => {
          const lt = await this.svc.listTests({ lessonId: `${lesson.id}` });
          testsByLesson[`${lesson.id}`] = lt;
        }));
      }
      this.lessonsBySubject.set(lessonsBySubject);
      this.testsByLesson.set(testsByLesson);
      this.testsBySubject.set(testsBySubject);
    } finally {
      this.loading.set(false);
    }
  }

  displayedItemsForSubject(subjectId: string): { kind: 'lesson' | 'test'; lesson?: Lesson; test?: TestItem }[] {
    const placement = this.placement();
    const lessons = this.lessonsBySubject()[subjectId] ?? [];
    const subjectTests = this.testsBySubject()[subjectId] ?? [];
    const items: { kind: 'lesson' | 'test'; lesson?: Lesson; test?: TestItem }[] = [];
    const lessonTests = (lid: string) => this.testsByLesson()[lid] ?? [];

    if (placement.kind === 'after_each_lesson') {
      for (const l of lessons) {
        items.push({ kind: 'lesson', lesson: l });
        for (const t of lessonTests(`${l.id}`)) items.push({ kind: 'test', test: t });
        for (const t of subjectTests) items.push({ kind: 'test', test: t });
      }
      return items;
    }
    if (placement.kind === 'after_every_n_lessons') {
      let i = 0;
      for (const l of lessons) {
        items.push({ kind: 'lesson', lesson: l });
        i++;
        if (i % Math.max(1, placement.n) === 0) {
          for (const t of subjectTests) items.push({ kind: 'test', test: t });
        }
        for (const t of lessonTests(`${l.id}`)) items.push({ kind: 'test', test: t });
      }
      return items;
    }
    if (placement.kind === 'random') {
      const shuffled = [...subjectTests];
      shuffled.sort(() => Math.random() - 0.5);
      const count = Math.min(placement.count ?? subjectTests.length, subjectTests.length);
      const indexes = new Set<number>();
      while (indexes.size < Math.min(count, lessons.length)) indexes.add(Math.floor(Math.random() * lessons.length));
      lessons.forEach((l, idx) => {
        items.push({ kind: 'lesson', lesson: l });
        if (indexes.has(idx)) {
          const t = shuffled.pop();
          if (t) items.push({ kind: 'test', test: t });
        }
        for (const t of lessonTests(`${l.id}`)) items.push({ kind: 'test', test: t });
      });
      return items;
    }
    for (const l of lessons) {
      items.push({ kind: 'lesson', lesson: l });
      for (const t of lessonTests(`${l.id}`)) items.push({ kind: 'test', test: t });
    }
    for (const t of subjectTests) items.push({ kind: 'test', test: t });
    return items;
  }

  async enroll() {
    const c = this.course();
    if (!c) return;
    this.enrolling.set(true);
    try {
      await this.svc.enrollInCourse(`${c.id}`);
      this.toast.set({ kind: 'success', text: 'You are enrolled in this course.' });
    } catch (e: any) {
      this.toast.set({ kind: 'error', text: e?.message || 'Unable to enroll right now.' });
    } finally {
      this.enrolling.set(false);
    }
  }

  updatePlacement(p: TestPlacementStrategy) {
    this.placement.set(p);
    if (this.courseId) localStorage.setItem(`course:${this.courseId}:testPlacement`, JSON.stringify(p));
  }

  async createSubject() {
    const c = this.course();
    const title = this.subjectTitle().trim();
    if (!c || !title) return;
    this.creatingSubject.set(true);
    try {
      await this.svc.createSubject({ classId: `${c.id}`, title });
      this.subjectTitle.set('');
      this.toast.set({ kind: 'success', text: 'Subject added.' });
      await this.loadData(this.courseId);
    } catch (e: any) {
      this.toast.set({ kind: 'error', text: e?.message || 'Could not add subject.' });
    } finally {
      this.creatingSubject.set(false);
    }
  }

  openLessonForm(subjectId: string) {
    this.activeLessonSubjectId.set(subjectId);
    this.lessonFormState = { ...createLessonFormDefaults(), subjectId };
  }

  cancelLessonForm() {
    this.activeLessonSubjectId.set(null);
    this.lessonFormState = createLessonFormDefaults();
  }

  async createLesson() {
    const form = this.lessonFormState;
    if (!form.subjectId || !form.title.trim()) return;
    this.creatingLesson.set(true);
    try {
      const content = this.buildLessonContent(form);
      const lessons = this.lessonsBySubject()[form.subjectId] ?? [];
      await this.svc.createLesson({
        subjectId: form.subjectId,
        title: form.title.trim(),
        type: form.type,
        content,
        orderIndex: lessons.length,
        isFree: form.isFree,
      });
      this.toast.set({ kind: 'success', text: 'Lesson created.' });
      this.cancelLessonForm();
      await this.loadData(this.courseId);
    } catch (e: any) {
      this.toast.set({ kind: 'error', text: e?.message || 'Could not create lesson.' });
    } finally {
      this.creatingLesson.set(false);
    }
  }

  openTestForm(subjectId: string) {
    this.activeTestSubjectId.set(subjectId);
    this.testFormState = { ...createTestFormDefaults(), subjectId };
  }

  cancelTestForm() {
    this.activeTestSubjectId.set(null);
    this.testFormState = createTestFormDefaults();
  }

  async createTest() {
    const form = this.testFormState;
    if (!form.subjectId || !form.title.trim()) return;
    if (form.scope === 'lesson' && !form.lessonId) return;
    this.creatingTest.set(true);
    try {
      await this.svc.createTest({
        title: form.title.trim(),
        description: form.description.trim() || undefined,
        visibility: form.visibility,
        subjectId: form.scope === 'subject' ? form.subjectId : undefined,
        lessonId: form.scope === 'lesson' ? form.lessonId ?? undefined : undefined,
      });
      this.toast.set({ kind: 'success', text: 'Assessment created.' });
      this.cancelTestForm();
      await this.loadData(this.courseId);
    } catch (e: any) {
      this.toast.set({ kind: 'error', text: e?.message || 'Could not create assessment.' });
    } finally {
      this.creatingTest.set(false);
    }
  }

  private buildLessonContent(form: LessonFormState): any {
    switch (form.type) {
      case 'text':
        return form.textHtml.trim() ? { html: form.textHtml.trim() } : null;
      case 'audio':
        return {
          url: form.audioUrl.trim() || undefined,
          durationSec: this.parseDuration(form.audioDuration),
        };
      case 'video':
        return {
          url: form.videoUrl.trim() || undefined,
          durationSec: this.parseDuration(form.videoDuration),
        };
      case 'live':
        return {
          mode: form.liveMode,
          startsAt: form.liveStartsAt || undefined,
          endsAt: form.liveEndsAt || undefined,
        };
      case 'simulation':
        return {
          sandboxUrl: form.simulationUrl.trim() || undefined,
          instructions: form.simulationInstructions.trim() || undefined,
        };
      default:
        return null;
    }
  }

  setAssessmentScope(scope: 'subject' | 'lesson') {
    const current = this.testFormState;
    this.testFormState = {
      ...current,
      scope,
      lessonId: scope === 'lesson' ? current.lessonId : null,
    };
  }

  private parseDuration(raw: string): number | undefined {
    if (!raw || !raw.trim()) return undefined;
    const num = Number(raw);
    return Number.isFinite(num) && num > 0 ? num : undefined;
  }

  dismissToast() {
    this.toast.set(null);
  }
}
