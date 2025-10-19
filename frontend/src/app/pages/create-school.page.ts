import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { urlFor } from '../../app/util';

@Component({
  selector: 'app-create-school-page',
  standalone: true,
  imports: [CommonModule, FormsModule],
  template: `
    <div class="p-5 max-w-3xl">
      <h2 class="mt-0 text-lg font-semibold">Apply to Create a School</h2>
      <div class="text-sm text-gray-600 mb-1">Multi-step application similar to tutor enrollment. Save drafts anytime.</div>
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

      <form class="space-y-6" (ngSubmit)="submit(false)">
        <!-- 0. Basic School Information -->
        <section class="space-y-3" *ngIf="step===0">
          <h3 class="font-semibold">Basic School Information</h3>
          <div class="text-xs text-red-700" *ngIf="fieldsMissingForStep(0).length">Required: {{ fieldsMissingForStep(0).join(', ') }}</div>
          <div class="grid gap-3 md:grid-cols-2">
            <div>
              <label class="block text-sm mb-1">Official school name</label>
              <input [(ngModel)]="info.name" name="info_name" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="e.g., Seed Academy" />
            </div>
            <div>
              <label class="block text-sm mb-1">Legal business name</label>
              <input [(ngModel)]="info.legalName" name="info_legalName" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Business registration number</label>
              <input [(ngModel)]="info.registrationNumber" name="info_registrationNumber" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Country of registration</label>
              <input [(ngModel)]="info.country" name="info_country" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Business type</label>
              <input [(ngModel)]="info.businessType" name="info_businessType" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="LLC, NGO, etc." />
            </div>
            <div>
              <label class="block text-sm mb-1">School website</label>
              <input [(ngModel)]="info.website" name="info_website" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="https://..." />
            </div>
            <div>
              <label class="block text-sm mb-1">Social links (comma-separated)</label>
              <input [(ngModel)]="info.social" name="info_social" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="https://twitter..., https://linkedin..." />
            </div>
            <div>
              <label class="block text-sm mb-1">Logo URL</label>
              <input [(ngModel)]="info.logoUrl" name="info_logoUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="https://..." />
            </div>
            <div class="md:col-span-2">
              <label class="block text-sm mb-1">Primary contact person</label>
              <div class="grid gap-3 md:grid-cols-3">
                <input [(ngModel)]="info.contact.name" name="info_contact_name" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="Name" />
                <input [(ngModel)]="info.contact.email" name="info_contact_email" type="email" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="Email" />
                <input [(ngModel)]="info.contact.phone" name="info_contact_phone" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="Phone" />
              </div>
            </div>
            <div class="md:col-span-2">
              <label class="block text-sm mb-1">Short description</label>
              <textarea [(ngModel)]="info.description" name="info_description" rows="3" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="Mission, specialization, subjects"></textarea>
            </div>
          </div>
        </section>

        <!-- 1. Verification Documents -->
        <section class="space-y-3" *ngIf="step===1">
          <h3 class="font-semibold">Verification Documents</h3>
          <div class="text-xs text-red-700" *ngIf="fieldsMissingForStep(1).length">Required: {{ fieldsMissingForStep(1).join(', ') }}</div>
          <div class="grid gap-3 md:grid-cols-2">
            <div>
              <label class="block text-sm mb-1">Business registration certificate (URL)</label>
              <input [(ngModel)]="verify.registrationCertUrl" name="v_registrationCertUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Tax identification/PIN (URL)</label>
              <input [(ngModel)]="verify.taxPinUrl" name="v_taxPinUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Proof of address (URL)</label>
              <input [(ngModel)]="verify.proofAddressUrl" name="v_proofAddressUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Founder/admin ID (URL)</label>
              <input [(ngModel)]="verify.founderIdUrl" name="v_founderIdUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Authorization letter (optional)</label>
              <input [(ngModel)]="verify.authorizationLetterUrl" name="v_authorizationLetterUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Accreditation (optional)</label>
              <input [(ngModel)]="verify.accreditationUrl" name="v_accreditationUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Insurance certificate (optional)</label>
              <input [(ngModel)]="verify.insuranceUrl" name="v_insuranceUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
          </div>
        </section>

        <!-- 2. Staff and Tutor Details -->
        <section class="space-y-3" *ngIf="step===2">
          <h3 class="font-semibold">Staff and Tutor Details</h3>
          <div class="text-xs text-red-700" *ngIf="fieldsMissingForStep(2).length">Required: {{ fieldsMissingForStep(2).join(', ') }}</div>
          <div>
            <label class="block text-sm mb-1">Tutors / Instructors</label>
            <div class="space-y-3">
              <div *ngFor="let t of staff.tutors; let i = index" class="border rounded-md p-3">
                <div class="grid gap-2 md:grid-cols-4">
                  <input [(ngModel)]="t.name" name="tutor_name_{{i}}" class="w-full border border-gray-300 rounded-md px-2 py-1" placeholder="Name" />
                  <input [(ngModel)]="t.email" name="tutor_email_{{i}}" type="email" class="w-full border border-gray-300 rounded-md px-2 py-1" placeholder="Email" />
                  <input [(ngModel)]="t.role" name="tutor_role_{{i}}" class="w-full border border-gray-300 rounded-md px-2 py-1" placeholder="Role" />
                  <select [(ngModel)]="t.status" name="tutor_status_{{i}}" class="w-full border border-gray-300 rounded-md px-2 py-1">
                    <option value="pending">Pending</option>
                    <option value="verified">Verified</option>
                  </select>
                </div>
                <div class="mt-2 text-right">
                  <button type="button" (click)="removeTutor(i)" class="text-xs text-red-700">Remove</button>
                </div>
              </div>
              <button type="button" (click)="addTutor()" class="px-3 py-1 border rounded">Add Tutor</button>
            </div>
          </div>
          <div>
            <label class="block text-sm mb-1">Admin/staff roles</label>
            <input [(ngModel)]="staff.adminRoles" name="staff_adminRoles" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="Who manages students, billing, etc." />
          </div>
          <div>
            <label class="block text-sm mb-1">Contract terms</label>
            <input [(ngModel)]="staff.contractTerms" name="staff_contractTerms" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="Internal staff or freelance?" />
          </div>
        </section>

        <!-- 3. Financial Information -->
        <section class="space-y-3" *ngIf="step===3">
          <h3 class="font-semibold">Financial Information</h3>
          <div class="text-xs text-red-700" *ngIf="fieldsMissingForStep(3).length">Required: {{ fieldsMissingForStep(3).join(', ') }}</div>
          <div class="grid gap-3 md:grid-cols-2">
            <div>
              <label class="block text-sm mb-1">Preferred payout method</label>
              <select [(ngModel)]="finance.payoutMethod" name="finance_payoutMethod" class="w-full border border-gray-300 rounded-md px-3 py-2">
                <option value="">Select…</option>
                <option>Stripe</option>
                <option>Wise</option>
                <option>PayPal</option>
                <option>Bank</option>
              </select>
            </div>
            <div>
              <label class="block text-sm mb-1">Currency</label>
              <input [(ngModel)]="finance.currency" name="finance_currency" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="USD, KES, NGN, ..." />
            </div>
            <div class="md:col-span-2">
              <label class="block text-sm mb-1">Business bank account details</label>
              <textarea [(ngModel)]="finance.bankDetails" name="finance_bankDetails" rows="3" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="Account name, number, bank name, SWIFT/routing"></textarea>
            </div>
            <div>
              <label class="block text-sm mb-1">Revenue model</label>
              <select [(ngModel)]="finance.revenueModel" name="finance_revenueModel" class="w-full border border-gray-300 rounded-md px-3 py-2">
                <option value="">Select…</option>
                <option>Commission</option>
                <option>Subscription</option>
                <option>Hybrid</option>
              </select>
            </div>
            <div>
              <label class="block text-sm mb-1">Tax compliance documents (URL, optional)</label>
              <input [(ngModel)]="finance.taxDocsUrl" name="finance_taxDocsUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Bank statement (URL, optional)</label>
              <input [(ngModel)]="finance.bankStatementUrl" name="finance_bankStatementUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
          </div>
        </section>

        <!-- 4. Educational Scope and Curriculum -->
        <section class="space-y-3" *ngIf="step===4">
          <h3 class="font-semibold">Educational Scope and Curriculum</h3>
          <div class="text-xs text-red-700" *ngIf="fieldsMissingForStep(4).length">Required: {{ fieldsMissingForStep(4).join(', ') }}</div>
          <div class="grid gap-3 md:grid-cols-2">
            <div>
              <label class="block text-sm mb-1">Subjects / categories offered</label>
              <input [(ngModel)]="curriculum.subjects" name="curr_subjects" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="comma-separated" />
            </div>
            <div>
              <label class="block text-sm mb-1">Target learners</label>
              <input [(ngModel)]="curriculum.targets" name="curr_targets" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="age groups, proficiency levels" />
            </div>
            <div>
              <label class="block text-sm mb-1">Teaching format</label>
              <input [(ngModel)]="curriculum.format" name="curr_format" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="live, recorded, hybrid, mentorship" />
            </div>
            <div>
              <label class="block text-sm mb-1">Languages of instruction</label>
              <input [(ngModel)]="curriculum.languages" name="curr_languages" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div class="md:col-span-2">
              <label class="block text-sm mb-1">Curriculum outline or course catalog (URL, optional)</label>
              <input [(ngModel)]="curriculum.outlineUrl" name="curr_outlineUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Sample course material / syllabus (URL, optional)</label>
              <input [(ngModel)]="curriculum.sampleUrl" name="curr_sampleUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Demo/promotional video (URL)</label>
              <input [(ngModel)]="curriculum.demoUrl" name="curr_demoUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
          </div>
        </section>

        <!-- 5. Policies and Legal Agreements -->
        <section class="space-y-3" *ngIf="step===5">
          <h3 class="font-semibold">Policies and Legal Agreements</h3>
          <div class="text-xs text-red-700" *ngIf="fieldsMissingForStep(5).length">Required: {{ fieldsMissingForStep(5).join(', ') }}</div>
          <div class="grid gap-2 md:grid-cols-2">
            <label class="flex items-center gap-2 text-sm"><input type="checkbox" [(ngModel)]="agreements.partnership" name="agr_partnership" /> School Partnership Agreement</label>
            <label class="flex items-center gap-2 text-sm"><input type="checkbox" [(ngModel)]="agreements.privacy" name="agr_privacy" /> Data Protection & Privacy Policy</label>
            <label class="flex items-center gap-2 text-sm"><input type="checkbox" [(ngModel)]="agreements.revenueSplit" name="agr_revenueSplit" /> Revenue Split / Commission Terms</label>
            <label class="flex items-center gap-2 text-sm"><input type="checkbox" [(ngModel)]="agreements.codeOfConduct" name="agr_codeOfConduct" /> Code of Conduct for Tutors and Admins</label>
            <label class="flex items-center gap-2 text-sm"><input type="checkbox" [(ngModel)]="agreements.quality" name="agr_quality" /> Quality Assurance Policy</label>
            <label class="flex items-center gap-2 text-sm"><input type="checkbox" [(ngModel)]="agreements.antiFraud" name="agr_antiFraud" /> Anti-Plagiarism & Anti-Fraud Declaration</label>
            <label class="flex items-center gap-2 text-sm"><input type="checkbox" [(ngModel)]="agreements.refund" name="agr_refund" /> Student Refund Policy</label>
          </div>
        </section>

        <!-- 6. Optional Extras -->
        <section class="space-y-3" *ngIf="step===6">
          <h3 class="font-semibold">Optional Extras</h3>
          <div class="grid gap-3 md:grid-cols-2">
            <div>
              <label class="block text-sm mb-1">Marketing media URLs (comma-separated)</label>
              <input [(ngModel)]="extras.mediaUrls" name="extras_mediaUrls" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Testimonials</label>
              <input [(ngModel)]="extras.testimonials" name="extras_testimonials" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="short quotes or links" />
            </div>
            <div>
              <label class="block text-sm mb-1">Brand banner (URL)</label>
              <input [(ngModel)]="extras.bannerUrl" name="extras_bannerUrl" class="w-full border border-gray-300 rounded-md px-3 py-2" />
            </div>
            <div>
              <label class="block text-sm mb-1">Preferred subdomain</label>
              <input [(ngModel)]="extras.subdomain" name="extras_subdomain" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="e.g., seed" />
            </div>
            <div>
              <label class="block text-sm mb-1">Theme color</label>
              <input [(ngModel)]="extras.themeColor" name="extras_themeColor" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="#0055cc" />
            </div>
            <div>
              <label class="block text-sm mb-1">Integrations (LMS/CRM)</label>
              <input [(ngModel)]="extras.integrations" name="extras_integrations" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="Moodle, HubSpot" />
            </div>
            <div>
              <label class="block text-sm mb-1">Partnership type</label>
              <select [(ngModel)]="extras.partnershipType" name="extras_partnershipType" class="w-full border border-gray-300 rounded-md px-3 py-2">
                <option value="">Select…</option>
                <option>Independent</option>
                <option>Corporate Training</option>
                <option>Community School</option>
              </select>
            </div>
          </div>
        </section>

        <!-- 7. Review & Submit -->
        <section class="space-y-3" *ngIf="step===7">
          <h3 class="font-semibold">Review & Submit</h3>
          <div class="text-sm text-gray-600">Complete all required steps to enable submission.</div>
          <div class="text-xs text-red-700" *ngIf="missingRequired().length">Missing: {{ missingRequired().join(', ') }}</div>
          <div>
            <pre class="text-xs bg-gray-50 border rounded p-3 overflow-auto max-h-64">{{ previewJson }}</pre>
          </div>
        </section>

        <div class="flex items-center gap-3 pt-2">
          <button type="button" (click)="back()" class="px-4 py-2 rounded-md border" [disabled]="step===0">Back</button>
          <button type="button" (click)="next(); saveDraft()" class="px-4 py-2 rounded-md border" [disabled]="!validForStep(step)">Next</button>
          <button type="button" (click)="saveDraft()" class="px-4 py-2 rounded-md border">Save Draft</button>
          <button type="submit" class="bg-blue-600 text-white px-4 py-2 rounded-md" [disabled]="saving || step!==7 || missingRequired().length>0">{{ saving ? 'Submitting…' : 'Submit Application' }}</button>
          <div *ngIf="message" class="text-sm" [class.text-green-700]="success" [class.text-red-700]="!success">{{ message }}</div>
        </div>
      </form>
    </div>
  `
})
export class CreateSchoolPage implements OnInit {
  // Form state
  info: any = { name: '', legalName: '', registrationNumber: '', country: '', businessType: '', website: '', social: '', logoUrl: '', description: '', contact: { name: '', email: '', phone: '' } };
  verify: any = { registrationCertUrl: '', taxPinUrl: '', proofAddressUrl: '', founderIdUrl: '', authorizationLetterUrl: '', accreditationUrl: '', insuranceUrl: '' };
  staff: any = { tutors: [] as Array<{ name: string; email: string; role: string; status: 'pending'|'verified' }>, adminRoles: '', contractTerms: '' };
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

  addTutor() { this.staff.tutors.push({ name: '', email: '', role: '', status: 'pending' }) }
  removeTutor(i: number) { this.staff.tutors.splice(i, 1) }

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
      if (!s(this.verify.registrationCertUrl)) miss.push('Registration certificate');
      if (!s(this.verify.taxPinUrl)) miss.push('Tax identification/PIN');
      if (!s(this.verify.proofAddressUrl)) miss.push('Proof of address');
      if (!s(this.verify.founderIdUrl)) miss.push('Founder/admin ID');
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
