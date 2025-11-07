import { Component, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { TermPipe } from '../pipes/term.pipe';
import { SchoolsService } from '../services/schools.service';

@Component({
  selector: 'app-sections-page',
  standalone: true,
  imports: [CommonModule, FormsModule, TermPipe],
  templateUrl: './sections.page.html'
})
export class SectionsPage implements OnInit {
  schools: Array<{ id: string; name: string }> = [];
  selectedSchool = signal<string>('');
  sections = signal<any[]>([]);
  selectedSection = signal<string>('');
  members = signal<Array<{ studentUserId: string }>>([]);
  newSection = { name: '', gradeLevel: '' };
  addMemberId = '';
  studentSearch = '';
  studentChoices = signal<Array<{ userId: string; label: string }>>([]);
  busy = false;

  constructor(private svc: SchoolsService) {}

  async ngOnInit() {
    this.schools = await this.svc.listAdminSchools();
    if (this.schools.length) {
      this.selectedSchool.set(this.schools[0].id);
      await this.loadSections();
    }
  }

  async loadSections() {
    const sid = this.selectedSchool();
    if (!sid) return;
    this.sections.set(await this.svc.listSections(sid));
    this.selectedSection.set('');
    this.members.set([]);
    await this.refreshStudentChoices();
  }

  async createSection() {
    const sid = this.selectedSchool();
    if (!sid || !this.newSection.name.trim()) return;
    this.busy = true;
    try {
      await this.svc.createSection(sid, { name: this.newSection.name.trim(), gradeLevel: this.newSection.gradeLevel || undefined });
      this.newSection = { name: '', gradeLevel: '' };
      await this.loadSections();
    } finally { this.busy = false; }
  }

  async openSection(id: string) {
    this.selectedSection.set(id);
    this.members.set(await this.svc.listSectionMembers(id));
  }

  async addMember() {
    const sec = this.selectedSection();
    const uid = this.addMemberId.trim();
    if (!sec || !uid) return;
    this.busy = true;
    try {
      await this.svc.addSectionMember(sec, uid);
      this.addMemberId = '';
      await this.openSection(sec);
    } finally { this.busy = false; }
  }

  async removeMember(uid: string) {
    const sec = this.selectedSection();
    if (!sec) return;
    this.busy = true;
    try {
      await this.svc.removeSectionMember(sec, uid);
      await this.openSection(sec);
    } finally { this.busy = false; }
  }

  async refreshStudentChoices() {
    const sid = this.selectedSchool();
    if (!sid) return;
    const list = await this.svc.listSchoolStudents(sid);
    this.studentChoices.set(list.map(s => ({ userId: s.userId, label: (s.displayName || s.userId) + (s.admissionNo ? ` • ${s.admissionNo}` : '') })));
  }

  onTypeahead(q: string) {
    this.studentSearch = q;
  }
}
