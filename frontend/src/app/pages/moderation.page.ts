import { Component } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { urlFor } from '../../app/util';

@Component({
  selector: 'app-moderation-page',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './moderation.page.html'
})
export class ModerationPage {
  targetType: 'user'|'school'|'class'|'subject'|'lesson'|'test'|'other' = 'user';
  targetId = '';
  reason = '';
  details = '';
  savingReport = false;
  reportMsg = '';
  reportOk = false;
  query = '';
  results: { id: string; label: string }[] = [];
  selectedLabel = '';

  blockUserId = '';
  blockQuery = '';
  blockResults: { id: string; label: string }[] = [];
  blockSelectedLabel = '';
  blocks: { blockedUserId: string; createdAt: string }[] = [];

  async report() {
    this.savingReport = true; this.reportMsg = ''; this.reportOk = false;
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/moderation/reports`, {
        method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ targetType: this.targetType, targetId: this.targetId, reason: this.reason || null, details: this.details || null })
      });
      const j = await res.json();
      if (!j?.success) throw new Error(j?.message || 'Failed');
      this.reportOk = true; this.reportMsg = 'Report submitted.';
      this.targetId = ''; this.reason = ''; this.details = '';
    } catch (e: any) { this.reportMsg = e?.message || 'Failed to submit report' }
    finally { this.savingReport = false }
  }

  async search() {
    if (this.targetType === 'other') return;
    const type = this.targetType;
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/search?type=${type}&q=${encodeURIComponent(this.query)}`, { credentials: 'include' });
      const j = await res.json();
      const arr = j?.data ?? [];
      this.results = arr.map((x: any) => ({ id: x.id || x.userId, label: x.displayName || x.name || x.title || x.id || x.userId }));
    } catch { this.results = [] }
  }

  select(r: { id: string; label: string }) {
    this.targetId = r.id; this.selectedLabel = r.label; this.results = [];
  }

  async loadBlocks() {
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/users/blocks`, { credentials: 'include' });
      const j = await res.json();
      this.blocks = j?.data ?? [];
    } catch { this.blocks = [] }
  }

  async searchBlock() {
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/search?type=user&q=${encodeURIComponent(this.blockQuery)}`, { credentials: 'include' });
      const j = await res.json();
      const arr = j?.data ?? [];
      this.blockResults = arr.map((x: any) => ({ id: x.userId, label: x.displayName || x.userId }));
    } catch { this.blockResults = [] }
  }

  selectBlock(r: { id: string; label: string }) {
    this.blockUserId = r.id; this.blockSelectedLabel = r.label; this.blockResults = [];
  }

  async block() {
    try {
      await fetch(`${urlFor('schools-api')}/v1/users/blocks`, {
        method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ blockedUserId: this.blockUserId })
      });
      this.blockUserId = '';
      await this.loadBlocks();
    } catch {}
  }

  async unblock(uid: string) {
    try {
      await fetch(`${urlFor('schools-api')}/v1/users/blocks/${uid}`, { method: 'DELETE', credentials: 'include' });
      await this.loadBlocks();
    } catch {}
  }
}


