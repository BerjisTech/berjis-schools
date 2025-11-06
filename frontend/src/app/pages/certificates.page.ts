import { Component, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { RouterModule } from '@angular/router';
import { SchoolsService } from '../services/schools.service';

@Component({
  selector: 'app-certificates',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterModule],
  templateUrl: './certificates.page.html'
})
export class CertificatesPage implements OnInit {
  templates = signal<any[]>([]);
  scope = signal<'school'|'platform'>('school');
  schoolId = signal<string>('');
  newName = signal('');
  selectedTemplateId = signal('');
  recipientUserId = signal('');
  classId = signal('');
  message = signal('');
  // automation
  ruleScope = signal<'class'|'school'>('class');
  ruleSchoolId = signal('');
  ruleClassId = signal('');
  ruleTemplateId = signal('');
  ruleMinPercent = signal<number | null>(80);
  rules = signal<any[]>([]);

  constructor(private schools: SchoolsService) {}

  async ngOnInit() {
    await this.reload();
  }

  async reload() {
    const list = await this.schools.listCertificateTemplates({ scope: this.scope(), schoolId: this.schoolId() || undefined });
    this.templates.set(list);
    const rules = await this.schools.listCertificateRules({ schoolId: this.ruleSchoolId() || undefined, classId: this.ruleClassId() || undefined });
    this.rules.set(rules);
  }

  async createTemplate() {
    if (!this.newName().trim()) return;
    await this.schools.createCertificateTemplate({ scope: this.scope(), schoolId: this.schoolId() || undefined, name: this.newName().trim(), body: { body: 'Awarded for outstanding performance' } });
    this.newName.set('');
    await this.reload();
  }

  async issue() {
    if (!this.selectedTemplateId() || !this.recipientUserId()) return;
    const res = await this.schools.issueCertificate({ templateId: this.selectedTemplateId(), recipientUserId: this.recipientUserId(), classId: this.classId() || undefined, data: {} });
    this.message.set(`Issued certificate code: ${res?.code || ''}`);
  }

  async createRule() {
    const cond: any = {};
    if (this.ruleMinPercent() !== null && this.ruleMinPercent() !== undefined) cond.minPercent = this.ruleMinPercent();
    await this.schools.createCertificateRule({ scope: this.ruleScope(), schoolId: this.ruleSchoolId() || undefined, classId: this.ruleClassId() || undefined, templateId: this.ruleTemplateId(), enabled: true, conditions: cond });
    await this.reload();
  }

  async toggleRule(r: any) {
    await this.schools.updateCertificateRule(r.id, { enabled: !r.enabled });
    await this.reload();
  }
}
