import { Component, OnInit, computed, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { SchoolsService } from '../../services/schools.service';
import { Course, Lesson, Subject, TestItem, TestPlacementStrategy } from '../../interfaces/course';

@Component({
  selector: 'app-course-overview',
  standalone: true,
  imports: [CommonModule, RouterLink],
  templateUrl: './course-overview.component.html'
})
export class CourseOverviewComponent implements OnInit {
  course = signal<Course | null>(null);
  subjects = signal<Subject[]>([]);
  lessonsBySubject = signal<Record<string, Lesson[]>>({});
  testsByLesson = signal<Record<string, TestItem[]>>({});
  testsBySubject = signal<Record<string, TestItem[]>>({});
  loading = signal(true);
  enrolling = signal(false);
  placement = signal<TestPlacementStrategy>({ kind: 'manual' });

  totalMinutes = computed(() => {
    const lbs = this.lessonsBySubject();
    let sum = 0;
    for (const sid of Object.keys(lbs)) {
      for (const l of lbs[sid]) sum += l.estimatedMinutes ?? 0;
    }
    return sum;
  });

  constructor(private route: ActivatedRoute, private svc: SchoolsService) {}

  async ngOnInit() {
    const id = this.route.snapshot.paramMap.get('id')!;
    const stored = localStorage.getItem(`course:${id}:testPlacement`);
    if (stored) try { this.placement.set(JSON.parse(stored)); } catch {}

    this.loading.set(true);
    const course = await this.svc.getCourse(id);
    if (course) {
      this.course.set(course);
      const subs = await this.svc.listSubjects(`${course.id}`);
      this.subjects.set(subs);
      const lessonsBySubject: Record<string, Lesson[]> = {};
      const testsByLesson: Record<string, TestItem[]> = {};
      const testsBySubject: Record<string, TestItem[]> = {};
      for (const s of subs) {
        const lessons = await this.svc.listLessons(s.id);
        lessonsBySubject[s.id] = lessons;
        const subTests = await this.svc.listTests({ subjectId: s.id });
        testsBySubject[s.id] = subTests.filter(t => !t.lessonId);
        for (const l of lessons) {
          const lt = await this.svc.listTests({ lessonId: `${l.id}` });
          testsByLesson[`${l.id}`] = lt;
        }
      }
      this.lessonsBySubject.set(lessonsBySubject);
      this.testsByLesson.set(testsByLesson);
      this.testsBySubject.set(testsBySubject);
    }
    this.loading.set(false);
  }

  displayedItemsForSubject(subjectId: string): { kind: 'lesson'|'test'; lesson?: Lesson; test?: TestItem }[] {
    const placement = this.placement();
    const lessons = this.lessonsBySubject()[subjectId] ?? [];
    const subjectTests = this.testsBySubject()[subjectId] ?? [];
    const items: { kind: 'lesson'|'test'; lesson?: Lesson; test?: TestItem }[] = [];
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
      // naive random insertion: sprinkle subject tests randomly among lessons
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
    // manual (default): lesson items interleaved with their lesson-specific tests; subject-level tests at the end
    for (const l of lessons) {
      items.push({ kind: 'lesson', lesson: l });
      for (const t of lessonTests(`${l.id}`)) items.push({ kind: 'test', test: t });
    }
    for (const t of subjectTests) items.push({ kind: 'test', test: t });
    return items;
  }

  async enroll() {
    const c = this.course(); if (!c) return;
    this.enrolling.set(true);
    try { await this.svc.enrollInCourse(`${c.id}`); } finally { this.enrolling.set(false) }
  }

  updatePlacement(p: TestPlacementStrategy) {
    this.placement.set(p);
    const id = this.course()?.id; if (id) localStorage.setItem(`course:${id}:testPlacement`, JSON.stringify(p));
  }
}

