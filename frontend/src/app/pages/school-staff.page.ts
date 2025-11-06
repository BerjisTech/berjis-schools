import { Component, OnInit, computed, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { SchoolsService } from '../services/schools.service';
import { SchoolInvite, SchoolOverview, SchoolSummary } from '../interfaces/school';
import { UserRef } from '../interfaces/course';

@Component({
  selector: 'app-school-staff-page',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './school-staff.page.html'
})
export class SchoolStaffPage implements OnInit {
  schools = signal<SchoolSummary[]>([]);
  schoolId = signal('');
  overview = signal<SchoolOverview | null>(null);
  invites = signal<SchoolInvite[]>([]);
  members = signal<Array<{ userId: string; role: string; status: string }>>([]);

  loadingOverview = signal(false);
  loadingInvites = signal(false);
  loadingMembers = signal(false);
  searchingUsers = signal(false);

  searchQuery = signal('');
  searchResults = signal<UserRef[]>([]);
  addRole = signal<'tutor'|'admin'>('tutor');

  inviteEmail = signal('');
  inviteRole = signal<'tutor'|'admin'>('tutor');
  inviteMessage = signal('');
  inviteSubmitting = signal(false);

  toast = signal<{ kind: 'success'|'error'; text: string }|null>(null);

  readonly selectedSchool = computed(() => this.schools().find(s => s.id === this.schoolId()) ?? null);

  constructor(private svc: SchoolsService) {}

  async ngOnInit() {
    await this.loadAdminSchools();
  }

  async loadAdminSchools() {
    try {
      const list = await this.svc.listAdminSchools();
      this.schools.set(list);
      if (!this.schoolId() && list.length) {
        this.schoolId.set(list[0].id);
      }
      if (this.schoolId()) {
        await Promise.all([this.loadOverview(), this.loadMembers(), this.loadInvites()]);
      }
    } catch (e: unknown) {
      const msg = e && typeof e === 'object' && 'message' in e ? String((e as { message?: unknown }).message) : 'Failed to load schools';
      this.toast.set({ kind: 'error', text: msg });
    }
  }

  async selectSchool(id: string) {
    if (this.schoolId() === id) return;
    this.schoolId.set(id);
    this.overview.set(null);
    this.searchResults.set([]);
    await Promise.all([this.loadOverview(), this.loadMembers(), this.loadInvites()]);
  }

  async loadOverview() {
    if (!this.schoolId()) return;
    this.loadingOverview.set(true);
    try {
      const data = await this.svc.getSchoolOverview(this.schoolId());
      this.overview.set(data);
    } catch (e: unknown) {
      const msg = e && typeof e === 'object' && 'message' in e ? String((e as { message?: unknown }).message) : 'Failed to load overview';
      this.toast.set({ kind: 'error', text: msg });
    } finally {
      this.loadingOverview.set(false);
    }
  }

  async loadMembers() {
    if (!this.schoolId()) return;
    this.loadingMembers.set(true);
    try {
      const data = await this.svc.getSchoolMembers(this.schoolId());
      this.members.set(data);
    } catch (e: any) {
      this.toast.set({ kind: 'error', text: e?.message || 'Failed to load members' });
      this.members.set([]);
    } finally {
      this.loadingMembers.set(false);
    }
  }

  async loadInvites() {
    if (!this.schoolId()) return;
    this.loadingInvites.set(true);
    try {
      const rows = await this.svc.getSchoolInvites(this.schoolId());
      this.invites.set(rows);
    } catch (e: any) {
      this.toast.set({ kind: 'error', text: e?.message || 'Failed to load invites' });
      this.invites.set([]);
    } finally {
      this.loadingInvites.set(false);
    }
  }

  async searchUsers() {
    const q = this.searchQuery().trim();
    if (!q) { this.searchResults.set([]); return; }
    this.searchingUsers.set(true);
    try {
      const res = await this.svc.searchUsersGlobal(q);
      this.searchResults.set(res);
    } catch (e: any) {
      this.toast.set({ kind: 'error', text: e?.message || 'Search failed' });
      this.searchResults.set([]);
    } finally {
      this.searchingUsers.set(false);
    }
  }

  async addMember(user: UserRef) {
    if (!user?.id || !this.schoolId()) return;
    try {
      const ok = await this.svc.addSchoolMember(this.schoolId(), user.id, this.addRole());
      if (!ok) throw new Error('Unable to add member');
      this.toast.set({ kind: 'success', text: `Added ${user.name || user.id} as ${this.addRole()}` });
      this.searchResults.set([]);
      this.searchQuery.set('');
      await this.loadMembers();
    } catch (e: any) {
      this.toast.set({ kind: 'error', text: e?.message || 'Failed to add member' });
    }
  }

  async submitInvite() {
    if (!this.schoolId()) return;
    const email = this.inviteEmail().trim();
    if (!email) {
      this.toast.set({ kind: 'error', text: 'Enter an email to invite' });
      return;
    }
    this.inviteSubmitting.set(true);
    try {
      const created = await this.svc.createSchoolInvite(this.schoolId(), {
        email,
        role: this.inviteRole(),
        message: this.inviteMessage().trim() || undefined,
      });
      if (!created) throw new Error('Invite could not be created');
      this.toast.set({ kind: 'success', text: 'Invite sent' });
      this.inviteEmail.set('');
      this.inviteMessage.set('');
      await this.loadInvites();
    } catch (e: any) {
      this.toast.set({ kind: 'error', text: e?.message || 'Failed to send invite' });
    } finally {
      this.inviteSubmitting.set(false);
    }
  }

  dismissToast() { this.toast.set(null); }
}
