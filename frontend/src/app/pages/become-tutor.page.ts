import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { urlFor } from '../../app/util';

@Component({
  selector: 'app-become-tutor-page',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './become-tutor.page.html'
})
export class BecomeTutorPage implements OnInit {
  bio = '';
  subjects = '';
  saving = false;
  message = '';
  success = false;
  status: 'pending'|'approved'|'rejected'|null = null;
  statusLine = '';
  profile: any = { legalName: '', displayName: '', email: '', phone: '', country: '', languages: '', timezone: '' };
  verification: any = { govIdType: '', govIdUrl: '', selfieUrl: '', proofResidenceUrl: '' };
  education: any = { degreeUrl: '', certificateUrl: '', transcriptUrl: '', portfolioUrl: '', referenceUrl: '' };
  teaching: any = { levels: '', formats: '', rate: '', availability: '' };
  media: any = { introUrl: '', demoUrl: '' };
  payout: any = { method: '', name: '', tax: '' };
  consents: any = { backgroundCheck: false, codeOfConduct: false, cleanRecord: false, lessonRecording: false };

  step = 0;
  steps = ['Personal Info','Verification','Education & Credentials','Teaching Profile','Media','Consents & Agreements','Payout','Review & Submit'];

  get progressPct(): number {
    const missing = this.missingRequired();
    const total = 14; // matches server required count
    const done = Math.max(0, total - missing.length);
    return Math.round((done / total) * 100);
  }

  ngOnInit() { this.refreshStatus() }

  async refreshStatus() {
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/tutors/me`, { credentials: 'include' });
      const j = await res.json();
      const d = j?.data;
      if (d) {
        this.status = d.status;
        this.statusLine = `Current status: ${d.status}`;
        // load existing draft/application into form
        this.bio = d.bio || '';
        this.subjects = d.subjects || '';
        this.profile = d.profile || this.profile;
        this.verification = d.verification || this.verification;
        this.education = d.education || this.education;
        this.teaching = d.teaching || this.teaching;
        this.media = d.media || this.media;
        this.payout = d.payout || this.payout;
        this.consents = d.consents || this.consents;
      } else { this.status = null; this.statusLine = '' }
    } catch { this.status = null; this.statusLine = '' }
  }

  back() { if (this.step > 0) this.step--; }

  fieldsMissingForStep(step: number): string[] {
    const miss: string[] = [];
    const s = (v: any) => (typeof v === 'string' ? v.trim() : '');
    if (step === 0) {
      if (!s(this.profile.legalName)) miss.push('Full legal name');
      if (!s(this.profile.displayName)) miss.push('Display name');
      if (!s(this.profile.email)) miss.push('Email');
      if (!s(this.profile.phone)) miss.push('Phone');
      if (!s(this.profile.country)) miss.push('Country');
      if (!s(this.profile.languages)) miss.push('Languages');
      if (!s(this.profile.timezone)) miss.push('Time zone');
      if (!s(this.bio)) miss.push('Short bio');
    } else if (step === 1) {
      if (!s(this.verification.govIdType)) miss.push('Government ID type');
      if (!s(this.verification.govIdUrl)) miss.push('Government ID URL');
      if (!s(this.verification.selfieUrl)) miss.push('Selfie/Video with ID URL');
    } else if (step === 2) {
      if (!s(this.education.degreeUrl)) miss.push('Highest degree (URL)');
    } else if (step === 4) {
      if (!s(this.media.introUrl)) miss.push('Video introduction URL');
      if (!s(this.media.demoUrl)) miss.push('Demo lesson/sample URL');
    } else if (step === 5) {
      if (!this.consents.backgroundCheck) miss.push('Background check consent');
      if (!this.consents.codeOfConduct) miss.push('Code of Conduct agreement');
      if (!this.consents.cleanRecord) miss.push('Clean record declaration');
      if (!this.consents.lessonRecording) miss.push('Lesson recording consent');
    } else if (step === 6) {
      if (!s(this.payout.method)) miss.push('Payout method');
      if (!s(this.payout.name)) miss.push('Payout name');
    }
    return miss;
  }

  validForStep(step: number): boolean { return this.fieldsMissingForStep(step).length === 0 }

  maxReachableStep(): number {
    for (let i = 0; i <= 6; i++) {
      if (!this.validForStep(i)) return i;
    }
    return 7;
  }

  canGoTo(i: number): boolean { return i <= this.maxReachableStep() }

  next() {
    if (!this.validForStep(this.step)) {
      this.success = false;
      this.message = `Please complete: ${this.fieldsMissingForStep(this.step).join(', ')}`;
      return;
    }
    if (this.step < this.steps.length - 1) this.step++;
  }

  missingRequired(): string[] {
    const miss: string[] = [];
    const s = (v: any) => (typeof v === 'string' ? v.trim() : '');
    if (!s(this.profile.legalName)) miss.push('profile.legalName');
    if (!s(this.profile.displayName)) miss.push('profile.displayName');
    if (!s(this.profile.email)) miss.push('profile.email');
    if (!s(this.profile.phone)) miss.push('profile.phone');
    if (!s(this.profile.country)) miss.push('profile.country');
    if (!s(this.profile.languages)) miss.push('profile.languages');
    if (!s(this.profile.timezone)) miss.push('profile.timezone');
    if (!s(this.bio)) miss.push('bio');
    if (!s(this.verification.govIdType)) miss.push('verification.govIdType');
    if (!s(this.verification.govIdUrl)) miss.push('verification.govIdUrl');
    if (!s(this.verification.selfieUrl)) miss.push('verification.selfieUrl');
    if (!s(this.education.degreeUrl)) miss.push('education.degreeUrl');
    if (!s(this.media.introUrl)) miss.push('media.introUrl');
    if (!s(this.media.demoUrl)) miss.push('media.demoUrl');
    if (!this.consents.backgroundCheck) miss.push('consents.backgroundCheck');
    if (!this.consents.codeOfConduct) miss.push('consents.codeOfConduct');
    if (!this.consents.cleanRecord) miss.push('consents.cleanRecord');
    if (!this.consents.lessonRecording) miss.push('consents.lessonRecording');
    if (!s(this.payout.method)) miss.push('payout.method');
    if (!s(this.payout.name)) miss.push('payout.name');
    return miss;
  }

  async saveDraft() { await this.apply(true, true) }

  async apply(draft: boolean, silent = false) {
    this.saving = true; this.message = ''; this.success = false;
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/tutors/apply`, {
        method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          bio: this.bio || null,
          subjects: this.subjects || null,
          profile: this.profile,
          verification: this.verification,
          education: this.education,
          teaching: this.teaching,
          media: this.media,
          payout: this.payout,
          consents: this.consents,
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
      await this.refreshStatus();
    } catch (e: any) { this.message = e?.message || 'Failed to submit application' }
    finally { this.saving = false }
  }
}

