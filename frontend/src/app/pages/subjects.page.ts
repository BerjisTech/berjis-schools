import { Component, OnInit, computed, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { RouterLink } from '@angular/router';
import { SchoolsService } from '../services/schools.service';
import { Course, Subject } from '../interfaces/course';

interface CourseSubjects {
  course: Course;
  subjects: Subject[];
  isOwner: boolean;
}

@Component({
  selector: 'app-subjects-page',
  standalone: true,
  imports: [CommonModule, RouterLink],
  templateUrl: './subjects.page.html'
})
export class SubjectsPage implements OnInit {
  private readonly maxExploreCourses = 6;

  loading = signal(true);
  error = signal<string | null>(null);
  tutorEligible = signal(false);
  myCourses = signal<CourseSubjects[]>([]);
  exploreCourses = signal<Course[]>([]);

  totalSubjects = computed(() => this.myCourses().reduce((sum, entry) => sum + entry.subjects.length, 0));
  hasSubjects = computed(() => this.totalSubjects() > 0);

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
        return { course, subjects, isOwner } as CourseSubjects;
      }));

      const filtered = entries.filter((entry): entry is CourseSubjects => !!entry);
      filtered.sort((a, b) => {
        const aTitle = (a.course.title || a.course.name || '').toLowerCase();
        const bTitle = (b.course.title || b.course.name || '').toLowerCase();
        return aTitle.localeCompare(bTitle);
      });

      this.myCourses.set(filtered);
      const ownsCourse = filtered.some(entry => entry.isOwner);
      this.tutorEligible.set(tutorFlag || ownsCourse);
    } catch (e: any) {
      this.error.set(e?.message || 'Unable to load subjects right now.');
      this.myCourses.set([]);
      this.exploreCourses.set([]);
      this.tutorEligible.set(false);
    } finally {
      this.loading.set(false);
    }
  }
}
