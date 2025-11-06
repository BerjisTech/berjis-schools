import { Component, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { RouterModule, ActivatedRoute } from '@angular/router';
import { SchoolsService } from '../services/schools.service';

@Component({
  selector: 'app-groups',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterModule],
  templateUrl: './groups.page.html'
})
export class GroupsPage implements OnInit {
  classId = '';
  groups = signal<any[]>([]);
  selectedId = signal<string>('');
  messages = signal<any[]>([]);
  members = signal<any[]>([]);
  newTitle = signal('');
  compose = signal('');

  constructor(private svc: SchoolsService, private route: ActivatedRoute) {}

  async ngOnInit() {
    this.classId = this.route.snapshot.paramMap.get('id') || '';
    await this.reload();
  }

  async reload() {
    this.groups.set(await this.svc.listStudyGroups(this.classId));
  }

  async create() {
    if (!this.newTitle().trim()) return;
    const id = await this.svc.createStudyGroup(this.classId, this.newTitle().trim());
    this.newTitle.set('');
    await this.reload();
    if (id) this.selectedId.set(id);
  }

  async open(id: string) {
    this.selectedId.set(id);
    this.messages.set(await this.svc.listGroupMessages(id));
    this.members.set(await this.svc.listGroupMembers(id));
  }

  async send() {
    const id = this.selectedId(); const body = this.compose().trim();
    if (!id || !body) return;
    await this.svc.sendGroupMessage(id, body);
    this.compose.set('');
    await this.open(id);
  }

  async startCall() {
    const id = this.selectedId(); if (!id) return;
    const r = await this.svc.startGroupCall(id);
    if (r?.url) window.open(r.url, '_blank');
  }

  async setRole(uid: string, role: 'member'|'moderator'|'owner') {
    const id = this.selectedId(); if (!id) return;
    await this.svc.setGroupMemberRole(id, uid, role);
    this.members.set(await this.svc.listGroupMembers(id));
  }
  async ban(uid: string) {
    const id = this.selectedId(); if (!id) return;
    await this.svc.banGroupMember(id, uid);
    this.members.set(await this.svc.listGroupMembers(id));
  }
  async unban(uid: string) {
    const id = this.selectedId(); if (!id) return;
    await this.svc.unbanGroupMember(id, uid);
    this.members.set(await this.svc.listGroupMembers(id));
  }
}
