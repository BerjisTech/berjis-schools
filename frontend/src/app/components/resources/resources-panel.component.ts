import { Component, Input, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { SchoolsService } from '../../services/schools.service';

type Scope = 'lesson'|'group'|'class'|'school'|'user';
type PrincipalType = 'user'|'class'|'group'|'school';
type Role = 'owner'|'editor'|'commenter'|'viewer';

@Component({
  standalone: true,
  selector: 'app-resources-panel',
  imports: [CommonModule, FormsModule],
  templateUrl: './resources-panel.component.html'
})
export class ResourcesPanelComponent implements OnInit {
  @Input() scope!: Scope;
  @Input() scopeId!: string;
  @Input() canManage = false;
  @Input() quickPrincipal?: { type: PrincipalType; id: string; label: string };

  resources = signal<Array<{ id: string; type: 'docs'|'sheets'|'notes'|'pdf'|'slides'; url: string; title?: string }>>([]);
  // Attach
  attachMode = signal<'create'|'link'>('create');
  attachType = signal<'docs'|'sheets'|'notes'|'pdf'|'slides'>('docs');
  attachTitle = signal('');
  attachURL = signal('');
  // Share
  shareOpen = signal(false);
  shareForResource = signal<string | null>(null);
  shareRole = signal<Role>('viewer');
  shareQuery = signal('');
  shareUserId = signal('');
  shareRows = signal<Array<{ id: string; principalType: PrincipalType; principalId: string; role: Role; createdBy?: string; createdAt?: string }>>([]);
  shareSuggestions = signal<Array<{ userId: string; displayName?: string }>>([]);
  shareSearching = signal(false);
  toast = signal<string | null>(null);
  private shareDebounce: ReturnType<typeof setTimeout> | null = null;

  constructor(private svc: SchoolsService) {}

  async ngOnInit() {
    await this.reload();
  }

  async reload() {
    try { this.resources.set(await this.svc.listResources(this.scope, this.scopeId)); } catch { this.resources.set([]); }
  }

  async attach() {
    if (!this.canManage) return;
    const mode = this.attachMode(); const type = this.attachType();
    const title = this.attachTitle().trim(); const url = this.attachURL().trim();
    const row = await this.svc.attachResource({ mode, type, title: title||undefined, url: mode==='link'?url:undefined, scope: this.scope, scopeId: this.scopeId });
    if (row) {
      this.resources.set([ ...this.resources(), { id: row.id, type: row.type as 'docs'|'sheets'|'notes'|'pdf'|'slides', url: row.url, title: row.title } ]);
      this.attachTitle.set(''); this.attachURL.set('');
    }
  }

  openResource(id: string){ this.svc.openResource(id); }

  // Share modal
  openShare(resourceId: string){ this.shareForResource.set(resourceId); this.shareOpen.set(true); this.loadAcl(resourceId); }
  private async loadAcl(resourceId: string){ try { this.shareRows.set(await this.svc.listResourceAcl(resourceId)); } catch { this.shareRows.set([]); } }
  async addUserShare(){
    const rid=this.shareForResource(); const uid=this.shareUserId().trim(); if(!rid||!uid) return; const role=this.shareRole();
    if (await this.svc.grantResourceRole(rid, 'user', uid, role)) { this.toast.set('Access granted'); setTimeout(()=>this.toast.set(null), 1200); }
    this.shareUserId.set(''); await this.loadAcl(rid);
  }
  async revokeShare(principalType: PrincipalType, principalId: string){
    const rid=this.shareForResource(); if(!rid) return;
    if (await this.svc.revokeResourceRole(rid, principalType, principalId)) { this.toast.set('Access revoked'); setTimeout(()=>this.toast.set(null), 1200); }
    await this.loadAcl(rid);
  }
  async onShareQueryChange(q: string){
    this.shareQuery.set(q);
    const query = (q||'').trim();
    if (this.shareDebounce) { clearTimeout(this.shareDebounce); this.shareDebounce = null; }
    if (query.length < 2) { this.shareSuggestions.set([]); this.shareSearching.set(false); return; }
    this.shareSearching.set(true);
    this.shareDebounce = setTimeout(async () => {
      try { this.shareSuggestions.set(await this.svc.searchUsers(query)); } finally { this.shareSearching.set(false); }
    }, 250);
  }
  pickSuggestion(u: { userId: string; displayName?: string }){ this.shareUserId.set(u.userId); this.shareQuery.set(u.displayName || u.userId); this.shareSuggestions.set([]); }
}
