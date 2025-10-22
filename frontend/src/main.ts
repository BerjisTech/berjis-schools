import { bootstrapApplication } from '@angular/platform-browser';
import { Routes, provideRouter } from '@angular/router';
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
import { CourseOverviewComponent } from './app/components/course/course-overview.component';
import { LessonViewComponent } from './app/components/course/lesson-view.component';
import { TestViewComponent } from './app/components/course/test-view.component';
import { CreateCoursePage } from './app/pages/create-course.page';
import { AppComponent } from './app/app.component';
import { SearchPage } from './app/pages/search.page';
import { authGuard } from './app/guards/auth.guard';

interface School { id: string; name: string; description?: string | null }
interface ClassItem { id: string; title: string; tutorUserId: string; schoolId?: string | null }
const routes: Routes = [
  { path: '', loadComponent: () => Promise.resolve(HomePage) },
  { path: 'classes', loadComponent: () => Promise.resolve(ClassesPage) },
  { path: 'subjects', loadComponent: () => Promise.resolve(SubjectsPage) },
  { path: 'lessons', loadComponent: () => Promise.resolve(LessonsPage), canActivate: [authGuard] },
  { path: 'tests', loadComponent: () => Promise.resolve(TestsPage), canActivate: [authGuard] },
  { path: 'schools/create', loadComponent: () => Promise.resolve(CreateSchoolPage), canActivate: [authGuard] },
  { path: 'schools/staff', loadComponent: () => Promise.resolve(SchoolStaffPage), canActivate: [authGuard] },
  { path: 'tutors/become', loadComponent: () => Promise.resolve(BecomeTutorPage), canActivate: [authGuard] },
  { path: 'moderation', loadComponent: () => Promise.resolve(ModerationPage), canActivate: [authGuard] },
  { path: 'moderation/platform', loadComponent: () => Promise.resolve(PlatformModerationPage), canActivate: [authGuard] },
  { path: 'moderation/tutors', loadComponent: () => Promise.resolve(TutorReviewPage), canActivate: [authGuard] },
  { path: 'moderation/schools', loadComponent: () => Promise.resolve(SchoolReviewPage), canActivate: [authGuard] },
  { path: 'course/create', loadComponent: () => Promise.resolve(CreateCoursePage), canActivate: [authGuard] },
  { path: 'course/:id', component: CourseOverviewComponent },
  { path: 'course/:id/lessons/:lessonId', component: LessonViewComponent, canActivate: [authGuard] },
  { path: 'course/:id/tests/:testId', component: TestViewComponent, canActivate: [authGuard] },
  { path: 'search', loadComponent: () => Promise.resolve(SearchPage) },
];

bootstrapApplication(AppComponent, { providers: [provideRouter(routes)] }).catch(err => console.error(err));
