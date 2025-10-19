import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { urlFor } from '../../app/util';

@Component({
  selector: 'app-school-staff-page',
  standalone: true,
  imports: [CommonModule, FormsModule],
  template: `
    <div class="p-5 max-w-3xl">
      <h2 class="mt-0 text-lg font-semibold">School Staff</h2>
      <div class="grid gap-4">
        <div class="border border-gray-200 rounded-xl p-4">
          <h3 class="mt-0 font-semibold">Select School</h3>
          <div class="flex gap-2 items-end">
            <div class="flex-1">
              <label class="block text-sm mb-1">Your Schools (Admin)</label>
              <select [(ngModel)]="schoolId" name="schoolId" (change)="loadMembers()" class="w-full border border-gray-300 rounded-md px-3 py-2">
                <option *ngFor="let s of myAdminSchools" [value]="s.id">{{ s.name }}</option>
              </select>
            </div>
            <button (click)="refreshSchools()" class="bg-gray-800 text-white px-4 py-2 rounded-md">Refresh</button>
          </div>
          <div class="mt-3" *ngIf="members?.length">
            <table class="w-full text-sm">
              <thead><tr class="text-left text-gray-600"><th class="py-1">User</th><th class="py-1">Role</th><th class="py-1">Status</th></tr></thead>
              <tbody>
                <tr *ngFor="let m of members" class="border-t border-gray-100">
                  <td class="py-1">{{ m.userId }}</td>
                  <td class="py-1">{{ m.role }}</td>
                  <td class="py-1">{{ m.status }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <div class="border border-gray-200 rounded-xl p-4">
          <h3 class="mt-0 font-semibold">Add Staff Member</h3>
          <form class="grid gap-3 max-w-xl" (ngSubmit)="addMember()">
            <div>
              <label class="block text-sm mb-1">User ID</label>
              <input [(ngModel)]="userId" name="userId" class="w-full border border-gray-300 rounded-md px-3 py-2" placeholder="core user id" required />
            </div>
            <div>
              <label class="block text-sm mb-1">Role</label>
              <select [(ngModel)]="role" name="role" class="w-full border border-gray-300 rounded-md px-3 py-2">
                <option value="tutor">Tutor</option>
                <option value="admin">Admin</option>
              </select>
            </div>
            <div class="flex items-center gap-3">
              <button type="submit" class="bg-blue-600 text-white px-4 py-2 rounded-md" [disabled]="saving">{{ saving ? 'Adding…' : 'Add Member' }}</button>
              <div *ngIf="message" class="text-sm" [class.text-green-700]="success" [class.text-red-700]="!success">{{ message }}</div>
            </div>
          </form>
        </div>
      </div>
    </div>
  `
})
export class SchoolStaffPage implements OnInit {
  schoolId = '';
  members: { userId: string; role: string; status: string }[] = [];
  userId = '';
  role: 'tutor'|'admin' = 'tutor';
  saving = false;
  message = '';
  success = false;
  myAdminSchools: { id: string; name: string }[] = [];

  async ngOnInit() { await this.refreshSchools() }

  async refreshSchools() {
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/schools/mine?role=admin`, { credentials: 'include' });
      const j = await res.json();
      this.myAdminSchools = (j?.data ?? []) as any[];
      if (!this.schoolId && this.myAdminSchools.length) {
        this.schoolId = this.myAdminSchools[0].id;
        await this.loadMembers();
      }
    } catch { this.myAdminSchools = [] }
  }

  async loadMembers() {
    this.message = '';
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/schools/${this.schoolId}/members`, { credentials: 'include' });
      const j = await res.json();
      this.members = j?.data ?? [];
    } catch { this.members = [] }
  }

  async addMember() {
    this.saving = true; this.message = ''; this.success = false;
    try {
      const res = await fetch(`${urlFor('schools-api')}/v1/schools/${this.schoolId}/members`, {
        method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ userId: this.userId, role: this.role })
      });
      const j = await res.json();
      if (!j?.success) throw new Error(j?.message || 'Failed');
      this.success = true; this.message = 'Member added';
      this.userId = '';
      await this.loadMembers();
    } catch (e: any) { this.message = e?.message || 'Failed to add member' }
    finally { this.saving = false }
  }
}
