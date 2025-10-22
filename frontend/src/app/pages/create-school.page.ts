import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { urlFor } from '../../app/util';

@Component({
  selector: 'app-create-school-page',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './create-school.page.html'
})
export class CreateSchoolPage implements OnInit {
  // Form state
  info: any = { name: '', legalName: '', registrationNumber: '', country: '', businessType: '', website: '', social: '', logoUrl: '', description: '', contact: { name: '', email: '', phone: '' } };
  verify: any = { registrationCertUrl: '', taxPinUrl: '', proofAddressUrl: '', founderIdUrl: '', authorizationLetterUrl: '', accreditationUrl: '', insuranceUrl: '' };
  // hold selected PDF Files (uploaded later)
  verifyFiles: Record<string, File|null> = { registrationCertUrl: null, taxPinUrl: null, proofAddressUrl: null, founderIdUrl: null } as any;
  staff: any = { tutors: [] as Array<{ userId: string; role: string; status: 'pending'|'verified' }>, adminRoles: '', contractTerms: '' };
  tutorSearchQuery = '';
  tutorResults: Array<{ userId: string; displayName?: string|null }> = [];
  finance: any = { payoutMethod: '', currency: '', bankDetails: '', revenueModel: '', taxDocsUrl: '', bankStatementUrl: '' };
  curriculum: any = { subjects: '', targets: '', format: '', languages: '', outlineUrl: '', sampleUrl: '', demoUrl: '' };
  agreements: any = { partnership: false, privacy: false, revenueSplit: false, codeOfConduct: false, quality: false, antiFraud: false, refund: false };
  extras: any = { mediaUrls: '', testimonials: '', bannerUrl: '', subdomain: '', themeColor: '', integrations: '', partnershipType: '' };

  // UI state
  step = 0;
  steps = ['School Info','Verification','Staff & Tutors','Financial','Curriculum','Agreements','Extras','Review & Submit'];
  saving = false;
  message = '';
  success = false;
  status: 'draft'|'pending'|'approved'|'rejected'|null = null;
  statusLine = '';

  ngOnInit() { this.loadDraft(); this.refreshStatus() }

  async refreshStatus() {
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/schools/applications/me`, { credentials: 'include' });
      const j = await res.json();
      const d = j?.data;
      if (d) {
        this.status = d.status;
        this.statusLine = `Current status: ${d.status}`;
        this.info = d.info || this.info;
        this.verify = d.verify || this.verify;
        this.staff = d.staff || this.staff;
        this.finance = d.finance || this.finance;
        this.curriculum = d.curriculum || this.curriculum;
        this.agreements = d.agreements || this.agreements;
        this.extras = d.extras || this.extras;
      } else { this.status = null; this.statusLine = '' }
    } catch { this.status = null; this.statusLine = '' }
  }

  get progressPct(): number {
    const missing = this.missingRequired();
    const total = 17; // count of required items below
    const done = Math.max(0, total - missing.length);
    return Math.round((done / total) * 100);
  }

  get previewJson(): string {
    const data = this.collectPayload();
    try { return JSON.stringify(data, null, 2) } catch { return '' }
  }

  back() { if (this.step > 0) this.step--; }
  next() { if (this.validForStep(this.step) && this.step < this.steps.length - 1) this.step++; }

  addTutor() { /* deprecated in favor of search add */ }
  removeTutor(i: number) { this.staff.tutors.splice(i, 1) }
  async searchUsers() {
    const q = this.tutorSearchQuery.trim(); if (!q) { this.tutorResults = []; return }
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/search?type=user&q=${encodeURIComponent(q)}`, { credentials: 'include' });
      const j = await res.json();
      this.tutorResults = (j?.data || []).map((u:any) => ({ userId: u.userId, displayName: u.displayName }));
    } catch { this.tutorResults = [] }
  }
  addTutorByUserId(uid: string) {
    if (!uid) return;
    if (!this.staff.tutors.find((t:any)=>t.userId===uid)) this.staff.tutors.push({ userId: uid, role: 'tutor', status: 'pending' });
    this.tutorSearchQuery = ''; this.tutorResults = [];
  }

  onVerifyFileChange(key: string, ev: Event) {
    const input = ev.target as HTMLInputElement;
    const file = (input.files && input.files[0]) || null;
    this.verifyFiles[key] = file;
    if (file) this.verify[key] = file.name;
  }

  fieldsMissingForStep(step: number): string[] {
    const miss: string[] = [];
    const s = (v: any) => (typeof v === 'string' ? v.trim() : '');
    if (step === 0) {
      if (!s(this.info.name)) miss.push('Official school name');
      if (!s(this.info.country)) miss.push('Country of registration');
      if (!s(this.info.businessType)) miss.push('Business type');
      if (!s(this.info.contact?.name)) miss.push('Primary contact name');
      if (!s(this.info.contact?.email)) miss.push('Primary contact email');
      if (!s(this.info.contact?.phone)) miss.push('Primary contact phone');
      if (!s(this.info.description)) miss.push('Short description');
    } else if (step === 1) {
      if (!this.verifyFiles.registrationCertUrl) miss.push('Registration certificate (PDF)');
      if (!this.verifyFiles.taxPinUrl) miss.push('Tax identification/PIN (PDF)');
      if (!this.verifyFiles.proofAddressUrl) miss.push('Proof of address (PDF)');
      if (!this.verifyFiles.founderIdUrl) miss.push('Founder/admin ID (PDF)');
    } else if (step === 2) {
      if (!this.staff.tutors.length) miss.push('At least one tutor/instructor');
    } else if (step === 3) {
      if (!s(this.finance.payoutMethod)) miss.push('Preferred payout method');
      if (!s(this.finance.currency)) miss.push('Currency');
      if (!s(this.finance.bankDetails)) miss.push('Business bank account details');
      if (!s(this.finance.revenueModel)) miss.push('Revenue model');
    } else if (step === 4) {
      if (!s(this.curriculum.subjects)) miss.push('Subjects/categories');
      if (!s(this.curriculum.targets)) miss.push('Target learners');
      if (!s(this.curriculum.format)) miss.push('Teaching format');
      if (!s(this.curriculum.languages)) miss.push('Languages of instruction');
      if (!s(this.curriculum.demoUrl)) miss.push('Demo/promotional video');
    } else if (step === 5) {
      if (!this.agreements.partnership) miss.push('Partnership Agreement');
      if (!this.agreements.privacy) miss.push('Data Protection & Privacy');
      if (!this.agreements.revenueSplit) miss.push('Revenue Split / Commission Terms');
      if (!this.agreements.codeOfConduct) miss.push('Code of Conduct');
      if (!this.agreements.quality) miss.push('Quality Assurance Policy');
      if (!this.agreements.antiFraud) miss.push('Anti-Plagiarism & Anti-Fraud');
      if (!this.agreements.refund) miss.push('Student Refund Policy');
    }
    return miss;
  }

  validForStep(step: number): boolean { return this.fieldsMissingForStep(step).length === 0 }
  maxReachableStep(): number { for (let i = 0; i <= 5; i++) { if (!this.validForStep(i)) return i } return 7 }
  canGoTo(i: number): boolean { return i <= this.maxReachableStep() }

  missingRequired(): string[] {
    const miss: string[] = [];
    const s = (v: any) => (typeof v === 'string' ? v.trim() : '');
    if (!s(this.info.name)) miss.push('info.name');
    if (!s(this.info.country)) miss.push('info.country');
    if (!s(this.info.businessType)) miss.push('info.businessType');
    if (!s(this.info.contact?.name)) miss.push('info.contact.name');
    if (!s(this.info.contact?.email)) miss.push('info.contact.email');
    if (!s(this.info.contact?.phone)) miss.push('info.contact.phone');
    if (!s(this.info.description)) miss.push('info.description');
    if (!s(this.verify.registrationCertUrl)) miss.push('verify.registrationCertUrl');
    if (!s(this.verify.taxPinUrl)) miss.push('verify.taxPinUrl');
    if (!s(this.verify.proofAddressUrl)) miss.push('verify.proofAddressUrl');
    if (!s(this.verify.founderIdUrl)) miss.push('verify.founderIdUrl');
    if (!(this.staff.tutors.length > 0)) miss.push('staff.tutors');
    if (!s(this.finance.payoutMethod)) miss.push('finance.payoutMethod');
    if (!s(this.finance.currency)) miss.push('finance.currency');
    if (!s(this.finance.bankDetails)) miss.push('finance.bankDetails');
    if (!s(this.finance.revenueModel)) miss.push('finance.revenueModel');
    if (!s(this.curriculum.subjects)) miss.push('curriculum.subjects');
    if (!s(this.curriculum.targets)) miss.push('curriculum.targets');
    if (!s(this.curriculum.format)) miss.push('curriculum.format');
    if (!s(this.curriculum.languages)) miss.push('curriculum.languages');
    if (!s(this.curriculum.demoUrl)) miss.push('curriculum.demoUrl');
    if (!this.agreements.partnership) miss.push('agreements.partnership');
    if (!this.agreements.privacy) miss.push('agreements.privacy');
    if (!this.agreements.revenueSplit) miss.push('agreements.revenueSplit');
    if (!this.agreements.codeOfConduct) miss.push('agreements.codeOfConduct');
    if (!this.agreements.quality) miss.push('agreements.quality');
    if (!this.agreements.antiFraud) miss.push('agreements.antiFraud');
    if (!this.agreements.refund) miss.push('agreements.refund');
    return miss;
  }

  async saveDraft() { await this.apply(true, true) }

  loadDraft() {
    try {
      const raw = localStorage.getItem('schoolOnboardingDraft');
      if (!raw) return;
      const d = JSON.parse(raw);
      this.info = d.info || this.info;
      this.verify = d.verify || this.verify;
      this.staff = d.staff || this.staff;
      this.finance = d.finance || this.finance;
      this.curriculum = d.curriculum || this.curriculum;
      this.agreements = d.agreements || this.agreements;
      this.extras = d.extras || this.extras;
    } catch {}
  }

  collectPayload() {
    return {
      info: this.info,
      verify: this.verify,
      staff: this.staff,
      finance: this.finance,
      curriculum: this.curriculum,
      agreements: this.agreements,
      extras: this.extras,
      progress: this.progressPct
    };
  }

  async submit(draft: boolean) { await this.apply(draft, false) }

  async apply(draft: boolean, silent = false) {
    this.saving = true; this.message = ''; this.success = false;
    try {
      if (!draft) {
        const missing = this.missingRequired();
        if (missing.length) throw new Error(`Missing required fields: ${missing.join(', ')}`);
      }
      const res = await fetch(`${urlFor('schools-api')}/v1/schools/apply`, {
        method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          info: this.info,
          verify: this.verify,
          staff: this.staff,
          finance: this.finance,
          curriculum: this.curriculum,
          agreements: this.agreements,
          extras: this.extras,
          draft,
          progress: this.progressPct
        })
      });
      const j = await res.json();
      if (!j?.success) {
        const m = j?.missing?.length ? `: missing ${j.missing.join(', ')}` : '';
        throw new Error((j?.message || 'Failed') + m);
      }
      this.success = !silent; this.message = !silent ? (draft ? 'Draft saved.' : 'Application submitted. Status set to pending.') : '';
      localStorage.setItem('schoolOnboardingDraft', JSON.stringify(this.collectPayload()));
      await this.refreshStatus();
    } catch (e: any) { this.message = e?.message || 'Failed to submit application' }
    finally { this.saving = false }
  }
}
