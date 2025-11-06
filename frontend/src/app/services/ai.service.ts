import { Injectable } from '@angular/core';
import { urlFor } from '../../app/util';
import { I18nService } from '../i18n.service';

@Injectable({ providedIn: 'root' })
export class AiService {
  private api = urlFor('schools-api');
  constructor(private i18n: I18nService) {}
  async chat(messages: Array<{ role: 'user'|'assistant'|'system'; content: string }>, opts: { model?: string; context?: string } = {}) {
    const body = { messages, model: opts.model ?? 'gpt-4o-mini', context: opts.context ?? window.location.pathname, lang: (localStorage.getItem('lang') || 'en') } as any;
    const res = await fetch(`${this.api}/v1/ai/chat`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    const j = await res.json();
    return j;
  }
}
