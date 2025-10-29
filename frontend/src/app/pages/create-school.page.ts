import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import { urlFor } from '../../app/util';

type VerifyDocKey = 'registrationCertUrl' | 'taxPinUrl' | 'proofAddressUrl' | 'founderIdUrl';
type FinanceDocKey = 'taxDocsUrl' | 'bankStatementUrl';

const INFO_TEMPLATE = {
  name: '',
  legalName: '',
  registrationNumber: '',
  country: '',
  businessType: '',
  website: '',
  social: '',
  logoUrl: '',
  description: '',
  contact: { name: '', email: '', phone: '' }
};

const VERIFY_TEMPLATE = {
  registrationCertUrl: '',
  taxPinUrl: '',
  proofAddressUrl: '',
  founderIdUrl: '',
  authorizationLetterUrl: '',
  accreditationUrl: '',
  insuranceUrl: ''
};

const STAFF_TEMPLATE = {
  tutors: [] as Array<{ userId: string; role: string; status: 'pending' | 'verified' }>,
  adminRoles: '',
  contractTerms: ''
};

const FINANCE_TEMPLATE = {
  payoutMethod: '',
  currency: '',
  bankDetails: '',
  revenueModel: '',
  taxDocsUrl: [] as string[],
  bankStatementUrl: [] as string[]
};

const CURRICULUM_TEMPLATE = {
  subjects: '',
  targets: '',
  format: '',
  languages: '',
  outlineUrl: '',
  sampleUrl: '',
  demoUrl: ''
};

const AGREEMENTS_TEMPLATE = {
  partnership: false,
  privacy: false,
  revenueSplit: false,
  codeOfConduct: false,
  quality: false,
  antiFraud: false,
  refund: false
};

const EXTRAS_TEMPLATE = {
  mediaUrls: '',
  testimonials: '',
  bannerUrl: '',
  subdomain: '',
  themeColor: '',
  integrations: '',
  partnershipType: ''
};

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value));
}

@Component({
  selector: 'app-create-school-page',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './create-school.page.html'
})
export class CreateSchoolPage implements OnInit {
  // Form state
  info: any = clone(INFO_TEMPLATE);
  verify: any = clone(VERIFY_TEMPLATE);
  // hold selected PDF Files (uploaded later)
  verifyFiles: Record<VerifyDocKey, File | null> = {
    registrationCertUrl: null,
    taxPinUrl: null,
    proofAddressUrl: null,
    founderIdUrl: null
  };
  staff: any = clone(STAFF_TEMPLATE);
  tutorSearchQuery = '';
  tutorResults: Array<{ userId: string; displayName?: string|null }> = [];
  finance: any = clone(FINANCE_TEMPLATE);
  financeFiles: Record<FinanceDocKey, File[] | null> = {
    taxDocsUrl: null,
    bankStatementUrl: null
  };
  curriculum: any = clone(CURRICULUM_TEMPLATE);
  agreements: any = clone(AGREEMENTS_TEMPLATE);
  extras: any = clone(EXTRAS_TEMPLATE);

  // UI state
  step = 0;
  readonly stepDefinitions = [
    { slug: 'info', label: 'School Info' },
    { slug: 'verification', label: 'Verification' },
    { slug: 'staff', label: 'Staff & Tutors' },
    { slug: 'financial', label: 'Financial' },
    { slug: 'curriculum', label: 'Curriculum' },
    { slug: 'agreements', label: 'Agreements' },
    { slug: 'extras', label: 'Extras' },
    { slug: 'review', label: 'Review & Submit' }
  ];
  private currentSlug = this.stepDefinitions[0].slug;
  saving = false;
  message = '';
  uploadError = '';
  uploading = false;
  success = false;
  status: 'draft'|'pending'|'approved'|'rejected'|null = null;
  statusLine = '';
  // expose urlFor in template
  urlFor = urlFor;

  constructor(private route: ActivatedRoute, private router: Router) {}

  ngOnInit() {
    this.route.paramMap.subscribe(params => {
      const slug = params.get('section') ?? this.stepDefinitions[0].slug;
      const idx = this.stepDefinitions.findIndex(def => def.slug === slug);
      if (idx === -1) {
        this.router.navigate(['/schools/create', this.stepDefinitions[0].slug], { replaceUrl: true });
        return;
      }
      this.currentSlug = slug;
      const max = this.maxReachableStep();
      if (idx > max) {
        this.goToStep(max, true);
        return;
      }
      this.step = idx;
    });
    this.loadDraft();
    this.refreshStatus();
  }

  async refreshStatus() {
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/schools/applications/me`, { credentials: 'include' });
      const j = await res.json();
      const d = j?.data;
      if (d) {
        this.status = d.status;
        this.statusLine = `Current status: ${d.status}`;
        this.info = this.parseSection(d.info, INFO_TEMPLATE);
        this.verify = this.parseSection(d.verify, VERIFY_TEMPLATE);
        this.staff = this.parseSection(d.staff, STAFF_TEMPLATE);
        this.finance = this.parseSection(d.finance, FINANCE_TEMPLATE);
        this.curriculum = this.parseSection(d.curriculum, CURRICULUM_TEMPLATE);
        this.agreements = this.parseSection(d.agreements, AGREEMENTS_TEMPLATE);
        this.extras = this.parseSection(d.extras, EXTRAS_TEMPLATE);
        this.resetUploadBuffers();
        const reachable = this.maxReachableStep();
        if (this.step > reachable) {
          this.goToStep(reachable, true);
        }
      } else {
        this.status = null;
        this.statusLine = '';
      }
    } catch {
      this.status = null;
      this.statusLine = '';
    }
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

  back() { if (this.step > 0) this.goToStep(this.step - 1); }
  next() {
    if (this.validForStep(this.step) && this.step < this.stepDefinitions.length - 1) {
      this.goToStep(this.step + 1);
    }
  }

  goToStep(index: number, replaceUrl = false) {
    if (index < 0 || index >= this.stepDefinitions.length) return;
    if (!this.canGoTo(index)) return;
    const targetSlug = this.stepDefinitions[index].slug;
    this.step = index;
    if (this.currentSlug !== targetSlug) {
      this.currentSlug = targetSlug;
      this.router.navigate(['/schools/create', targetSlug], { replaceUrl });
    }
  }

  onStepClick(index: number) {
    if (this.canGoTo(index)) {
      this.goToStep(index);
    }
  }

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

  onVerifyFileChange(key: VerifyDocKey, ev: Event) {
    this.ensureFormShape();
    const input = ev.target as HTMLInputElement;
    const file = (input.files && input.files[0]) || null;
    this.uploadError = '';
    this.verifyFiles[key] = file;
    if (file) this.verify[key] = file.name;
  }

  onFinanceFileChange(key: FinanceDocKey, ev: Event) {
    this.ensureFormShape();
    const input = ev.target as HTMLInputElement;
    const files = (input.files && Array.from(input.files)) || null;
    this.uploadError = '';
    this.financeFiles[key] = files;
    if (files && files.length) this.finance[key] = files.map(f => f.name);
  }

  fieldsMissingForStep(step: number): string[] {
    this.ensureFormShape();
    const miss: string[] = [];
    const s = (v: any) => (typeof v === 'string' ? v.trim() : '');
    const hasDoc = (key: VerifyDocKey) => {
      const file = this.verifyFiles[key];
      if (file && typeof file.name === 'string' && file.name.trim()) return true;
      const value = this.verify?.[key];
      return typeof value === 'string' && value.trim().length > 0;
    };
    if (step === 0) {
      if (!s(this.info.name)) miss.push('Official school name');
      if (!s(this.info.country)) miss.push('Country of registration');
      if (!s(this.info.businessType)) miss.push('Business type');
      if (!s(this.info.contact?.name)) miss.push('Primary contact name');
      if (!s(this.info.contact?.email)) miss.push('Primary contact email');
      if (!s(this.info.contact?.phone)) miss.push('Primary contact phone');
      if (!s(this.info.description)) miss.push('Short description');
    } else if (step === 1) {
      if (!hasDoc('registrationCertUrl')) miss.push('Registration certificate (PDF)');
      if (!hasDoc('taxPinUrl')) miss.push('Tax identification/PIN (PDF)');
      if (!hasDoc('proofAddressUrl')) miss.push('Proof of address (PDF)');
      if (!hasDoc('founderIdUrl')) miss.push('Founder/admin ID (PDF)');
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
  maxReachableStep(): number {
    const lastIndex = this.stepDefinitions.length - 1;
    for (let i = 0; i < lastIndex; i++) {
      if (!this.validForStep(i)) return i;
    }
    return lastIndex;
  }
  canGoTo(i: number): boolean { return i <= this.maxReachableStep() }

  missingRequired(): string[] {
    this.ensureFormShape();
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
      this.info = this.parseSection(d.info, INFO_TEMPLATE);
      this.verify = this.parseSection(d.verify, VERIFY_TEMPLATE);
      this.staff = this.parseSection(d.staff, STAFF_TEMPLATE);
      this.finance = this.parseSection(d.finance, FINANCE_TEMPLATE);
      this.curriculum = this.parseSection(d.curriculum, CURRICULUM_TEMPLATE);
      this.agreements = this.parseSection(d.agreements, AGREEMENTS_TEMPLATE);
      this.extras = this.parseSection(d.extras, EXTRAS_TEMPLATE);
      this.resetUploadBuffers();
      const reachable = this.maxReachableStep();
      if (this.step > reachable) {
        this.goToStep(reachable, true);
      }
    } catch {}
  }

  private parseSection<T>(raw: any, template: T): T {
    if (raw == null) return clone(template);
    if (typeof raw === 'object') return this.mergeSection(template, raw);
    if (typeof raw === 'string') {
      const parsed = this.tryParseJson(raw);
      if (parsed !== undefined) return this.mergeSection(template, parsed);
      const decoded = this.tryDecodeBase64(raw);
      if (decoded !== undefined) {
        const parsedDecoded = this.tryParseJson(decoded);
        if (parsedDecoded !== undefined) return this.mergeSection(template, parsedDecoded);
      }
    }
    return clone(template);
  }

  private mergeSection<T>(template: T, patch: any): T {
    const base = clone(template);
    if (!patch || typeof patch !== 'object') return base;
    const stack: Array<{ target: any; source: any }> = [{ target: base, source: patch }];
    while (stack.length) {
      const { target, source } = stack.pop()!;
      for (const key of Object.keys(source)) {
        const value = source[key];
        if (Array.isArray(value)) {
          target[key] = value.slice();
        } else if (value && typeof value === 'object') {
          if (!target[key] || typeof target[key] !== 'object' || Array.isArray(target[key])) {
            target[key] = clone(value);
          } else {
            stack.push({ target: target[key], source: value });
          }
        } else {
          target[key] = value;
        }
      }
    }
    return base;
  }

  private tryParseJson(value: string): any | undefined {
    try {
      return JSON.parse(value);
    } catch {
      return undefined;
    }
  }

  private tryDecodeBase64(value: string): string | undefined {
    try {
      if (typeof atob === 'function') {
        return atob(value);
      }
    } catch {
      return undefined;
    }
    return undefined;
  }

  private resetUploadBuffers() {
    this.verifyFiles = {
      registrationCertUrl: null,
      taxPinUrl: null,
      proofAddressUrl: null,
      founderIdUrl: null
    };
    this.financeFiles = { taxDocsUrl: null, bankStatementUrl: null };
  }

  private ensureFormShape() {
    this.info = this.parseSection(this.info, INFO_TEMPLATE);
    this.verify = this.parseSection(this.verify, VERIFY_TEMPLATE);
    this.staff = this.parseSection(this.staff, STAFF_TEMPLATE);
    this.finance = this.parseSection(this.finance, FINANCE_TEMPLATE);
    this.curriculum = this.parseSection(this.curriculum, CURRICULUM_TEMPLATE);
    this.agreements = this.parseSection(this.agreements, AGREEMENTS_TEMPLATE);
    this.extras = this.parseSection(this.extras, EXTRAS_TEMPLATE);
  }

  collectPayload() {
    this.ensureFormShape();
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
      this.ensureFormShape();
      if (!draft) {
        const missing = this.missingRequired();
        if (missing.length) throw new Error(`Missing required fields: ${missing.join(', ')}`);
      }
      // Upload selected PDFs locally and replace URLs
      const maxBytes = 2 * 1024 * 1024; // 2MB client guard for business docs
      const upload = async (f: File): Promise<string> => {
        if (f.size > maxBytes) { throw new Error(`File too large. Max is ${maxBytes} bytes`) }
        if (!/\.pdf$/i.test(f.name)) { throw new Error('Only PDF files are allowed') }
        const form = new FormData(); form.append('file', f);
        const r = await fetch(`${urlFor('schools-api')}/v1/uploads`, { method: 'POST', credentials: 'include', body: form });
        const j = await r.json(); if (!j?.success) throw new Error(j?.message || 'Upload failed');
        return j.data?.url || '';
      };
      this.uploading = true;
      try {
        for (const k of Object.keys(this.verifyFiles) as VerifyDocKey[]) {
          const f = this.verifyFiles[k];
          if (f) {
            this.verify[k] = await upload(f);
          }
        }
        for (const k of Object.keys(this.financeFiles) as FinanceDocKey[]) {
          const arr = this.financeFiles[k];
          if (arr && arr.length) {
            const urls: string[] = [];
            for (const f of arr) { urls.push(await upload(f)) }
            this.finance[k] = urls;
          }
        }
      } catch (e:any) { this.uploadError = e?.message || 'Upload failed'; throw e }
      finally { this.uploading = false }
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
