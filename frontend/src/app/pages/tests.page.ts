import { Component, OnInit, computed, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { RouterLink } from '@angular/router';
import { SchoolsService } from '../services/schools.service';
import { Course, Lesson, Subject, TestItem } from '../interfaces/course';

interface LessonAssessments {
  lesson: Lesson;
  tests: TestItem[];
}

interface SubjectAssessments {
  subject: Subject;
  subjectTests: TestItem[];
  lessons: LessonAssessments[];
  hasAssessments: boolean;
}

interface CourseAssessments {
  course: Course;
  subjects: SubjectAssessments[];
  isOwner: boolean;
}

@Component({
  selector: 'app-tests-page',
  standalone: true,
  imports: [CommonModule, RouterLink],
  templateUrl: './tests.page.html'
})
export class TestsPage implements OnInit {
  private readonly maxExploreCourses = 6;

  loading = signal(true);
  error = signal<string | null>(null);
  tutorEligible = signal(false);
  myCourses = signal<CourseAssessments[]>([]);
  exploreCourses = signal<Course[]>([]);

  totalAssessments = computed(() => {
    return this.myCourses().reduce((sum, entry) => {
      for (const subject of entry.subjects) {
        sum += subject.subjectTests.length;
        for (const lesson of subject.lessons) sum += lesson.tests.length;
      }
      return sum;
    }, 0);
  });
  hasAssessments = computed(() => this.totalAssessments() > 0);

  constructor(private svc: SchoolsService) {}

  async ngOnInit() {
    await this.load();
  }

  private async load() {
    this.loading.set(true);
    this.error.set(null);
    try {
      const [courses, uid, tutorFlag] = await Promise.all([
        this.svc.listCourses(),
        this.svc.getCurrentUserId(),
        this.svc.isTutor().catch(() => false),
      ]);

      const userId = (uid ?? '').trim().toLowerCase();
      this.exploreCourses.set(courses.slice(0, this.maxExploreCourses));

      const entries = await Promise.all(courses.map(async course => {
        const tutorId = (course.tutorUserId ?? '').trim().toLowerCase();
        const isOwner = !!userId && !!tutorId && tutorId === userId;
        const isParticipant = isOwner || !!course.isEnrolled;
        if (!isParticipant) return null;

        let subjects: Subject[] = [];
        try {
          subjects = await this.svc.listSubjects(`${course.id}`);
        } catch {
          subjects = [];
        }

        const subjectEntries = await Promise.all(subjects.map(async subject => {
          const [lessons, subjectTests] = await Promise.all([
            this.svc.listLessons(subject.id).catch(() => [] as Lesson[]),
            this.svc.listTests({ subjectId: subject.id }).catch(() => [] as TestItem[]),
          ]);

          const lessonEntries = await Promise.all(lessons.map(async lesson => {
            const tests = await this.svc.listTests({ lessonId: `${lesson.id}` }).catch(() => [] as TestItem[]);
            return { lesson, tests } as LessonAssessments;
          }));

          const hasAssessments = (subjectTests.length > 0) || lessonEntries.some(l => l.tests.length > 0);
          return { subject, subjectTests, lessons: lessonEntries, hasAssessments } as SubjectAssessments;
        }));

        return { course, subjects: subjectEntries, isOwner } as CourseAssessments;
      }));

      const filtered = entries.filter((entry): entry is CourseAssessments => !!entry);
      filtered.sort((a, b) => {
        const aTitle = (a.course.title || a.course.name || '').toLowerCase();
        const bTitle = (b.course.title || b.course.name || '').toLowerCase();
        return aTitle.localeCompare(bTitle);
      });

      this.myCourses.set(filtered);
      const ownsCourse = filtered.some(entry => entry.isOwner);
      const hasCreatedAssessments = filtered.some(entry => entry.subjects.some(s => s.hasAssessments));
      this.tutorEligible.set(tutorFlag || ownsCourse || hasCreatedAssessments);
    } catch (e: any) {
      this.error.set(e?.message || 'Unable to load assessments right now.');
      this.myCourses.set([]);
      this.exploreCourses.set([]);
      this.tutorEligible.set(false);
    } finally {
      this.loading.set(false);
    }
  }
}
