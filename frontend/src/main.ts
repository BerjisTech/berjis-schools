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

interface School { id: string; name: string; description?: string | null }
interface ClassItem { id: string; title: string; tutorUserId: string; schoolId?: string | null }
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
  { path: 'course/create', loadComponent: () => Promise.resolve(CreateCoursePage) },
  { path: 'course/:id', component: CourseOverviewComponent },
  { path: 'course/:id/lessons/:lessonId', component: LessonViewComponent },
  { path: 'course/:id/tests/:testId', component: TestViewComponent },
  { path: 'search', loadComponent: () => Promise.resolve(SearchPage) },
];

bootstrapApplication(AppComponent, { providers: [provideRouter(routes)] }).catch(err => console.error(err));
