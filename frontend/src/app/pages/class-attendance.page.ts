import { Component, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute } from '@angular/router';
import { SchoolsService } from '../services/schools.service';

type Status = 'present'|'absent'|'late'|'excused';

@Component({
  selector: 'app-class-attendance-page',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './class-attendance.page.html'
})
export class ClassAttendancePage implements OnInit {
  classId = '';
  day = '';
  roster = signal<Array<{ userId: string; name: string }>>([]);
  current = signal<Record<string, Status>>({});
  busy = false;

  constructor(private route: ActivatedRoute, private svc: SchoolsService) {}

  async ngOnInit() {
    this.classId = this.route.snapshot.paramMap.get('id') || '';
    this.day = new Date().toISOString().slice(0,10);
    await this.load();
  }

  async load() {
    const [roster, att] = await Promise.all([
      this.svc.getClassRoster(this.classId),
      this.svc.getAttendance(this.classId, this.day)
    ]);
    this.roster.set(roster);
    const map: Record<string, Status> = {} as any;
    for (const a of att) { map[a.studentUserId] = a.status as Status; }
    this.current.set(map);
  }

  setStatus(uid: string, st: Status) {
    const c = { ...this.current() } as Record<string, Status>;
    c[uid] = st;
    this.current.set(c);
  }

  async save() {
    this.busy = true;
    try {
      const entries = this.roster().map(r => ({ studentUserId: r.userId, status: (this.current()[r.userId] || 'present') as Status }));
      await this.svc.markAttendance(this.classId, this.day, entries as any);
      await this.load();
    } finally { this.busy = false; }
  }
}

