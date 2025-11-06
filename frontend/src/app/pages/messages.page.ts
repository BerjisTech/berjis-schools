import { Component, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { RouterModule } from '@angular/router';
import { SchoolsService } from '../services/schools.service';

@Component({
  selector: 'app-messages',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterModule],
  templateUrl: './messages.page.html'
})
export class MessagesPage implements OnInit {
  threads = signal<any[]>([]);
  selectedId = signal<string>('');
  messages = signal<any[]>([]);
  compose = signal('');
  newClassId = signal('');
  newUserId = signal('');

  constructor(private svc: SchoolsService) {}

  async ngOnInit() { await this.reload(); }

  async reload() {
    this.threads.set(await this.svc.listMessageThreads());
    if (this.threads().length && !this.selectedId()) {
      await this.open(this.threads()[0].id);
    }
  }

  async open(id: string) {
    this.selectedId.set(id);
    this.messages.set(await this.svc.getThreadMessages(id));
  }

  async startCall() {
    const id = this.selectedId();
    if (!id) return;
    const resp = await this.svc.startThreadCall(id, 'video');
    if (resp?.url) window.open(resp.url, '_blank');
  }

  async send() {
    const id = this.selectedId();
    const body = this.compose().trim();
    if (!id || !body) return;
    await this.svc.sendThreadMessage(id, body);
    this.compose.set('');
    await this.open(id);
  }

  async start() {
    if (!this.newClassId() || !this.newUserId()) return;
    const tid = await this.svc.startMessageThread(this.newUserId(), this.newClassId());
    if (tid) {
      await this.reload();
      await this.open(tid);
    }
  }
}
