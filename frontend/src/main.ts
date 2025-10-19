import { bootstrapApplication } from '@angular/platform-browser';
import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { detectRootDomain, urlFor, loginUrl } from './app/util';

interface School { id: string; name: string; description?: string | null }
interface ClassItem { id: string; title: string; tutorUserId: string; schoolId?: string | null }

@Component({
  selector: 'app-root',
  standalone: true,
  imports: [CommonModule],
  template: `
    <div style="font-family: system-ui, sans-serif; padding: 1.25rem; max-width: 1100px; margin: 0 auto;">
      <header style="display:flex; align-items:center; justify-content:space-between; gap:1rem; margin-bottom: 1rem;">
        <h1 style="margin:0; font-size:1.4rem;">Berjis Schools</h1>
        <div style="opacity:.7; font-size:.9rem;">{{ domainHint }}</div>
      </header>

      <section *ngIf="!authed; else dashboard">
        <div style="display:grid; gap:1rem; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));">
          <div style="border:1px solid #ddd; border-radius:12px; padding:1rem;">
            <h3 style="margin-top:0">Explore Top Schools</h3>
            <p>See featured schools and programs. Join and learn.</p>
            <ul>
              <li *ngFor="let s of featuredSchools">{{ s.name }}<span *ngIf="s.description"> — {{ s.description }}</span></li>
            </ul>
          </div>
          <div style="border:1px solid #ddd; border-radius:12px; padding:1rem;">
            <h3 style="margin-top:0">Best Tutors</h3>
            <p>Discover highly rated tutors across subjects.</p>
            <ul>
              <li *ngFor="let t of featuredTutors">{{ t }}</li>
            </ul>
          </div>
          <div style="border:1px solid #ddd; border-radius:12px; padding:1rem;">
            <h3 style="margin-top:0">See How You Compare</h3>
            <p>Take a public test and compare against top students.</p>
            <p style="font-size:.9rem; opacity:.8">Example: "Best in Physics scored 90%"</p>
          </div>
        </div>
        <div style="margin-top:1.25rem;">
          <a [href]="loginHref" style="display:inline-block; background:#2563eb; color:#fff; padding:.7rem 1rem; border-radius:10px; text-decoration:none;">Get Started →</a>
        </div>
      </section>

      <ng-template #dashboard>
        <div style="display:grid; gap:1rem; grid-template-columns: 1fr;">
          <div style="border:1px solid #eee; border-radius:10px; padding:1rem;">
            <h3 style="margin-top:0">My Schools</h3>
            <div *ngIf="mySchools?.length; else noSchools">
              <ul>
                <li *ngFor="let s of mySchools">{{ s.name }}</li>
              </ul>
            </div>
            <ng-template #noSchools><div style="opacity:.7">No schools yet.</div></ng-template>
          </div>

          <div style="display:grid; gap:1rem; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));">
            <div style="border:1px solid #eee; border-radius:10px; padding:1rem;">
              <h4 style="margin-top:0">Ongoing Lessons</h4>
              <div style="opacity:.7">Coming soon</div>
            </div>
            <div style="border:1px solid #eee; border-radius:10px; padding:1rem;">
              <h4 style="margin-top:0">Recent Tests</h4>
              <div style="opacity:.7">Coming soon</div>
            </div>
            <div style="border:1px solid #eee; border-radius:10px; padding:1rem;">
              <h4 style="margin-top:0">Unfinished Homework</h4>
              <div style="opacity:.7">Coming soon</div>
            </div>
            <div style="border:1px solid #eee; border-radius:10px; padding:1rem;">
              <h4 style="margin-top:0">Teachers</h4>
              <ul>
                <li *ngFor="let t of myTutors">{{ t }}</li>
              </ul>
            </div>
          </div>
        </div>
      </ng-template>
    </div>
  `
})
class AppComponent implements OnInit {
  authed = false;
  domainHint = '';
  loginHref = loginUrl();
  featuredSchools: School[] = [];
  featuredTutors: string[] = [];
  mySchools: School[] = [];
  myTutors: string[] = [];

  async ngOnInit() {
    const root = detectRootDomain(window.location.hostname);
    this.domainHint = `Domain: ${root}`;

    // Determine auth via core API verify
    try {
      const verify = await fetch(`${urlFor('api')}/v1/auth/verify`, { credentials: 'include' });
      const v = await verify.json();
      this.authed = !!(v?.data?.valid);
    } catch {}

    // Always show some featured content
    await this.loadFeatured();

    if (this.authed) {
      await this.loadDashboard();
    }
  }

  private async loadFeatured() {
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/schools`, { credentials: 'include' });
      const j = await res.json();
      const list: School[] = j?.data ?? [];
      this.featuredSchools = list.slice(0, 5);
      this.featuredTutors = ['Seed Tutor', 'Top Math Tutor'];
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
      // derive tutors and schools
      const tutors = new Set<string>();
      this.myTutors = [];
      for (const c of classes) if (c?.tutorUserId) tutors.add(c.tutorUserId);
      this.myTutors = Array.from(tutors);

      // map school IDs to names using fetched schools
      const sRes = await fetch(`${urlFor('schools-api')}/v1/schools`, { credentials: 'include' });
      const sJ = await sRes.json();
      const schools: School[] = sJ?.data ?? [];
      const set = new Set(schools.map(s => s.id));
      this.mySchools = schools.filter(s => set.has(s.id));
    } catch {
      this.mySchools = [];
      this.myTutors = [];
    }
  }
}

bootstrapApplication(AppComponent).catch(err => console.error(err));