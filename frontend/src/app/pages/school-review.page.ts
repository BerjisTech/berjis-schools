import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { urlFor } from '../../app/util';

@Component({
  selector: 'app-school-review-page',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './school-review.page.html'
})
export class SchoolReviewPage implements OnInit {
  status: 'pending'|'approved'|'rejected' = 'pending';
  rows: any[] = [];
  forbidden = false;

  async ngOnInit() { await this.load() }

  async load() {
    this.forbidden = false;
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/schools/applications?status=${this.status}`, { credentials: 'include' });
      if (res.status === 403) { this.forbidden = true; this.rows = []; return }
      const j = await res.json();
      this.rows = j?.data ?? [];
    } catch { this.rows = [] }
  }

  async approve(id: string) {
    try { await fetch(`${urlFor('schools-api')}/v1/schools/applications/${id}/approve`, { method: 'POST', credentials: 'include' }); await this.load() } catch {}
  }
  async reject(id: string) {
    const reason = prompt('Reason for rejection?') || '';
    try { await fetch(`${urlFor('schools-api')}/v1/schools/applications/${id}/reject`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ reason }) }); await this.load() } catch {}
  }
}
