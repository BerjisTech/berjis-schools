import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { RouterLink } from '@angular/router';
import { detectRootDomain, urlFor, loginUrl, verifySession } from '../../app/util';

interface School { id: string; name: string; description?: string | null }
interface ClassItem { id: string; title: string; tutorUserId: string; schoolId?: string | null; isEnrolled?: boolean }

@Component({
  selector: 'app-home-page',
  standalone: true,
  imports: [CommonModule, RouterLink],
  templateUrl: './home.page.html'
})
export class HomePage implements OnInit {
  authed = false;
  domainHint = '';
  loginHref = loginUrl();
  featuredSchools: School[] = [];
  featuredTutors: string[] = [];
  topLeaderboardLine: string | null = null;
  mySchools: School[] = [];
  adminSchools: School[] = [];
  tutorSchools: School[] = [];
  studentSchools: School[] = [];
  staffSchools: School[] = [];
  myTutors: string[] = [];
  inProgressLessonsCount: number | null = null;
  recentTests: Array<{ id: string; title: string }> = [];
  tutorStatus: string | null = null;
  tutorStatusDisplay = 'Not applied yet';
  enrolledClasses: ClassItem[] = [];
  isAdmin = false;
  isTutor = false;
  isStaff = false;
  tutorActionLabel = 'Become a tutor';
  tutorActionHelper = 'Apply to teach with Berjis.';
  tutorActionHref = '/tutors/become';
  primaryActions: Array<{ label: string; href: string; icon: string }> = [
    { label: 'Continue lessons', href: '/lessons', icon: 'play_circle' },
    { label: 'My assessments', href: '/tests', icon: 'quiz' },
    { label: 'Browse classes', href: '/classes', icon: 'travel_explore' },
    { label: 'Become a tutor', href: '/tutors/become', icon: 'person_add' },
    { label: 'Create a school', href: '/schools/create', icon: 'domain_add' },
  ];

  platformHighlights = [
    {
      icon: 'hub',
      title: 'Unified learning journeys',
      body: 'Blend recorded lessons, instructor-led sessions, peer rooms, and simulations in one cohesive experience.'
    },
    {
      icon: 'assignment_turned_in',
      title: 'Assessments that adapt',
      body: 'Mix objective and subjective testing, schedule class promotions, and surface insights for tutors and admins.'
    },
    {
      icon: 'payments',
      title: 'Monetise your expertise',
      body: 'Sell private courses, run cohorts, or embed your school brand with streamlined payouts and pricing controls.'
    }
  ];

  personaStories = [
    {
      title: 'Learners & Families',
      description: 'Discover curated classes, measure mastery, and invite guardians to monitor progress with real-time reports.'
    },
    {
      title: 'Tutors & Instructors',
      description: 'Publish modular courses, launch live workshops, build simulations, and earn from private or school-backed programs.'
    },
    {
      title: 'Schools & Organisations',
      description: 'Spin up digital campuses, orchestrate staff, manage classes, and move cohorts through structured promotions.'
    }
  ];

  async ngOnInit() {
    const root = detectRootDomain(window.location.hostname);
    this.domainHint = `Domain: ${root}`;

    try {
      const result = await verifySession({ attemptRefresh: true });
      this.authed = !!result.valid;
    } catch {}

    await this.loadFeatured();
    if (this.authed) await this.loadDashboard();
  }

  private async loadFeatured() {
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/schools`, { credentials: 'include' });
      const j = await res.json();
      const list: School[] = j?.data ?? [];
      this.featuredSchools = list.slice(0, 5);
      try {
        const tRes = await fetch(`${urlFor('schools-api')}/v1/ratings/tutors/top?limit=5`, { credentials: 'include' });
        const tj = await tRes.json();
      const tops: Array<{ tutorUserId: string }> = tj?.data ?? [];
        this.featuredTutors = tops.map(t => t.tutorUserId);
      } catch { this.featuredTutors = ['Top Tutor'] }
      try {
        const lRes = await fetch(`${urlFor('schools-api')}/v1/leaderboards/public?limit=1`);
        const lj = await lRes.json();
        const top = (lj?.data ?? [])[0];
        if (top?.score != null) this.topLeaderboardLine = `Top public score: ${Math.round(top.score)}%`;
      } catch { this.topLeaderboardLine = null }
    } catch {
      this.featuredSchools = [];
      this.featuredTutors = ['Top Tutor'];
    }
  }

  private async loadDashboard() {
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/classes`, { credentials: 'include' });
      const j = await res.json();
      const classes: ClassItem[] = j?.data ?? [];
      const tutors = new Set<string>();
      this.myTutors = [];
      for (const c of classes) if (c?.tutorUserId) tutors.add(c.tutorUserId);
      this.myTutors = Array.from(tutors);
      this.enrolledClasses = classes.filter(c => !!c?.isEnrolled);

      const [adminSchools, tutorSchools, studentSchools] = await Promise.all([
        this.fetchSchoolsByRole('admin'),
        this.fetchSchoolsByRole('tutor'),
        this.fetchSchoolsByRole('student')
      ]);
      this.adminSchools = adminSchools;
      this.tutorSchools = tutorSchools;
      this.studentSchools = studentSchools;
      this.staffSchools = this.mergeSchools(adminSchools, tutorSchools);
      this.mySchools = this.staffSchools;

      try {
        const lpRes = await fetch(`${urlFor('schools-api')}/v1/progress/lessons?status=in_progress`, { credentials: 'include' });
        const lpJ = await lpRes.json();
        const rows: any[] = lpJ?.data ?? [];
        this.inProgressLessonsCount = rows.length;
      } catch { this.inProgressLessonsCount = 0 }

      try {
        const tRes = await fetch(`${urlFor('schools-api')}/v1/tests`, { credentials: 'include' });
        const tJ = await tRes.json();
        const tests: any[] = tJ?.data ?? [];
        this.recentTests = tests.slice(0, 5).map(t => ({ id: t.id, title: t.title }));
      } catch { this.recentTests = [] }

      // Tutor application status
      try {
        const meRes = await fetch(`${urlFor('schools-api')}/v1/tutors/me`, { credentials: 'include' });
        const meJ = await meRes.json();
        this.tutorStatus = meJ?.data?.status ?? null;
        this.tutorStatusDisplay = this.tutorStatus ? this.tutorStatus.replace(/_/g, ' ') : 'Not applied yet';
      } catch { this.tutorStatus = null }

      const normalizedStatus = (this.tutorStatus ?? '').toLowerCase();
      this.isAdmin = this.adminSchools.length > 0;
      this.isTutor = ['approved', 'active', 'onboarded'].includes(normalizedStatus) || this.tutorSchools.length > 0;
      this.isStaff = this.isAdmin || this.isTutor;
      if (!this.isStaff) {
        this.mySchools = [];
      }
      this.buildActions();
    } catch {
      this.mySchools = [];
      this.adminSchools = [];
      this.tutorSchools = [];
      this.studentSchools = [];
      this.staffSchools = [];
      this.myTutors = [];
      this.inProgressLessonsCount = 0;
      this.recentTests = [];
      this.tutorStatus = null;
      this.tutorStatusDisplay = 'Not applied yet';
      this.enrolledClasses = [];
      this.isAdmin = false;
      this.isTutor = false;
      this.isStaff = false;
      this.buildActions();
    }
  }

  private async fetchSchoolsByRole(role: 'admin' | 'tutor' | 'student'): Promise<School[]> {
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/schools/mine?role=${role}`, { credentials: 'include' });
      if (!res.ok) return [];
      const j = await res.json();
      return (j?.data ?? []) as School[];
    } catch {
      return [];
    }
  }

  private mergeSchools(...lists: School[][]): School[] {
    const map = new Map<string, School>();
    for (const list of lists) {
      for (const school of list) {
        if (school?.id && !map.has(school.id)) {
          map.set(school.id, school);
        }
      }
    }
    return Array.from(map.values());
  }

  private buildActions() {
    const isTutorApproved = this.isTutor;
    const tutorAction = isTutorApproved
      ? { label: 'Tutor console', href: '/tutors/become', icon: 'workspace_premium' }
      : { label: 'Become a tutor', href: '/tutors/become', icon: 'person_add' };
    this.tutorActionLabel = tutorAction.label;
    this.tutorActionHelper = isTutorApproved ? 'Launch the teaching workspace.' : 'Apply to teach with Berjis.';
    this.tutorActionHref = tutorAction.href;

    if (this.isStaff) {
      this.primaryActions = [
        { label: 'Teach & curriculum', href: '/courses/mine', icon: 'menu_book' },
        { label: 'Student management', href: '/schools/staff', icon: 'assignment_ind' },
        { label: 'Finance workspace', href: '/schools/staff#finance', icon: 'payments' },
        { label: 'Family support', href: '/moderation', icon: 'family_restroom' },
        { label: 'Create a school', href: '/schools/create', icon: 'domain_add' },
        tutorAction,
      ];
    } else {
      this.primaryActions = [
        { label: 'Continue lessons', href: '/lessons', icon: 'play_circle' },
        { label: 'My assessments', href: '/tests', icon: 'quiz' },
        { label: 'Browse classes', href: '/classes', icon: 'travel_explore' },
        tutorAction,
        { label: 'Create a school', href: '/schools/create', icon: 'domain_add' },
      ];
    }
  }
}
