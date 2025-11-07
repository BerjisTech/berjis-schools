import { Injectable, signal, computed } from '@angular/core';

@Injectable({ providedIn: 'root' })
export class I18nService {
  private langSig = signal<string>(localStorage.getItem('lang') || 'en');
  private dictSig = signal<Record<string, string>>({});

  lang = computed(() => this.langSig());

  async setLang(lang: string) {
    this.langSig.set(lang);
    localStorage.setItem('lang', lang);
    // Apply text direction based on language
    const rtlLangs = new Set(['ar', 'he', 'fa', 'ur']);
    const isRtl = rtlLangs.has((lang || '').toLowerCase());
    document.documentElement.setAttribute('dir', isRtl ? 'rtl' : 'ltr');
    document.documentElement.classList.toggle('rtl', isRtl);
    try {
      const res = await fetch(`/assets/i18n/${lang}.json`);
      const json = await res.json();
      this.dictSig.set(json || {});
    } catch {
      this.dictSig.set({});
    }
  }

  t(key: string): string {
    const dict = this.dictSig();
    return dict[key] || key;
  }
}

export function t(i18n: I18nService, key: string) {
  return i18n.t(key);
}
