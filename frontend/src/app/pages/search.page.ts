import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { FormsModule } from '@angular/forms';
import { SchoolsService } from '../services/schools.service';

@Component({
  selector: 'app-search-page',
  standalone: true,
  imports: [CommonModule, RouterLink, FormsModule],
  templateUrl: './search.page.html'
})
export class SearchPage implements OnInit {
  q = '';
  loading = false;
  users: { userId: string; displayName?: string | null }[] = [];
  tutors: { userId: string; bio?: string }[] = [];
  schools: { id: string; name: string }[] = [];
  classes: { id: string; title: string }[] = [];
  subjects: { id: string; title: string }[] = [];
  lessons: { id: string; title: string; classId: string }[] = [];
  tests: { id: string; title: string; classId?: string }[] = [];

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
      this.users = users.map((u: any) => ({ userId: u.userId, displayName: u.displayName ?? null }));
      this.schools = schools.map((s: any) => ({ id: s.id, name: s.name }));
      this.classes = classes.map((c: any) => ({ id: c.id, title: c.title }));
      this.subjects = subjects.map((s: any) => ({ id: s.id, title: s.title }));
      this.lessons = lessons.map((l: any) => ({ id: l.id, title: l.title, classId: l.classId }));
      this.tests = tests.map((t: any) => ({ id: t.id, title: t.title, classId: t.classId }));
      this.tutors = tutors;
    } finally {
      this.loading = false;
    }
  }
}
