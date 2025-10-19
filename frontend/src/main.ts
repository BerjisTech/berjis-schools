import { bootstrapApplication } from '@angular/platform-browser';
import { Component } from '@angular/core';
import { CommonModule } from '@angular/common';
import { RouterModule, Routes, RouterLink, provideRouter } from '@angular/router';
import { HomePage } from './app/pages/home.page';
import { ClassesPage } from './app/pages/classes.page';
import { SubjectsPage } from './app/pages/subjects.page';
import { LessonsPage } from './app/pages/lessons.page';
import { TestsPage } from './app/pages/tests.page';
import { CreateSchoolPage } from './app/pages/create-school.page';
import { SchoolStaffPage } from './app/pages/school-staff.page';
import { BecomeTutorPage } from './app/pages/become-tutor.page';
import { ModerationPage } from './app/pages/moderation.page';
import { PlatformModerationPage } from './app/pages/platform-moderation.page';
import { TutorReviewPage } from './app/pages/tutor-review.page';
import { SchoolReviewPage } from './app/pages/school-review.page';

interface School { id: string; name: string; description?: string | null }
interface ClassItem { id: string; title: string; tutorUserId: string; schoolId?: string | null }

@Component({
  selector: 'app-root',
  standalone: true,
  imports: [CommonModule, RouterModule, RouterLink],
  template: `
    <router-outlet></router-outlet>
  `
})
class AppComponent {}
const routes: Routes = [
  { path: '', loadComponent: () => Promise.resolve(HomePage) },
  { path: 'classes', loadComponent: () => Promise.resolve(ClassesPage) },
  { path: 'subjects', loadComponent: () => Promise.resolve(SubjectsPage) },
  { path: 'lessons', loadComponent: () => Promise.resolve(LessonsPage) },
  { path: 'tests', loadComponent: () => Promise.resolve(TestsPage) },
  { path: 'schools/create', loadComponent: () => Promise.resolve(CreateSchoolPage) },
  { path: 'schools/staff', loadComponent: () => Promise.resolve(SchoolStaffPage) },
  { path: 'tutors/become', loadComponent: () => Promise.resolve(BecomeTutorPage) },
  { path: 'moderation', loadComponent: () => Promise.resolve(ModerationPage) },
  { path: 'moderation/platform', loadComponent: () => Promise.resolve(PlatformModerationPage) },
  { path: 'moderation/tutors', loadComponent: () => Promise.resolve(TutorReviewPage) },
  { path: 'moderation/schools', loadComponent: () => Promise.resolve(SchoolReviewPage) },
];

bootstrapApplication(AppComponent, { providers: [provideRouter(routes)] }).catch(err => console.error(err));
