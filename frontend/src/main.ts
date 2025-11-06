import { bootstrapApplication } from '@angular/platform-browser';
import { Routes, provideRouter } from '@angular/router';
import { HomePage } from './app/pages/home.page';
import { ClassesPage } from './app/pages/classes.page';
import { SubjectsPage } from './app/pages/subjects.page';
import { LessonsPage } from './app/pages/lessons.page';
import { TestsPage } from './app/pages/tests.page';
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
import { MyCoursesPage } from './app/pages/my-courses.page';
import { CreateSchoolPage } from './app/pages/create-school.page';
import { GradebookPage } from './app/pages/gradebook.page';
import { TranscriptPage } from './app/pages/transcript.page';
import { DiscussionsPage } from './app/pages/discussions.page';
import { GroupsPage } from './app/pages/groups.page';
import { ClassAppointmentsPage } from './app/pages/class-appointments.page';
import { CertificatesPage } from './app/pages/certificates.page';
import { VerifyCertificatePage } from './app/pages/verify-certificate.page';
import { MessagesPage } from './app/pages/messages.page';

interface School { id: string; name: string; description?: string | null }
interface ClassItem { id: string; title: string; tutorUserId: string; schoolId?: string | null }
const routes: Routes = [
  { path: '', loadComponent: () => Promise.resolve(HomePage) },
  { path: 'classes', loadComponent: () => Promise.resolve(ClassesPage) },
  { path: 'subjects', loadComponent: () => Promise.resolve(SubjectsPage) },
  { path: 'lessons', loadComponent: () => Promise.resolve(LessonsPage), canActivate: [authGuard] },
  { path: 'tests', loadComponent: () => Promise.resolve(TestsPage), canActivate: [authGuard] },
  { path: 'schools/create', redirectTo: 'schools/create/info', pathMatch: 'full' },
  { path: 'schools/create/:section', loadComponent: () => Promise.resolve(CreateSchoolPage), canActivate: [authGuard] },
  { path: 'schools/staff', loadComponent: () => Promise.resolve(SchoolStaffPage), canActivate: [authGuard] },
  { path: 'tutors/become', redirectTo: 'tutors/become/info', pathMatch: 'full' },
  { path: 'tutors/become/:section', loadComponent: () => Promise.resolve(BecomeTutorPage), canActivate: [authGuard] },
  { path: 'moderation', loadComponent: () => Promise.resolve(ModerationPage), canActivate: [authGuard] },
  { path: 'moderation/platform', loadComponent: () => Promise.resolve(PlatformModerationPage), canActivate: [authGuard] },
  { path: 'moderation/tutors', loadComponent: () => Promise.resolve(TutorReviewPage), canActivate: [authGuard] },
  { path: 'moderation/schools', loadComponent: () => Promise.resolve(SchoolReviewPage), canActivate: [authGuard] },
  { path: 'course/create', loadComponent: () => Promise.resolve(CreateCoursePage), canActivate: [authGuard] },
  { path: 'courses/mine', loadComponent: () => Promise.resolve(MyCoursesPage), canActivate: [authGuard] },
  { path: 'gradebook', loadComponent: () => Promise.resolve(GradebookPage), canActivate: [authGuard] },
  { path: 'transcript', loadComponent: () => Promise.resolve(TranscriptPage), canActivate: [authGuard] },
  { path: 'certificates', loadComponent: () => Promise.resolve(CertificatesPage), canActivate: [authGuard] },
  { path: 'verify', loadComponent: () => Promise.resolve(VerifyCertificatePage) },
  { path: 'messages', loadComponent: () => Promise.resolve(MessagesPage), canActivate: [authGuard] },
  { path: 'course/:id', component: CourseOverviewComponent },
  { path: 'course/:id/lessons/:lessonId', component: LessonViewComponent, canActivate: [authGuard] },
  { path: 'course/:id/tests/:testId', component: TestViewComponent, canActivate: [authGuard] },
  { path: 'class/:id/discussions', loadComponent: () => Promise.resolve(DiscussionsPage), canActivate: [authGuard] },
  { path: 'class/:id/groups', loadComponent: () => Promise.resolve(GroupsPage), canActivate: [authGuard] },
  { path: 'class/:id/appointments', loadComponent: () => Promise.resolve(ClassAppointmentsPage), canActivate: [authGuard] },
  { path: 'search', loadComponent: () => Promise.resolve(SearchPage) },
];

bootstrapApplication(AppComponent, { providers: [provideRouter(routes)] }).catch(err => console.error(err));
