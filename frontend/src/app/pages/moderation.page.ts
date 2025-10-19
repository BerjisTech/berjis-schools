import { Component } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { urlFor } from '../../app/util';

@Component({
  selector: 'app-moderation-page',
  standalone: true,
  imports: [CommonModule, FormsModule],
  template: `
    <div class="p-5 max-w-3xl">
      <h2 class="mt-0 text-lg font-semibold">Moderation & Safety</h2>
      <div class="grid gap-4">
        <div class="border border-gray-200 rounded-xl p-4">
          <h3 class="mt-0 font-semibold">Report Content or User</h3>
          <form class="grid gap-3" (ngSubmit)="report()">
            <div>
              <label class="block text-sm mb-1">Target Type</label>
              <select [(ngModel)]="targetType" name="targetType" class="w-full border border-gray-300 rounded-md px-3 py-2">
                <option value="user">User</option>
                <option value="school">School</option>
                <option value="class">Class</option>
                <option value="subject">Subject</option>
                <option value="lesson">Lesson</option>
                <option value="test">Test</option>
                <option value="other">Other</option>
              </select>
            </div>
            <div *ngIf="targetType!=='other'">
              <label class="block text-sm mb-1">Search {{ targetType | titlecase }}</label>
              <input [(ngModel)]="query" name="query" (input)="search()" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="Type to search" />
              <ul *ngIf="results.length" class="mt-2 border border-gray-200 rounded-md divide-y divide-gray-100">
                <li *ngFor="let r of results" (click)="select(r)" class="px-3 py-2 cursor-pointer hover:bg-gray-50">{{ r.label }}</li>
              </ul>
              <div *ngIf="selectedLabel" class="mt-2 text-sm text-gray-700">Selected: {{ selectedLabel }}</div>
            </div>
            <div *ngIf="targetType==='other'">
              <label class="block text-sm mb-1">Target ID</label>
              <input [(ngModel)]="targetId" name="targetId" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="Enter target reference" />
            </div>
            <div>
              <label class="block text-sm mb-1">Reason</label>
              <input [(ngModel)]="reason" name="reason" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="Short reason" />
            </div>
            <div>
              <label class="block text-sm mb-1">Details</label>
              <textarea [(ngModel)]="details" name="details" rows="3" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="Optional details"></textarea>
            </div>
            <div class="flex items-center gap-3">
              <button type="submit" class="bg-blue-600 text-white px-4 py-2 rounded-md" [disabled]="savingReport">{{ savingReport ? 'Submitting…' : 'Submit Report' }}</button>
              <div *ngIf="reportMsg" class="text-sm" [class.text-green-700]="reportOk" [class.text-red-700]="!reportOk">{{ reportMsg }}</div>
            </div>
          </form>
        </div>

        <div class="border border-gray-200 rounded-xl p-4">
          <h3 class="mt-0 font-semibold">My Blocked Users</h3>
          <div class="grid gap-2">
            <div>
              <label class="block text-sm mb-1">Search User to Block</label>
              <input [(ngModel)]="blockQuery" name="blockQuery" (input)="searchBlock()" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="Type a name" />
              <ul *ngIf="blockResults.length" class="mt-2 border border-gray-200 rounded-md divide-y divide-gray-100">
                <li *ngFor="let r of blockResults" (click)="selectBlock(r)" class="px-3 py-2 cursor-pointer hover:bg-gray-50">{{ r.label }}</li>
              </ul>
              <div *ngIf="blockSelectedLabel" class="mt-2 text-sm text-gray-700">Selected: {{ blockSelectedLabel }}</div>
              <div class="mt-2 flex gap-2">
                <button (click)="block()" class="bg-gray-800 text-white px-4 py-2 rounded-md">Block</button>
                <button type="button" (click)="loadBlocks()" class="bg-gray-200 text-gray-800 px-3 py-2 rounded-md">Refresh</button>
              </div>
            </div>
          </div>
          <ul class="mt-3 list-disc pl-5">
            <li *ngFor="let b of blocks" class="flex items-center gap-3"><span class="flex-1">{{ b.blockedUserId }}</span><button (click)="unblock(b.blockedUserId)" class="text-sm text-blue-700 hover:underline">Unblock</button></li>
          </ul>
        </div>
      </div>
    </div>
  `
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
