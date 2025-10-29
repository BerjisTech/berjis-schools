import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { RouterLink } from '@angular/router';
import { detectRootDomain, urlFor, loginUrl, verifySession } from '../../app/util';

interface School { id: string; name: string; description?: string | null }
interface ClassItem { id: string; title: string; tutorUserId: string; schoolId?: string | null }

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
  myTutors: string[] = [];
  inProgressLessonsCount: number | null = null;
  recentTests: { id: string; title: string }[] = [];
  tutorStatus: string | null = null;

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

  quickActions = [
    { label: 'My authored courses', href: '/courses/mine', icon: 'dashboard_customize', accent: 'bg-indigo-50 text-indigo-700' },
    { label: 'Manage school staff', href: '/schools/staff', icon: 'group', accent: 'bg-emerald-50 text-emerald-700' },
    { label: 'Explore classes', href: '/classes', icon: 'travel_explore', accent: 'bg-sky-50 text-sky-700' }
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
        const tops: { tutorUserId: string }[] = tj?.data ?? [];
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

      const sRes = await fetch(`${urlFor('schools-api')}/v1/schools`, { credentials: 'include' });
      const sJ = await sRes.json();
      const schools: School[] = sJ?.data ?? [];
      const set = new Set(schools.map(s => s.id));
      this.mySchools = schools.filter(s => set.has(s.id));

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
      } catch { this.tutorStatus = null }
    } catch {
      this.mySchools = [];
      this.myTutors = [];
      this.inProgressLessonsCount = 0;
      this.recentTests = [];
      this.tutorStatus = null;
    }
  }
}

