import { Component, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { Router, RouterLink, RouterModule } from '@angular/router';
import { loginUrl, verifySession } from './util';
import { I18nService } from './i18n.service';
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

  constructor(private router: Router, private i18n: I18nService) {}

  async ngOnInit() {
    const persisted = (localStorage.getItem('theme') || '').toLowerCase();
    const preferDark = persisted === 'dark';
    this.setTheme(preferDark ? 'dark' : 'light');
    try {
      const result = await verifySession({ attemptRefresh: true });
      this.authed.set(!!result.valid);
    } catch {
      this.authed.set(false);
    }
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

  setLang(lang: string) { this.i18n.setLang(lang); }
}
