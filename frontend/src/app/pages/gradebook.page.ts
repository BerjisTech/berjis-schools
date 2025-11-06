import { Component, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { RouterModule } from '@angular/router';
import { SchoolsService } from '../services/schools.service';
import { JSONRecord } from '../types/json';

@Component({
  selector: 'app-gradebook',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterModule],
  templateUrl: './gradebook.page.html'
})
export class GradebookPage implements OnInit {
  classes = signal<Array<{ id: string; title: string }>>([]);
  selectedClassId = signal<string>('');
  tests = signal<Array<{ id: string; title: string; max: number }>>([]);
  students = signal<JSONRecord[]>([]);
  loading = signal(false);

  constructor(private schools: SchoolsService) {}

  async ngOnInit() {
    // Load tutor's classes
    const mine = await this.schools.listMyCourses();
    this.classes.set(mine.map(m => ({ id: m.id, title: m.title })));
    if (mine.length) {
      this.selectedClassId.set(mine[0].id);
      await this.load();
    }
  }

  async load() {
    const cid = this.selectedClassId();
    if (!cid) return;
    this.loading.set(true);
    try {
      const data = await this.schools.getClassGradebook(cid);
      this.tests.set(data.tests || []);
      this.students.set(data.students || []);
    } finally {
      this.loading.set(false);
    }
  }

  async exportCsv() {
    const cid = this.selectedClassId();
    if (!cid) return;
    const blob = await this.schools.downloadClassGradebookCSV(cid);
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'gradebook.csv';
    a.click();
    URL.revokeObjectURL(url);
  }
}
