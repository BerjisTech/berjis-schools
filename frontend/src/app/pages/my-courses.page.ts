import { Component, OnInit, computed, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { RouterLink } from '@angular/router';
import { SchoolsService } from '../services/schools.service';
import { Course } from '../interfaces/course';

interface CourseMetrics {
  subjectCount: number;
  lessonCount: number;
  testCount: number;
}

@Component({
  selector: 'app-my-courses-page',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterLink],
  templateUrl: './my-courses.page.html'
})
export class MyCoursesPage implements OnInit {
  loading = signal(true);
  courses = signal<(Course & { metrics: CourseMetrics })[]>([]);
  selectedCourseId = signal<string | null>(null);
  editPayload = signal<{ title: string; description: string; visibility: 'public'|'private'|'school'; isPaid: boolean; priceCents: number } | null>(null);
  saving = signal(false);
  toast = signal<{ kind: 'success' | 'error'; text: string } | null>(null);

  filteredVisibility = signal<'all'|'public'|'private'|'school'>('all');

  filteredCourses = computed(() => {
    const vis = this.filteredVisibility();
    if (vis === 'all') return this.courses();
    return this.courses().filter(c => (c.visibility ?? 'public') === vis);
  });

  constructor(private svc: SchoolsService) {}

  async ngOnInit() {
    await this.loadCourses();
  }

  private async loadCourses() {
    this.loading.set(true);
    try {
      const list = await this.svc.listMyCourses();
      const enriched = list.map(course => ({
        ...course,
        metrics: {
          subjectCount: course.subjectCount ?? 0,
          lessonCount: course.lessonCount ?? 0,
          testCount: course.testCount ?? 0,
        },
      }));
      this.courses.set(enriched);
    } catch (e: any) {
      this.toast.set({ kind: 'error', text: e?.message || 'Unable to load your courses.' });
    } finally {
      this.loading.set(false);
    }
  }

  openEdit(course: Course & { metrics: CourseMetrics }) {
    this.selectedCourseId.set(`${course.id}`);
    this.editPayload.set({
      title: course.title ?? course.name,
      description: course.description ?? '',
      visibility: (course.visibility ?? 'public') as 'public'|'private'|'school',
      isPaid: !!course.isPaid,
      priceCents: course.priceCents ?? 0,
    });
  }

  cancelEdit() {
    this.selectedCourseId.set(null);
    this.editPayload.set(null);
  }

  async saveCourse() {
    const payload = this.editPayload();
    const courseId = this.selectedCourseId();
    if (!payload || !courseId) return;
    this.saving.set(true);
    try {
      const ok = await this.svc.updateCourse(courseId, {
        title: payload.title?.trim(),
        description: payload.description?.trim(),
        visibility: payload.visibility,
        isPaid: payload.isPaid,
        priceCents: payload.priceCents,
      });
      if (!ok) throw new Error('Update failed');
      this.toast.set({ kind: 'success', text: 'Course updated' });
      this.cancelEdit();
      await this.loadCourses();
    } catch (e: any) {
      this.toast.set({ kind: 'error', text: e?.message || 'Unable to update course.' });
    } finally {
      this.saving.set(false);
    }
  }

  dismissToast() {
    this.toast.set(null);
  }
}
