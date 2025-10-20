import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { urlFor } from '../../app/util';

@Component({
  selector: 'app-school-staff-page',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './school-staff.page.html'
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


