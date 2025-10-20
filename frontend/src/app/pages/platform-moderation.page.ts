import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { urlFor } from '../../app/util';

@Component({
  selector: 'app-platform-moderation-page',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './platform-moderation.page.html'
})
export class PlatformModerationPage implements OnInit {
  status: 'open'|'reviewed'|'dismissed'|'action_taken' = 'open';
  reports: any[] = [];
  forbidden = false;

  async ngOnInit() { await this.loadReports() }

  async loadReports() {
    this.forbidden = false;
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/moderation/reports?status=${this.status}`, { credentials: 'include' });
      if (res.status === 403) { this.forbidden = true; this.reports = []; return }
      const j = await res.json();
      this.reports = j?.data ?? [];
    } catch { this.reports = [] }
  }

  async resolve(id: string, status: 'reviewed'|'dismissed'|'action_taken') {
    try {
      await fetch(`${urlFor('schools-api')}/v1/moderation/reports/${id}/resolve`, {
        method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ status })
      });
      await this.loadReports();
    } catch {}
  }
}
