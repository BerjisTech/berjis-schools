import { Component, OnDestroy, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import { Subscription } from 'rxjs';
import { TPipe } from '../t.pipe';
import { AuthedUserProfile, fetchUserProfile, urlFor } from '../../app/util';

type StepSlug = 'info' | 'verification' | 'education' | 'teaching' | 'media' | 'consents' | 'payout' | 'review';

interface StepDefinition { label: string; slug: StepSlug; description: string; }

type UploadSection = 'verification' | 'education' | 'media';

interface UploadOption {
  accept: string;
  allowedExt?: RegExp;
  allowedExamples: string;
  hint: string;
  maxBytes?: number;
}

const STEP_DEFINITIONS: StepDefinition[] = [
  { label: 'Personal Info', slug: 'info', description: 'Introduce yourself, contact details, and teaching focus.' },
  { label: 'Verification', slug: 'verification', description: 'Upload a government ID and identity confirmation.' },
  { label: 'Education & Credentials', slug: 'education', description: 'Share academic achievements and supporting documents.' },
  { label: 'Teaching Profile', slug: 'teaching', description: 'Describe your subjects, rates, and availability.' },
  { label: 'Media', slug: 'media', description: 'Showcase your teaching with intro and demo recordings.' },
  { label: 'Consents & Agreements', slug: 'consents', description: 'Acknowledge safety, conduct, and quality standards.' },
  { label: 'Payout', slug: 'payout', description: 'Tell us how to send earnings and required payee info.' },
  { label: 'Review & Submit', slug: 'review', description: 'Review your application before submitting to our team.' },
];

const ID_TYPES = [
  'National ID / Identity Card',
  'Passport',
  'Driver License',
  'Residence Permit',
  'Student ID',
  'Other',
];

const FALLBACK_COUNTRIES = [
  { code: 'US', name: 'United States' },
  { code: 'CA', name: 'Canada' },
  { code: 'GB', name: 'United Kingdom' },
  { code: 'KE', name: 'Kenya' },
  { code: 'UG', name: 'Uganda' },
  { code: 'TZ', name: 'Tanzania' },
  { code: 'NG', name: 'Nigeria' },
  { code: 'ZA', name: 'South Africa' },
  { code: 'GH', name: 'Ghana' },
  { code: 'IN', name: 'India' },
  { code: 'AE', name: 'United Arab Emirates' },
  { code: 'DE', name: 'Germany' },
  { code: 'FR', name: 'France' },
  { code: 'AU', name: 'Australia' },
  { code: 'BR', name: 'Brazil' },
  { code: 'CN', name: 'China' },
  { code: 'JP', name: 'Japan' },
  { code: 'SG', name: 'Singapore' },
  { code: 'SA', name: 'Saudi Arabia' },
  { code: 'MX', name: 'Mexico' },
];

const FALLBACK_TIMEZONES = [
  'UTC',
  'Africa/Nairobi',
  'Africa/Lagos',
  'Africa/Johannesburg',
  'Africa/Cairo',
  'America/New_York',
  'America/Chicago',
  'America/Denver',
  'America/Los_Angeles',
  'America/Sao_Paulo',
  'Europe/London',
  'Europe/Paris',
  'Europe/Berlin',
  'Europe/Moscow',
  'Asia/Dubai',
  'Asia/Kolkata',
  'Asia/Singapore',
  'Asia/Tokyo',
  'Asia/Shanghai',
  'Australia/Sydney',
];

function buildCountries(): Array<{ code: string; name: string }> {
  try {
    const supported = typeof (Intl as any).supportedValuesOf === 'function'
      ? (Intl as any).supportedValuesOf('region') as string[]
      : [];
    const display = typeof Intl.DisplayNames !== 'undefined'
      ? new Intl.DisplayNames(['en'], { type: 'region' })
      : null;
    const mapped = supported
      .filter(code => /^[A-Z]{2}$/.test(code))
      .map(code => {
        const name = display?.of(code);
        return { code, name: name || code };
      })
      .filter(opt => !!opt.name)
      .sort((a, b) => a.name.localeCompare(b.name));
    if (mapped.length) {
      const withoutDupes = mapped.filter((opt, idx, arr) => arr.findIndex(o => o.code === opt.code) === idx);
      if (!withoutDupes.some(opt => opt.code === 'OTHER')) {
        withoutDupes.push({ code: 'OTHER', name: 'Other / Not listed' });
      }
      return withoutDupes;
    }
  } catch {
    // ignore and fallback
  }
  const fallback = [...FALLBACK_COUNTRIES];
  fallback.push({ code: 'OTHER', name: 'Other / Not listed' });
  return fallback;
}

function buildTimezones(): string[] {
  try {
    if (typeof (Intl as any).supportedValuesOf === 'function') {
      const supported = (Intl as any).supportedValuesOf('timeZone') as string[];
      if (supported?.length) {
        const deduped = Array.from(new Set(supported));
        return deduped.sort((a, b) => a.localeCompare(b));
      }
    }
  } catch {
    // ignore and fallback
  }
  return [...FALLBACK_TIMEZONES];
}

const COUNTRY_OPTIONS = buildCountries();
const TIMEZONE_OPTIONS = buildTimezones();

const UPLOAD_FIELD_OPTIONS: Record<string, UploadOption> = {
  'verification.govIdUrl': {
    accept: '.pdf,.png,.jpg,.jpeg,.webp,.heic',
    allowedExt: /\.(pdf|png|jpe?g|webp|heic)$/i,
    allowedExamples: 'PDF, PNG, JPG, WEBP, HEIC',
    hint: 'Upload a clear scan or photo of your government-issued ID (max 2 MB).',
  },
  'verification.selfieUrl': {
    accept: '.pdf,.png,.jpg,.jpeg,.webp,.mp4,.mov,.m4v,.webm',
    allowedExt: /\.(pdf|png|jpe?g|webp|mp4|mov|m4v|webm)$/i,
    allowedExamples: 'PDF, PNG, JPG, WEBP, MP4, MOV, WebM',
    hint: 'Upload a selfie or short video showing you holding the ID (max 2 MB).',
  },
  'verification.proofResidenceUrl': {
    accept: '.pdf,.png,.jpg,.jpeg,.webp',
    allowedExt: /\.(pdf|png|jpe?g|webp)$/i,
    allowedExamples: 'PDF, PNG, JPG, WEBP',
    hint: 'Optional: upload proof of residence such as a bill or lease (max 2 MB).',
  },
  'education.degreeUrl': {
    accept: '.pdf,.png,.jpg,.jpeg,.webp',
    allowedExt: /\.(pdf|png|jpe?g|webp)$/i,
    allowedExamples: 'PDF, PNG, JPG, WEBP',
    hint: 'Upload your highest degree or diploma (max 2 MB).',
  },
  'education.certificateUrl': {
    accept: '.pdf,.png,.jpg,.jpeg,.webp',
    allowedExt: /\.(pdf|png|jpe?g|webp)$/i,
    allowedExamples: 'PDF, PNG, JPG, WEBP',
    hint: 'Optional: additional certifications (max 2 MB).',
  },
  'education.transcriptUrl': {
    accept: '.pdf,.png,.jpg,.jpeg,.webp',
    allowedExt: /\.(pdf|png|jpe?g|webp)$/i,
    allowedExamples: 'PDF, PNG, JPG, WEBP',
    hint: 'Optional: upload academic transcripts (max 2 MB).',
  },
  'education.portfolioUrl': {
    accept: '.pdf,.png,.jpg,.jpeg,.webp',
    allowedExt: /\.(pdf|png|jpe?g|webp)$/i,
    allowedExamples: 'PDF, PNG, JPG, WEBP',
    hint: 'Optional: showcase portfolio or sample work (max 2 MB).',
  },
  'education.referenceUrl': {
    accept: '.pdf,.png,.jpg,.jpeg,.webp',
    allowedExt: /\.(pdf|png|jpe?g|webp)$/i,
    allowedExamples: 'PDF, PNG, JPG, WEBP',
    hint: 'Optional: reference letter (max 2 MB).',
  },
  'media.introUrl': {
    accept: '.mp4,.mov,.m4v,.webm,.pdf',
    allowedExt: /\.(mp4|mov|m4v|webm|pdf)$/i,
    allowedExamples: 'MP4, MOV, M4V, WebM, PDF',
    hint: 'Upload a short introduction video or PDF script (max 2 MB).',
  },
  'media.demoUrl': {
    accept: '.mp4,.mov,.m4v,.webm,.pdf',
    allowedExt: /\.(mp4|mov|m4v|webm|pdf)$/i,
    allowedExamples: 'MP4, MOV, M4V, WebM, PDF',
    hint: 'Upload a demo lesson clip or teaching sample (max 2 MB).',
  },
};

function mergeWithDefaults<T extends Record<string, unknown>>(defaults: T, source: unknown): T {
  const result: Record<string, unknown> = { ...defaults };
  if (!source || typeof source !== 'object') {
    return result as T;
  }
  const incoming = source as Record<string, unknown>;
  for (const [key, defaultValue] of Object.entries(defaults)) {
    const value = incoming[key];
    if (typeof defaultValue === 'boolean') {
      result[key] = typeof value === 'boolean' ? value : defaultValue;
    } else if (Array.isArray(defaultValue)) {
      result[key] = Array.isArray(value) ? value : defaultValue;
    } else {
      result[key] = typeof value === 'string' ? value : (value ?? defaultValue);
    }
  }
  return result as T;
}

@Component({
  selector: 'app-become-tutor-page',
  standalone: true,
  imports: [CommonModule, FormsModule, TPipe],
  templateUrl: './become-tutor.page.html'
})
export class BecomeTutorPage implements OnInit, OnDestroy {
  private paramSub?: Subscription;

  readonly steps = STEP_DEFINITIONS;
  readonly idTypes = ID_TYPES;
  readonly countries = COUNTRY_OPTIONS;
  readonly timezones = TIMEZONE_OPTIONS;
  readonly uploadOptions = UPLOAD_FIELD_OPTIONS;
  readonly apiBase = urlFor('schools-api');
  readonly maxUploadBytes = 2 * 1024 * 1024;

  get currentStep(): StepDefinition { return this.steps[this.step]; }

  private readonly defaultProfile = {
    legalName: '',
    displayName: '',
    email: '',
    phone: '',
    country: '',
    languages: '',
    timezone: ''
  };

  private readonly defaultVerification = {
    govIdType: '',
    govIdUrl: '',
    selfieUrl: '',
    proofResidenceUrl: ''
  };

  private readonly defaultEducation = {
    degreeUrl: '',
    certificateUrl: '',
    transcriptUrl: '',
    portfolioUrl: '',
    referenceUrl: ''
  };

  private readonly defaultTeaching = {
    levels: '',
    formats: '',
    rate: '',
    availability: ''
  };

  private readonly defaultMedia = {
    introUrl: '',
    demoUrl: ''
  };

  private readonly defaultPayout = {
    method: '',
    name: '',
    tax: ''
  };

  private readonly defaultConsents = {
    backgroundCheck: false,
    codeOfConduct: false,
    cleanRecord: false,
    lessonRecording: false
  };
  private readonly draftStorageKey = 'tutorOnboardingDraft';
  private localDraftLoaded = false;

  bio = '';
  subjects = '';
  saving = false;
  message = '';
  success = false;
  status: 'pending' | 'approved' | 'rejected' | null = null;
  statusLine = '';
  profile = { ...this.defaultProfile };
  verification = { ...this.defaultVerification };
  education = { ...this.defaultEducation };
  teaching = { ...this.defaultTeaching };
  media = { ...this.defaultMedia };
  payout = { ...this.defaultPayout };
  consents = { ...this.defaultConsents };

  step = 0;

  selectedCountryCode = '';
  useManualCountry = false;
  manualCountry = '';
  selectedTimezone = '';
  useManualTimezone = false;
  manualTimezone = '';

  uploadingField: Record<string, boolean> = {};
  uploadErrors: Record<string, string | undefined> = {};
  private userProfilePrefillLocked = false;

  constructor(private router: Router, private route: ActivatedRoute) {}

  get progressPct(): number {
    const missing = this.missingRequired();
    const total = 14; // matches server required count
    const done = Math.max(0, total - missing.length);
    return Math.round((done / total) * 100);
  }

  ngOnInit(): void {
    this.loadLocalDraft();
    this.paramSub = this.route.paramMap.subscribe(params => {
      const slug = params.get('section');
      this.setStepBySlug(slug);
    });
    this.refreshStatus();
  }

  ngOnDestroy(): void {
    this.paramSub?.unsubscribe();
  }

  private setStepBySlug(slug: string | null): void {
    const fallbackSlug = this.steps[0].slug;
    const targetSlug = (slug as StepSlug | null) ?? fallbackSlug;
    const idx = this.steps.findIndex(step => step.slug === targetSlug);
    if (idx === -1) {
      this.router.navigate(['/tutors/become', fallbackSlug], { replaceUrl: true });
      return;
    }
    this.step = idx;
  }

  private getSection(section: UploadSection): Record<string, string> {
    switch (section) {
      case 'verification': return this.verification;
      case 'education': return this.education;
      default: return this.media;
    }
  }

  private normalizeProfileSelections(): void {
    if (this.useManualCountry) {
      this.profile.country = this.manualCountry.trim();
    } else if (this.selectedCountryCode && this.selectedCountryCode !== 'OTHER') {
      const match = this.countries.find(c => c.code === this.selectedCountryCode);
      this.profile.country = match ? match.name : this.profile.country;
    } else if (this.selectedCountryCode === 'OTHER') {
      this.profile.country = this.manualCountry.trim();
    }
    if (this.useManualTimezone || this.selectedTimezone === '__manual') {
      this.profile.timezone = this.manualTimezone.trim();
    } else if (this.selectedTimezone) {
      this.profile.timezone = this.selectedTimezone;
    }
  }

  private syncCountryControls(): void {
    const current = (this.profile.country || '').trim();
    if (!current) {
      this.selectedCountryCode = '';
      this.useManualCountry = false;
      this.manualCountry = '';
      return;
    }
    const match = this.countries.find(c => c.name.toLowerCase() === current.toLowerCase() || c.code.toLowerCase() === current.toLowerCase());
    if (match && match.code !== 'OTHER') {
      this.selectedCountryCode = match.code;
      this.useManualCountry = false;
      this.manualCountry = '';
      this.profile.country = match.name;
    } else {
      this.selectedCountryCode = 'OTHER';
      this.useManualCountry = true;
      this.manualCountry = current;
    }
  }

  private syncTimezoneControls(): void {
    const current = (this.profile.timezone || '').trim();
    if (!current) {
      this.selectedTimezone = '';
      this.useManualTimezone = false;
      this.manualTimezone = '';
      return;
    }
    if (this.timezones.includes(current)) {
      this.selectedTimezone = current;
      this.useManualTimezone = false;
      this.manualTimezone = '';
    } else {
      this.selectedTimezone = '__manual';
      this.useManualTimezone = true;
      this.manualTimezone = current;
    }
  }

  onCountrySelect(code: string): void {
    this.selectedCountryCode = code;
    if (code === 'OTHER') {
      this.useManualCountry = true;
      this.profile.country = this.manualCountry.trim();
    } else {
      this.useManualCountry = false;
      const match = this.countries.find(c => c.code === code);
      this.profile.country = match ? match.name : '';
    }
  }

  onManualCountryChange(value: string): void {
    this.manualCountry = value;
    if (this.useManualCountry || this.selectedCountryCode === 'OTHER') {
      this.profile.country = value.trim();
    }
  }

  onTimezoneSelect(value: string): void {
    this.selectedTimezone = value;
    if (value === '__manual') {
      this.useManualTimezone = true;
      this.profile.timezone = this.manualTimezone.trim();
    } else {
      this.useManualTimezone = false;
      this.profile.timezone = value;
    }
  }

  onManualTimezoneChange(value: string): void {
    this.manualTimezone = value;
    if (this.useManualTimezone || this.selectedTimezone === '__manual') {
      this.profile.timezone = value.trim();
    }
  }

  async refreshStatus(): Promise<void> {
    try {
      const res = await fetch(`${this.apiBase}/v1/tutors/me`, { credentials: 'include' });
      const j = await res.json();
      const d = j?.data;
      if (d) {
        this.status = d.status;
        this.statusLine = `Current status: ${d.status}`;
        this.bio = typeof d.bio === 'string' ? d.bio : '';
        this.subjects = typeof d.subjects === 'string' ? d.subjects : '';
        this.profile = mergeWithDefaults(this.defaultProfile, d.profile);
        this.verification = mergeWithDefaults(this.defaultVerification, d.verification);
        this.education = mergeWithDefaults(this.defaultEducation, d.education);
        this.teaching = mergeWithDefaults(this.defaultTeaching, d.teaching);
        this.media = mergeWithDefaults(this.defaultMedia, d.media);
        this.payout = mergeWithDefaults(this.defaultPayout, d.payout);
        this.consents = mergeWithDefaults(this.defaultConsents, d.consents);
        const legacyEducation = d.education;
        if (legacyEducation && typeof legacyEducation === 'object') {
          const legacyCert = (legacyEducation as Record<string, any>).certUrl;
          if (!this.education.certificateUrl && typeof legacyCert === 'string') {
            this.education.certificateUrl = legacyCert;
          }
        }
        this.persistDraftSnapshot();
      } else {
        this.status = null;
        this.statusLine = '';
        if (!this.localDraftLoaded) {
          this.resetToDefaults();
        }
      }
    } catch {
      this.status = null;
      this.statusLine = '';
    } finally {
      this.syncCountryControls();
      this.syncTimezoneControls();
      if (!this.userProfilePrefillLocked) {
        const applied = await this.applyUserProfileDefaults();
        if (applied) {
          this.syncCountryControls();
          this.syncTimezoneControls();
        }
        this.userProfilePrefillLocked = true;
      }
    }
  }

  private async applyUserProfileDefaults(): Promise<boolean> {
    let profile: AuthedUserProfile | null = null;
    try {
      profile = await fetchUserProfile();
    } catch {
      profile = null;
    }
    if (!profile) return false;
    const isBlank = (value: any): boolean => typeof value !== 'string' || value.trim().length === 0;
    const name = typeof profile.name === 'string' ? profile.name.trim() : '';
    const username = typeof profile.username === 'string' ? profile.username.trim() : '';
    const displayName = name || username;
    const email = typeof profile.email === 'string' ? profile.email.trim() : '';
    const phone = this.extractPhone(profile);
    let changed = false;
    if (isBlank(this.profile.displayName) && displayName) {
      this.profile.displayName = displayName;
      changed = true;
    }
    if (isBlank(this.profile.legalName) && name) {
      this.profile.legalName = name;
      changed = true;
    }
    if (isBlank(this.profile.email) && email) {
      this.profile.email = email;
      changed = true;
    }
    if (isBlank(this.profile.phone) && phone) {
      this.profile.phone = phone;
      changed = true;
    }
    return changed;
  }

  private extractPhone(profile: AuthedUserProfile | null): string {
    if (!profile) return '';
    const prefs: any = profile.preferences ?? {};
    const candidates: unknown[] = [
      (profile as any)?.phone,
      prefs?.contactPhone,
      prefs?.contact?.phone,
      prefs?.contact?.mobile,
      prefs?.profile?.phone,
      prefs?.profile?.mobile,
      prefs?.phone
    ];
    for (const value of candidates) {
      if (typeof value === 'string') {
        const trimmed = value.trim();
        if (trimmed) return trimmed;
      }
    }
    return '';
  }

  private resetToDefaults(): void {
    this.bio = '';
    this.subjects = '';
    this.profile = { ...this.defaultProfile };
    this.verification = { ...this.defaultVerification };
    this.education = { ...this.defaultEducation };
    this.teaching = { ...this.defaultTeaching };
    this.media = { ...this.defaultMedia };
    this.payout = { ...this.defaultPayout };
    this.consents = { ...this.defaultConsents };
    this.clearDraftSnapshot();
  }

  back(): void {
    if (this.step === 0) return;
    this.goToStep(this.step - 1);
  }

  goToStep(index: number): void {
    if (index < 0 || index >= this.steps.length) return;
    if (index > this.step && !this.canGoTo(index)) return;
    const slug = this.steps[index].slug;
    this.router.navigate(['../', slug], { relativeTo: this.route });
  }

  next(): void {
    this.normalizeProfileSelections();
    if (!this.validForStep(this.step)) {
      this.success = false;
      this.message = `Please complete: ${this.fieldsMissingForStep(this.step).join(', ')}`;
      return;
    }
    if (this.step < this.steps.length - 1) {
      this.saveDraft();
      this.goToStep(this.step + 1);
    }
  }

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
      if (!s(this.verification.govIdUrl)) miss.push('Government ID upload');
      if (!s(this.verification.selfieUrl)) miss.push('Selfie/Video with ID');
    } else if (step === 2) {
      if (!s(this.education.degreeUrl)) miss.push('Highest degree upload');
    } else if (step === 4) {
      if (!s(this.media.introUrl)) miss.push('Video introduction');
      if (!s(this.media.demoUrl)) miss.push('Demo lesson/sample');
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

  validForStep(step: number): boolean {
    return this.fieldsMissingForStep(step).length === 0;
  }

  maxReachableStep(): number {
    for (let i = 0; i <= 6; i++) {
      if (!this.validForStep(i)) return i;
    }
    return 7;
  }

  canGoTo(i: number): boolean {
    return i <= this.maxReachableStep();
  }

  isStepComplete(i: number): boolean {
    return i < this.step && this.fieldsMissingForStep(i).length === 0;
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

  async saveDraft(): Promise<void> {
    this.normalizeProfileSelections();
    await this.apply(true, true);
    this.persistDraftSnapshot();
  }

  async onFileChange(section: UploadSection, key: string, event: Event): Promise<void> {
    const input = event.target as HTMLInputElement;
    const file = input?.files?.[0];
    if (!file) return;
    const mapKey = `${section}.${key}`;
    this.uploadErrors[mapKey] = undefined;
    this.uploadingField[mapKey] = true;
    try {
      this.validateFile(mapKey, file);
      const url = await this.uploadDocument(file);
      this.getSection(section)[key] = url;
    } catch (e: any) {
      this.uploadErrors[mapKey] = e?.message || 'Upload failed';
    } finally {
      this.uploadingField[mapKey] = false;
      if (input) input.value = '';
    }
  }

  clearFile(section: UploadSection, key: string): void {
    const mapKey = `${section}.${key}`;
    this.getSection(section)[key] = '';
    this.uploadErrors[mapKey] = undefined;
  }

  private loadLocalDraft(): void {
    try {
      const raw = localStorage.getItem(this.draftStorageKey);
      if (!raw) return;
      const data = JSON.parse(raw);
      if (!data || typeof data !== 'object') return;
      if (typeof data.bio === 'string') this.bio = data.bio;
      if (typeof data.subjects === 'string') this.subjects = data.subjects;
      if (data.profile) this.profile = mergeWithDefaults(this.defaultProfile, data.profile);
      if (data.verification) this.verification = mergeWithDefaults(this.defaultVerification, data.verification);
      if (data.education) this.education = mergeWithDefaults(this.defaultEducation, data.education);
      if (data.teaching) this.teaching = mergeWithDefaults(this.defaultTeaching, data.teaching);
      if (data.media) this.media = mergeWithDefaults(this.defaultMedia, data.media);
      if (data.payout) this.payout = mergeWithDefaults(this.defaultPayout, data.payout);
      if (data.consents) this.consents = mergeWithDefaults(this.defaultConsents, data.consents);
      this.localDraftLoaded = true;
    } catch {
      // ignore corrupt drafts
    }
  }

  private persistDraftSnapshot(): void {
    try {
      const snapshot = {
        bio: this.bio,
        subjects: this.subjects,
        profile: { ...this.profile },
        verification: { ...this.verification },
        education: { ...this.education },
        teaching: { ...this.teaching },
        media: { ...this.media },
        payout: { ...this.payout },
        consents: { ...this.consents }
      };
      localStorage.setItem(this.draftStorageKey, JSON.stringify(snapshot));
      this.localDraftLoaded = true;
    } catch {
      // storage may be unavailable, ignore
    }
  }

  private clearDraftSnapshot(): void {
    try { localStorage.removeItem(this.draftStorageKey) } catch { /* no-op */ }
    this.localDraftLoaded = false;
  }

  resolveUploadUrl(path: string | null | undefined): string | null {
    if (!path) return null;
    if (/^https?:\/\//i.test(path) || path.startsWith('data:')) return path;
    return `${this.apiBase}${path.startsWith('/') ? path : `/${path}`}`;
  }

  displayFileName(path: string | null | undefined): string {
    if (!path) return '';
    const cleaned = path.split('?')[0];
    const segments = cleaned.split('/');
    return decodeURIComponent(segments[segments.length - 1] || cleaned);
  }

  private validateFile(fieldKey: string, file: File): void {
    const cfg = this.uploadOptions[fieldKey];
    const max = cfg?.maxBytes ?? this.maxUploadBytes;
    if (file.size > max) {
      const mb = (max / (1024 * 1024)).toFixed(1).replace(/\.0$/, '');
      throw new Error(`File too large. Max size is ${mb} MB.`);
    }
    if (cfg?.allowedExt && !cfg.allowedExt.test(file.name)) {
      throw new Error(`File type not supported. Allowed: ${cfg.allowedExamples}.`);
    }
  }

  private async uploadDocument(file: File): Promise<string> {
    const form = new FormData();
    form.append('file', file);
    const res = await fetch(`${this.apiBase}/v1/uploads`, { method: 'POST', credentials: 'include', body: form });
    const j = await res.json();
    if (!j?.success) {
      throw new Error(j?.message || 'Upload failed');
    }
    const url = j.data?.url;
    if (!url) {
      throw new Error('Upload failed: missing URL');
    }
    return url;
  }

  async apply(draft: boolean, silent = false): Promise<void> {
    this.normalizeProfileSelections();
    this.saving = true; this.message = ''; this.success = false;
    try {
      const res = await fetch(`${this.apiBase}/v1/tutors/apply`, {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
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
      this.success = !silent;
      this.message = !silent ? (draft ? 'Draft saved.' : 'Application submitted. Status set to pending.') : '';
      await this.refreshStatus();
    } catch (e: any) {
      this.message = e?.message || 'Failed to submit application';
    } finally {
      this.saving = false;
    }
  }
}

