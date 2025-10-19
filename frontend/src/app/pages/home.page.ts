import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { RouterLink } from '@angular/router';
import { detectRootDomain, urlFor, loginUrl } from '../../app/util';

interface School { id: string; name: string; description?: string | null }
interface ClassItem { id: string; title: string; tutorUserId: string; schoolId?: string | null }

@Component({
  selector: 'app-home-page',
  standalone: true,
  imports: [CommonModule, RouterLink],
  template: `
    <div class="font-sans p-5 max-w-[1100px] mx-auto">
      <header class="flex items-center justify-between gap-4 mb-4">
        <h1 class="m-0 text-xl font-semibold">Berjis Schools</h1>
        <div class="opacity-70 text-sm">{{ domainHint }}</div>
      </header>

      <section *ngIf="!authed; else dashboard">
        <div class="grid gap-4 [grid-template-columns:repeat(auto-fit,minmax(260px,1fr))]">
          <div class="border border-gray-200 rounded-xl p-4">
            <h3 class="mt-0 font-semibold">Explore Top Schools</h3>
            <p class="mb-2">See featured schools and programs. Join and learn.</p>
            <ul class="list-disc pl-5">
              <li *ngFor="let s of featuredSchools">{{ s.name }}<span *ngIf="s.description"> — {{ s.description }}</span></li>
            </ul>
            <a routerLink="/classes" class="text-sm text-blue-600 hover:underline">View classes →</a>
          </div>
          <div class="border border-gray-200 rounded-xl p-4">
            <h3 class="mt-0 font-semibold">Best Tutors</h3>
            <p class="mb-2">Discover highly rated tutors across subjects.</p>
            <ul class="list-disc pl-5">
              <li *ngFor="let t of featuredTutors">{{ t }}</li>
            </ul>
          </div>
          <div class="border border-gray-200 rounded-xl p-4">
            <h3 class="mt-0 font-semibold">See How You Compare</h3>
            <p>Take a public test and compare against top students.</p>
            <p *ngIf="topLeaderboardLine; else lbFallback" class="text-sm opacity-80">{{ topLeaderboardLine }}</p>
            <ng-template #lbFallback>
              <p class="text-sm opacity-80">Example: "Best in Physics scored 90%"</p>
            </ng-template>
            <a routerLink="/tests" class="text-sm text-blue-600 hover:underline">Browse tests →</a>
          </div>
        </div>
        <div class="mt-5">
          <a [href]="loginHref" class="inline-block bg-blue-600 text-white px-4 py-2 rounded-lg no-underline">Get Started →</a>
        </div>
      </section>

      <ng-template #dashboard>
        <div class="grid gap-4 grid-cols-1">
          <div class="border border-gray-200 rounded-xl p-4">
            <h3 class="mt-0 font-semibold">My Schools</h3>
            <div *ngIf="mySchools?.length; else noSchools">
              <ul class="list-disc pl-5">
                <li *ngFor="let s of mySchools">{{ s.name }}</li>
              </ul>
            </div>
            <ng-template #noSchools><div class="opacity-70">No schools yet.</div></ng-template>
            <div class="mt-3 flex gap-3">
              <a routerLink="/schools/create" class="text-sm text-blue-600 hover:underline">Create a school</a>
              <a routerLink="/schools/staff" class="text-sm text-blue-600 hover:underline">Manage staff</a>
            </div>
          </div>

          <div class="grid gap-4 [grid-template-columns:repeat(auto-fit,minmax(260px,1fr))]">
            <div class="border border-gray-200 rounded-xl p-4">
              <h4 class="mt-0 font-semibold">Ongoing Lessons</h4>
              <div *ngIf="inProgressLessonsCount !== null; else lp">
                {{ inProgressLessonsCount }} in progress
              </div>
              <ng-template #lp><div class="opacity-70">Loading…</div></ng-template>
              <a routerLink="/lessons" class="text-sm text-blue-600 hover:underline">Go to lessons →</a>
            </div>
            <div class="border border-gray-200 rounded-xl p-4">
              <h4 class="mt-0 font-semibold">Recent Tests</h4>
              <ul *ngIf="recentTests?.length; else rt" class="list-disc pl-5">
                <li *ngFor="let t of recentTests">{{ t.title }}</li>
              </ul>
              <ng-template #rt><div class="opacity-70">Loading…</div></ng-template>
              <a routerLink="/tests" class="text-sm text-blue-600 hover:underline">See all tests →</a>
            </div>
            <div class="border border-gray-200 rounded-xl p-4">
              <h4 class="mt-0 font-semibold">Unfinished Homework</h4>
              <div *ngIf="inProgressLessonsCount !== null; else hw">
                {{ inProgressLessonsCount }} items to finish
              </div>
              <ng-template #hw><div class="opacity-70">Loading…</div></ng-template>
            </div>
            <div class="border border-gray-200 rounded-xl p-4">
              <h4 class="mt-0 font-semibold">Teachers</h4>
              <ul class="list-disc pl-5">
                <li *ngFor="let t of myTutors">{{ t }}</li>
              </ul>
              <a routerLink="/tutors/become" class="text-sm text-blue-600 hover:underline">Become a private tutor</a>
            </div>
            <div *ngIf="tutorStatus==='draft'" class="border border-yellow-200 rounded-xl p-4 bg-yellow-50">
              <h4 class="mt-0 font-semibold">Continue Tutor Enrollment</h4>
              <p class="text-sm text-gray-700">Your application is in draft. Complete the remaining steps to submit for review.</p>
              <a routerLink="/tutors/become" class="text-sm text-blue-600 hover:underline">Continue enrollment →</a>
            </div>
            <div class="border border-gray-200 rounded-xl p-4">
              <h4 class="mt-0 font-semibold">Safety & Moderation</h4>
              <p class="mb-2 text-sm text-gray-600">Report problematic content or manage your blocks.</p>
              <a routerLink="/moderation" class="text-sm text-blue-600 hover:underline">Open moderation tools</a>
            </div>
          </div>
        </div>
      </ng-template>
    </div>
  `
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

  async ngOnInit() {
    const root = detectRootDomain(window.location.hostname);
    this.domainHint = `Domain: ${root}`;

    try {
      const verify = await fetch(`${urlFor('api')}/v1/auth/verify`, { credentials: 'include' });
      const v = await verify.json();
      this.authed = !!(v?.data?.valid);
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
