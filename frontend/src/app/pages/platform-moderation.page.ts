import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { urlFor } from '../../app/util';

@Component({
  selector: 'app-platform-moderation-page',
  standalone: true,
  imports: [CommonModule, FormsModule],
  template: `
    <div class="p-5 max-w-5xl">
      <h2 class="mt-0 text-lg font-semibold">Platform Moderation</h2>
      <div *ngIf="forbidden" class="text-red-700">You are not a platform moderator.</div>
      <div *ngIf="!forbidden" class="grid gap-4">
        <div class="flex gap-6">
          <a routerLink="/moderation/tutors" class="text-sm text-blue-600 hover:underline">Review Tutor Applications</a>
          <a routerLink="/moderation/schools" class="text-sm text-blue-600 hover:underline">Review School Applications</a>
        </div>
        <div class="flex gap-2 items-end">
          <label class="text-sm">Status</label>
          <select [(ngModel)]="status" name="status" class="border border-gray-300 rounded-md px-3 py-2">
            <option value="open">Open</option>
            <option value="reviewed">Reviewed</option>
            <option value="dismissed">Dismissed</option>
            <option value="action_taken">Action Taken</option>
          </select>
          <button (click)="loadReports()" class="bg-gray-800 text-white px-4 py-2 rounded-md">Refresh</button>
        </div>
        <table class="w-full text-sm" *ngIf="reports?.length">
          <thead><tr class="text-left text-gray-600">
            <th class="py-1">ID</th><th class="py-1">Reporter</th><th class="py-1">Target</th><th class="py-1">Reason</th><th class="py-1">Status</th><th class="py-1">Actions</th></tr></thead>
          <tbody>
            <tr *ngFor="let r of reports" class="border-t border-gray-100">
              <td class="py-1">{{ r.id }}</td>
              <td class="py-1">{{ r.reporterUserId }}</td>
              <td class="py-1"><strong>{{ r.targetType }}</strong>: {{ r.targetId }}</td>
              <td class="py-1">{{ r.reason }}</td>
              <td class="py-1">{{ r.status }}</td>
              <td class="py-1 flex gap-2">
                <button (click)="resolve(r.id,'reviewed')" class="text-blue-700 hover:underline">Mark Reviewed</button>
                <button (click)="resolve(r.id,'dismissed')" class="text-gray-700 hover:underline">Dismiss</button>
                <button (click)="resolve(r.id,'action_taken')" class="text-green-700 hover:underline">Action Taken</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  `
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
