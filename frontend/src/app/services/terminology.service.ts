import { Injectable, computed, signal } from '@angular/core';
import { I18nService } from '../i18n.service';

@Injectable({ providedIn: 'root' })
export class TerminologyService {
  private termsSig = signal<Record<string, string>>({});
  lang = computed(() => this.i18n.lang());

  constructor(private i18n: I18nService) { this.load(); }

  private async load() {
    const lang = (this.i18n.lang() || 'en').toLowerCase();
    const tryFiles = [`/assets/terminology/${lang}.json`, `/assets/terminology/en.json`];
    for (const path of tryFiles) {
      try { const res = await fetch(path); if (res.ok) { const j = await res.json(); this.termsSig.set(j||{}); return; } } catch {}
    }
    this.termsSig.set({});
  }

  term(key: string, fallback?: string): string {
    const t = this.termsSig();
    return t[key] || fallback || key;
  }
}
