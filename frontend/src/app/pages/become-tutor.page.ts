import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { urlFor } from '../../app/util';

@Component({
  selector: 'app-become-tutor-page',
  standalone: true,
  imports: [CommonModule, FormsModule],
  template: `
    <div class="p-5 max-w-xl">
      <h2 class="mt-0 text-lg font-semibold">Apply to Become a Private Tutor</h2>
      <div class="text-sm text-gray-600 mb-4">You can apply even if you already teach at a school.</div>

      <div *ngIf="statusLine" class="mb-3" [ngClass]="{'text-blue-700': status==='pending', 'text-green-700': status==='approved', 'text-red-700': status==='rejected'}">{{ statusLine }}</div>

      <div class="mb-4">
        <div class="h-2 bg-gray-200 rounded-full overflow-hidden">
          <div class="h-full bg-blue-600" [style.width.%]="progressPct"></div>
        </div>
        <div class="mt-1 text-xs text-gray-600">Progress: {{ progressPct }}%</div>
      </div>

      <nav class="flex flex-wrap gap-2 mb-4">
        <button type="button" *ngFor="let s of steps; let i = index"
                (click)="canGoTo(i) && (step=i)"
                class="px-3 py-1 rounded border text-sm"
                [class.opacity-50]="!canGoTo(i)" [disabled]="!canGoTo(i)"
                [class.bg-blue-600]="i===step" [class.text-white]="i===step" [class.border-blue-600]="i===step">{{ s }}</button>
      </nav>

      <form class="space-y-6" (ngSubmit)="apply(false)">
        <section class="space-y-3" *ngIf="step===0">
          <h3 class="font-semibold">Personal Info</h3>
          <div class="text-xs text-red-700" *ngIf="fieldsMissingForStep(0).length">Required: {{ fieldsMissingForStep(0).join(', ') }}</div>
          <div class="grid gap-3 md:grid-cols-2">
            <div>
              <label class="block text-sm mb-1">Full legal name</label>
              <input [(ngModel)]="profile.legalName" name="legalName" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Display name</label>
              <input [(ngModel)]="profile.displayName" name="displayName" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Email</label>
              <input [(ngModel)]="profile.email" name="email" type="email" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Phone</label>
              <input [(ngModel)]="profile.phone" name="phone" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Country</label>
              <input [(ngModel)]="profile.country" name="country" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Languages</label>
              <input [(ngModel)]="profile.languages" name="languages" placeholder="comma-separated" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div class="md:col-span-2">
              <label class="block text-sm mb-1">Time zone</label>
              <input [(ngModel)]="profile.timezone" name="timezone" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div class="md:col-span-2">
              <label class="block text-sm mb-1">Short bio / About Me</label>
              <textarea [(ngModel)]="bio" name="bio" rows="3" class="w-full border border-gray-300 rounded-md px-3 py-2"></textarea>
            </div>
          </div>
        </section>

        <section class="space-y-3" *ngIf="step===1">
          <h3 class="font-semibold">Verification</h3>
          <div class="text-xs text-red-700" *ngIf="fieldsMissingForStep(1).length">Required: {{ fieldsMissingForStep(1).join(', ') }}</div>
          <div class="grid gap-3 md:grid-cols-2">
            <div>
              <label class="block text-sm mb-1">Government ID type</label>
              <input [(ngModel)]="verification.govIdType" name="govIdType" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Government ID (file URL)</label>
              <input [(ngModel)]="verification.govIdUrl" name="govIdUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Selfie/Video with ID (URL)</label>
              <input [(ngModel)]="verification.selfieUrl" name="selfieUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Proof of residence (URL, optional)</label>
              <input [(ngModel)]="verification.proofResidenceUrl" name="proofResidenceUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
          </div>
        </section>

        <section class="space-y-3" *ngIf="step===2">
          <h3 class="font-semibold">Education & Credentials</h3>
          <div class="text-xs text-red-700" *ngIf="fieldsMissingForStep(2).length">Required: {{ fieldsMissingForStep(2).join(', ') }}</div>
          <div class="grid gap-3 md:grid-cols-2">
            <div>
              <label class="block text-sm mb-1">Highest degree (URL)</label>
              <input [(ngModel)]="education.degreeUrl" name="degreeUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Teaching certificate (URL)</label>
              <input [(ngModel)]="education.certificateUrl" name="certificateUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Transcript/pro license (URL, optional)</label>
              <input [(ngModel)]="education.transcriptUrl" name="transcriptUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Portfolio (LinkedIn/GitHub/etc.)</label>
              <input [(ngModel)]="education.portfolioUrl" name="portfolioUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div class="md:col-span-2">
              <label class="block text-sm mb-1">Reference letter (URL, optional)</label>
              <input [(ngModel)]="education.referenceUrl" name="referenceUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
          </div>
        </section>

        <section class="space-y-3" *ngIf="step===3">
          <h3 class="font-semibold">Teaching Profile</h3>
          <div class="grid gap-3 md:grid-cols-2">
            <div>
              <label class="block text-sm mb-1">Subjects / skills</label>
              <input [(ngModel)]="subjects" name="subjects" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="e.g., Algebra, Physics" />
            </div>
            <div>
              <label class="block text-sm mb-1">Proficiency levels</label>
              <input [(ngModel)]="teaching.levels" name="levels" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="beginner, intermediate, advanced" />
            </div>
            <div>
              <label class="block text-sm mb-1">Formats</label>
              <input [(ngModel)]="teaching.formats" name="formats" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="live, recorded, Q&A, group" />
            </div>
            <div>
              <label class="block text-sm mb-1">Hourly rate / tier</label>
              <input [(ngModel)]="teaching.rate" name="rate" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div class="md:col-span-2">
              <label class="block text-sm mb-1">Availability (summary)</label>
              <input [(ngModel)]="teaching.availability" name="availability" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
          </div>
        </section>

        <section class="space-y-3" *ngIf="step===4">
          <h3 class="font-semibold">Media</h3>
          <div class="text-xs text-red-700" *ngIf="fieldsMissingForStep(4).length">Required: {{ fieldsMissingForStep(4).join(', ') }}</div>
          <div class="grid gap-3 md:grid-cols-2">
            <div>
              <label class="block text-sm mb-1">Video introduction (URL)</label>
              <input [(ngModel)]="media.introUrl" name="introUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Demo lesson / sample (URL)</label>
              <input [(ngModel)]="media.demoUrl" name="demoUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
          </div>
        </section>

        <section class="space-y-3" *ngIf="step===5">
          <h3 class="font-semibold">Consents & Agreements</h3>
          <div class="text-xs text-red-700" *ngIf="fieldsMissingForStep(5).length">Required: {{ fieldsMissingForStep(5).join(', ') }}</div>
          <div class="grid gap-3 md:grid-cols-2">
            <label class="inline-flex items-center gap-2"><input type="checkbox" [(ngModel)]="consents.backgroundCheck" name="backgroundCheck" /> Background check consent</label>
            <label class="inline-flex items-center gap-2"><input type="checkbox" [(ngModel)]="consents.codeOfConduct" name="codeOfConduct" /> Agree to Code of Conduct</label>
            <label class="inline-flex items-center gap-2"><input type="checkbox" [(ngModel)]="consents.cleanRecord" name="cleanRecord" /> Declaration of no offenses</label>
            <label class="inline-flex items-center gap-2"><input type="checkbox" [(ngModel)]="consents.lessonRecording" name="lessonRecording" /> Consent to lesson recording</label>
          </div>
        </section>

        <section class="space-y-3" *ngIf="step===6">
          <h3 class="font-semibold">Payout</h3>
          <div class="text-xs text-red-700" *ngIf="fieldsMissingForStep(6).length">Required: {{ fieldsMissingForStep(6).join(', ') }}</div>
          <div class="grid gap-3 md:grid-cols-2">
            <div>
              <label class="block text-sm mb-1">Method</label>
              <input [(ngModel)]="payout.method" name="payoutMethod" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="PayPal, Stripe, Wise, Bank" />
            </div>
            <div>
              <label class="block text-sm mb-1">Payout name (must match ID)</label>
              <input [(ngModel)]="payout.name" name="payoutName" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div class="md:col-span-2">
              <label class="block text-sm mb-1">Tax info (optional)</label>
              <input [(ngModel)]="payout.tax" name="payoutTax" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
          </div>
        </section>

        <section *ngIf="step===7" class="space-y-3">
          <h3 class="font-semibold">Review & Submit</h3>
          <p class="text-sm text-gray-700">Progress: {{ progressPct }}%. You can submit when all required items are complete. You may still submit and the server will validate.</p>
          <div class="text-xs text-red-700" *ngIf="missingRequired().length">Missing: {{ missingRequired().join(', ') }}</div>
        </section>

        <div class="flex items-center gap-3">
          <button type="button" (click)="back()" class="px-4 py-2 rounded-md border" [disabled]="step===0">Back</button>
          <button type="button" (click)="next(); saveDraft()" class="px-4 py-2 rounded-md border" [disabled]="!validForStep(step)">Next</button>
          <button type="button" (click)="saveDraft()" class="bg-gray-700 text-white px-4 py-2 rounded-md" [disabled]="saving">Save Draft</button>
          <button type="submit" class="bg-blue-600 text-white px-4 py-2 rounded-md" [disabled]="saving">{{ saving ? 'Submitting…' : 'Submit for Review' }}</button>
          <div *ngIf="message" class="text-sm" [class.text-green-700]="success" [class.text-red-700]="!success">{{ message }}</div>
        </div>
      </form>
    </div>
  `
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

  step = 0;
  steps = ['Personal Info','Verification','Education & Credentials','Teaching Profile','Media','Consents & Agreements','Payout','Review & Submit'];

  get progressPct(): number {
    const missing = this.missingRequired();
    const total = 14; // matches server required count
    const done = Math.max(0, total - missing.length);
    return Math.round((done / total) * 100);
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
