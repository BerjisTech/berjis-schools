import { Component, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { Router, RouterLink, RouterModule } from '@angular/router';
import { hasAppRole, loginUrl, verifySession } from './util';
import { I18nService } from './i18n.service';
import { PushService } from './services/push.service';
import { TPipe } from './t.pipe';
import { AiChatComponent } from './components/ai-chat/ai-chat.component';

@Component({
  selector: 'app-root',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterModule, RouterLink, TPipe, AiChatComponent],
  templateUrl: './app.component.html'
})
export class AppComponent implements OnInit {
  authed = signal(false);
  showSidebar = signal(true);
  showAccount = signal(false);
  search = signal('');
  loginHref = loginUrl();
  isDark = false;
  fontScale = 1; // 1.0x, cycles upward
  isHighContrast = false;
  announce = signal('');
  isOwner = signal(false);

  constructor(private router: Router, private i18n: I18nService, private push: PushService) {}

  async ngOnInit() {
    const persisted = (localStorage.getItem('theme') || '').toLowerCase();
    const preferDark = persisted === 'dark';
    this.setTheme(preferDark ? 'dark' : 'light');
    // enable touch-friendly mode for coarse pointers
    try {
      const coarse = (navigator as any).maxTouchPoints > 0 || window.matchMedia('(pointer: coarse)').matches;
      document.documentElement.classList.toggle('touch', !!coarse);
    } catch { /* ignore */ }
    // restore accessibility prefs
    const fs = parseFloat(localStorage.getItem('fontScale') || '1');
    if (!Number.isNaN(fs)) { this.setFontScale(Math.min(Math.max(fs, 1), 1.5)); }
    const contrast = (localStorage.getItem('contrast') || 'off').toLowerCase();
    this.setContrast(contrast === 'on');
    // announce on route change and move focus to main content
    this.router.events.subscribe(() => {
      const main = document.getElementById('main-content') as HTMLElement | null;
      if (main) { main.focus(); }
      const title = document.title || 'Page';
      this.announce.set(`Navigated to ${title}`);
    });
    try {
      const result = await verifySession({ attemptRefresh: true });
      const ok = !!result.valid;
      this.authed.set(ok);
      if (ok) {
        try { this.isOwner.set(await hasAppRole('schools', 'owner')); } catch { this.isOwner.set(false) }
      } else {
        this.isOwner.set(false);
      }
    } catch { this.authed.set(false); this.isOwner.set(false); }
    await this.i18n.setLang(localStorage.getItem('lang') || 'en');
  }

  toggleAccount() { this.showAccount.update(x => !x) }
  toggleSidebar() { this.showSidebar.update(x => !x) }
  onSearchSubmit(ev: Event) {
    ev.preventDefault();
    const q = this.search().trim();
    this.router.navigate(['/search'], { queryParams: { q } });
  }

  toggleTheme() { this.setTheme(this.isDark ? 'light' : 'dark'); }
  private setTheme(mode: 'light' | 'dark') {
    this.isDark = mode === 'dark';
    document.documentElement.classList.toggle('dark', mode === 'dark');
    try { localStorage.setItem('theme', mode); } catch { /* no-op: localStorage not available */ }
  }

  // Accessibility: adjustable font sizes (scales rem root)
  toggleFontScale() {
    const steps = [1, 1.125, 1.25, 1.5];
    const idx = steps.findIndex(s => Math.abs(s - this.fontScale) < 0.001);
    const next = steps[(idx + 1) % steps.length];
    this.setFontScale(next);
  }
  private setFontScale(scale: number) {
    this.fontScale = scale;
    document.documentElement.style.setProperty('--font-scale', String(scale));
    try { localStorage.setItem('fontScale', String(scale)); } catch { /* ignore */ }
  }

  // Accessibility: high contrast mode
  toggleContrast() { this.setContrast(!this.isHighContrast); }
  private setContrast(on: boolean) {
    this.isHighContrast = on;
    document.documentElement.classList.toggle('contrast', on);
    try { localStorage.setItem('contrast', on ? 'on' : 'off'); } catch { /* ignore */ }
  }

  setLang(lang: string) { this.i18n.setLang(lang); }
  async enablePush() { try { await this.push.subscribe(); } catch { /* ignore */ } }
  async testPush() {
    try { await fetch(`${location.origin.replace(/:\/\/.+?\//, '://')}/v1/push/test`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ title: 'Berjis Schools', body: 'This is a test notification', url: location.origin }) }); } catch {}
  }
}
