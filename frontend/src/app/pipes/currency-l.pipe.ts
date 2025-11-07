import { Pipe, PipeTransform } from '@angular/core';
import { I18nService } from '../i18n.service';

@Pipe({ name: 'lcurrency', standalone: true })
export class LCurrencyPipe implements PipeTransform {
  constructor(private i18n: I18nService) {}
  transform(amountCents: number | null | undefined, currency: string = 'USD', opts: Intl.NumberFormatOptions = {}): string {
    const cents = typeof amountCents === 'number' ? amountCents : 0;
    const amount = cents / 100;
    const lang = (this.i18n.lang() || 'en').toLowerCase();
    const locale = lang === 'fr' ? 'fr-FR' : lang === 'ar' ? 'ar-EG' : lang === 'de' ? 'de-DE' : 'en-US';
    const format: Intl.NumberFormatOptions = { style: 'currency', currency, currencyDisplay: 'symbol', ...opts };
    try { return new Intl.NumberFormat(locale, format).format(amount); } catch { return `${currency} ${amount.toFixed(2)}`; }
  }
}

