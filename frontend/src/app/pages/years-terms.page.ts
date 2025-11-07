import { Component, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { SchoolsService } from '../services/schools.service';

@Component({
  selector: 'app-years-terms-page',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './years-terms.page.html'
})
export class YearsTermsPage implements OnInit {
  schools: Array<{ id: string; name: string }> = [];
  selectedSchool = signal<string>('');
  years = signal<any[]>([]);
  terms = signal<any[]>([]);
  // new year
  yn = { name: '', startDate: '', endDate: '', structure: 'custom' as 'semester'|'trimester'|'quarter'|'custom' };
  // new term
  tn = { name: '', startDate: '', endDate: '' };
  selectedYear = signal<string>('');
  busy = false;
  editYearId = signal<string>('');
  editYear = { name: '', status: '', structure: 'custom', startDate: '', endDate: '' } as { name: string; status: string; structure: 'semester'|'trimester'|'quarter'|'custom'; startDate: string; endDate: string };
  editTermId = signal<string>('');
  editTerm = { name: '', startDate: '', endDate: '' };

  constructor(private svc: SchoolsService) {}

  async ngOnInit() {
    this.schools = await this.svc.listAdminSchools();
    if (this.schools.length) {
      this.selectedSchool.set(this.schools[0].id);
      await this.loadYears();
    }
  }

  async loadYears() {
    const sid = this.selectedSchool();
    if (!sid) return;
    this.years.set(await this.svc.listYears(sid));
    this.terms.set([]);
    this.selectedYear.set('');
  }

  async createYear() {
    const sid = this.selectedSchool();
    if (!sid || !this.yn.name.trim()) return;
    this.busy = true;
    try {
      await this.svc.createYear(sid, { name: this.yn.name.trim(), startDate: this.yn.startDate || undefined, endDate: this.yn.endDate || undefined, structure: this.yn.structure });
      this.yn = { name: '', startDate: '', endDate: '', structure: 'custom' };
      await this.loadYears();
    } finally { this.busy = false; }
  }

  async openYear(id: string) {
    this.selectedYear.set(id);
    this.terms.set(await this.svc.listTerms(id));
    this.editYearId.set('');
    this.editTermId.set('');
  }

  async createTerm() {
    const yid = this.selectedYear();
    if (!yid || !this.tn.name.trim()) return;
    this.busy = true;
    try {
      await this.svc.createTerm(yid, { name: this.tn.name.trim(), startDate: this.tn.startDate || undefined, endDate: this.tn.endDate || undefined });
      this.tn = { name: '', startDate: '', endDate: '' };
      await this.openYear(yid);
    } finally { this.busy = false; }
  }

  startEditYear(y: any) {
    this.editYearId.set(y.id);
    this.editYear = { name: y.name, status: y.status || '', structure: (y.structure || 'custom'), startDate: y.startDate || '', endDate: y.endDate || '' };
  }
  async saveYear() {
    const id = this.editYearId(); if (!id) return;
    this.busy = true; try {
      await this.svc.updateYear(id, { name: this.editYear.name, status: this.editYear.status || undefined, structure: this.editYear.structure, startDate: this.editYear.startDate || undefined, endDate: this.editYear.endDate || undefined });
      await this.loadYears();
      this.editYearId.set('');
    } finally { this.busy = false; }
  }
  cancelYearEdit() { this.editYearId.set(''); }

  async generate(struct: 'semester'|'trimester'|'quarter') {
    const y = this.selectedYear(); if (!y) return;
    this.busy = true; try {
      await this.svc.generateTerms(y, struct);
      await this.openYear(y);
    } finally { this.busy = false; }
  }

  startEditTerm(t: any) {
    this.editTermId.set(t.id);
    this.editTerm = { name: t.name, startDate: t.startDate || '', endDate: t.endDate || '' };
  }
  async saveTerm() {
    const id = this.editTermId(); if (!id) return;
    this.busy = true; try {
      await this.svc.updateTerm(id, { name: this.editTerm.name, startDate: this.editTerm.startDate || undefined, endDate: this.editTerm.endDate || undefined });
      const y = this.selectedYear(); if (y) await this.openYear(y);
      this.editTermId.set('');
    } finally { this.busy = false; }
  }
  cancelTermEdit() { this.editTermId.set(''); }
}
