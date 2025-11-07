import { Component, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { PushService } from '../services/push.service';

@Component({
  selector: 'app-notification-settings-page',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './notification-settings.page.html'
})
export class NotificationSettingsPage implements OnInit {
  subs = signal<Array<{ endpoint: string; createdAt?: string }>>([]);
  loading = signal(false);
  message = signal<string | null>(null);

  constructor(private push: PushService) {}

  async ngOnInit() { await this.refresh(); }

  async refresh() {
    this.loading.set(true);
    try {
      const res = await fetch('/v1/push/subscriptions', { credentials: 'include' });
      const j = await res.json();
      const rows = (j?.data ?? []) as any[];
      this.subs.set(rows.map(r => ({ endpoint: r.endpoint, createdAt: r.createdAt })));
    } catch { this.subs.set([]); }
    this.loading.set(false);
  }

  async enable() { const ok = await this.push.subscribe(); this.message.set(ok ? 'Push enabled' : 'Push failed'); await this.refresh(); }
  async remove(ep: string) {
    try { await fetch('/v1/push/subscribe', { method: 'DELETE', credentials: 'include', headers: { 'Content-Type':'application/json' }, body: JSON.stringify({ endpoint: ep }) }); } catch {}
    await this.refresh();
  }
  async test() { try { await fetch('/v1/push/test', { method: 'POST', credentials: 'include', headers: { 'Content-Type':'application/json' }, body: JSON.stringify({ title: 'Berjis Schools', body: 'Test notification', url: location.origin }) }); this.message.set('Test sent'); } catch { this.message.set('Test failed'); } }
}

