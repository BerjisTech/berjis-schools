import { Component, OnInit, computed, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { RouterLink } from '@angular/router';
import { LessonCaptionsComponent } from '../components/course/lesson-captions.component';
import { SchoolsService } from '../services/schools.service';
import { Course, Lesson, Subject } from '../interfaces/course';

interface SubjectLessons {
  subject: Subject;
  lessons: Lesson[];
}

interface CourseLessons {
  course: Course;
  subjects: SubjectLessons[];
  isOwner: boolean;
}

@Component({
  selector: 'app-lessons-page',
  standalone: true,
  imports: [CommonModule, RouterLink, LessonCaptionsComponent],
  templateUrl: './lessons.page.html'
})
export class LessonsPage implements OnInit {
  private readonly maxExploreCourses = 6;

  loading = signal(true);
  error = signal<string | null>(null);
  tutorEligible = signal(false);
  myCourses = signal<CourseLessons[]>([]);
  exploreCourses = signal<Course[]>([]);

  // Resources open state per lesson
  resourcesOpen = signal<Record<string, boolean>>({});
  // Share modal state
  shareOpen = signal(false);
  shareForResource = signal<string | null>(null);
  shareUserId = signal('');
  shareRole = signal<'owner'|'editor'|'commenter'|'viewer'>('viewer');
  shareRows = signal<Array<{ id: string; principalType: 'user'|'class'|'group'|'school'; principalId: string; role: 'owner'|'editor'|'commenter'|'viewer'; createdBy?: string; createdAt?: string }>>([]);
  shareQuery = signal('');
  shareSuggestions = signal<Array<{ userId: string; displayName?: string }>>([]);

  totalLessons = computed(() => {
    return this.myCourses().reduce((sum, entry) => {
      for (const subject of entry.subjects) sum += subject.lessons.length;
      return sum;
    }, 0);
  });
  hasLessons = computed(() => this.totalLessons() > 0);

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
          let lessons: Lesson[] = [];
          try {
            lessons = await this.svc.listLessons(subject.id);
          } catch {
            lessons = [];
          }
          return { subject, lessons } as SubjectLessons;
        }));

        return { course, subjects: subjectEntries, isOwner } as CourseLessons;
      }));

      const filtered = entries.filter((entry): entry is CourseLessons => !!entry);
      filtered.sort((a, b) => {
        const aTitle = (a.course.title || a.course.name || '').toLowerCase();
        const bTitle = (b.course.title || b.course.name || '').toLowerCase();
        return aTitle.localeCompare(bTitle);
      });

      this.myCourses.set(filtered);
      const ownsCourse = filtered.some(entry => entry.isOwner);
      const hasTeachingLessons = filtered.some(entry => entry.subjects.some(s => s.lessons.length > 0));
      this.tutorEligible.set(tutorFlag || ownsCourse || hasTeachingLessons);
    } catch (e: any) {
      this.error.set(e?.message || 'Unable to load lessons right now.');
      this.myCourses.set([]);
      this.exploreCourses.set([]);
      this.tutorEligible.set(false);
    } finally {
      this.loading.set(false);
    }
  }

  async toggleResources(lessonId: string) { const cur = { ...this.resourcesOpen() }; cur[lessonId] = !cur[lessonId]; this.resourcesOpen.set(cur); }
  openShare(resourceId: string){ this.shareForResource.set(resourceId); this.shareOpen.set(true); this.loadAcl(resourceId); }
  async addUserShare(){ const rid=this.shareForResource(); const uid=this.shareUserId().trim(); if(!rid||!uid) return; const role=this.shareRole(); await this.svc.grantResourceRole(rid, 'user', uid, role); this.shareUserId.set(''); }
  private async loadAcl(resourceId: string){ try { this.shareRows.set(await this.svc.listResourceAcl(resourceId)); } catch { this.shareRows.set([]); } }
  async revokeShare(principalType: 'user'|'class'|'group'|'school', principalId: string){ const rid=this.shareForResource(); if(!rid) return; await this.svc.revokeResourceRole(rid, principalType, principalId); await this.loadAcl(rid); }
  async onShareQueryChange(q: string){ this.shareQuery.set(q); if ((q||'').trim().length < 2) { this.shareSuggestions.set([]); return; } this.shareSuggestions.set(await this.svc.searchUsers(q)); }
  pickSuggestion(u: { userId: string; displayName?: string }){ this.shareUserId.set(u.userId); this.shareQuery.set(u.displayName || u.userId); this.shareSuggestions.set([]); }
}
