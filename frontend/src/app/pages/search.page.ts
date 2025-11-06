import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { FormsModule } from '@angular/forms';
import { SchoolsService } from '../services/schools.service';
type JSONObject = Record<string, unknown>;

@Component({
  selector: 'app-search-page',
  standalone: true,
  imports: [CommonModule, RouterLink, FormsModule],
  templateUrl: './search.page.html'
})
export class SearchPage implements OnInit {
  q = '';
  loading = false;
  users: Array<{ userId: string; displayName?: string | null }> = [];
  tutors: Array<{ userId: string; bio?: string }> = [];
  schools: Array<{ id: string; name: string }> = [];
  classes: Array<{ id: string; title: string }> = [];
  subjects: Array<{ id: string; title: string }> = [];
  lessons: Array<{ id: string; title: string; classId: string }> = [];
  tests: Array<{ id: string; title: string; classId?: string }> = [];

  constructor(private route: ActivatedRoute, private svc: SchoolsService) {}

  ngOnInit() {
    this.route.queryParamMap.subscribe(async params => {
      this.q = (params.get('q') ?? '').trim();
      await this.run();
    });
  }

  async run() {
    this.loading = true;
    try {
      const q = this.q;
      const [users, schools, classes, subjects, lessons, tests, tutors] = await Promise.all([
        this.svc.search('user', q).catch(() => []),
        this.svc.search('school', q).catch(() => []),
        this.svc.search('class', q).catch(() => []),
        this.svc.search('subject', q).catch(() => []),
        this.svc.search('lesson', q).catch(() => []),
        this.svc.search('test', q).catch(() => []),
        this.svc.searchTutors(q).catch(() => []),
      ]);
      this.users = users.map((u: JSONObject) => ({ userId: String(u.userId ?? ''), displayName: (u.displayName as string | undefined) ?? null }));
      this.schools = schools.map((s: JSONObject) => ({ id: String(s.id ?? ''), name: String(s.name ?? '') }));
      this.classes = classes.map((c: JSONObject) => ({ id: String(c.id ?? ''), title: String(c.title ?? '') }));
      this.subjects = subjects.map((s: JSONObject) => ({ id: String(s.id ?? ''), title: String(s.title ?? '') }));
      this.lessons = lessons.map((l: JSONObject) => ({ id: String(l.id ?? ''), title: String(l.title ?? ''), classId: String(l.classId ?? '') }));
      this.tests = tests.map((t: JSONObject) => ({ id: String(t.id ?? ''), title: String(t.title ?? ''), classId: t.classId ? String(t.classId) : undefined }));
      this.tutors = tutors;
    } finally {
      this.loading = false;
    }
  }
}
