import { Pipe, PipeTransform } from '@angular/core';
import { I18nService } from '../i18n.service';

@Pipe({ name: 'dateL', standalone: true })
export class DateLPipe implements PipeTransform {
  constructor(private i18n: I18nService) {}
  transform(value: string | number | Date | null | undefined, opts: Intl.DateTimeFormatOptions = { dateStyle: 'medium' }): string {
    if (!value) return '';
    const date = typeof value === 'string' || typeof value === 'number' ? new Date(value) : value;
    const lang = (this.i18n.lang() || 'en').toLowerCase();
    const locale = lang === 'fr' ? 'fr-FR' : lang === 'ar' ? 'ar-EG' : lang === 'de' ? 'de-DE' : 'en-US';
    try { return new Intl.DateTimeFormat(locale, opts).format(date); } catch { return String(date); }
  }
}

