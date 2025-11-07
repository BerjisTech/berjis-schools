import { Component, Input, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { SchoolsService } from '../../services/schools.service';
import { urlFor } from '../../util';

@Component({
  selector: 'app-lesson-captions',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './lesson-captions.component.html'
})
export class LessonCaptionsComponent implements OnInit {
  @Input() lessonId!: string;
  @Input() lesson: any;
  @Input() canManage = false;

  captionsUrl = '';
  transcript = '';
  saving = signal(false);
  toast = signal<string | null>(null);
  private apiBase = urlFor('schools-api');

  constructor(private svc: SchoolsService) {}

  ngOnInit(): void {
    const content = this.lesson?.content || {};
    this.captionsUrl = content.captionsUrl || '';
    this.transcript = content.transcript || '';
  }

  async onFileChange(ev: Event) {
    const input = ev.target as HTMLInputElement;
    const file = input?.files?.[0];
    if (!file) return;
    try {
      const url = await this.uploadDocument(file);
      this.captionsUrl = url;
      this.toast.set('Uploaded');
      setTimeout(() => this.toast.set(null), 1200);
    } catch (e: any) {
      this.toast.set(e?.message || 'Upload failed');
      setTimeout(() => this.toast.set(null), 1800);
    } finally { if (input) input.value = ''; }
  }

  clearCaptions() { this.captionsUrl = ''; }

  resolve(url: string): string { return /^https?:\/\//i.test(url) ? url : `${this.apiBase}${url.startsWith('/')?url:`/${url}`}`; }

  async save() {
    if (!this.canManage) return;
    this.saving.set(true);
    try {
      const content = { ...(this.lesson?.content || {}) };
      if (this.captionsUrl) content.captionsUrl = this.captionsUrl; else delete content.captionsUrl;
      if ((this.transcript || '').trim()) content.transcript = this.transcript.trim(); else delete content.transcript;
      await this.svc.updateLesson(this.lessonId, { content });
      this.toast.set('Saved');
      setTimeout(() => this.toast.set(null), 1200);
    } finally { this.saving.set(false); }
  }

  private async uploadDocument(file: File): Promise<string> {
    const form = new FormData();
    form.append('file', file);
    const res = await fetch(`${this.apiBase}/v1/uploads`, { method: 'POST', credentials: 'include', body: form });
    const j = await res.json();
    if (!j?.success) throw new Error(j?.message || 'Upload failed');
    const url = j?.data?.url;
    if (!url) throw new Error('Upload failed: missing URL');
    return url;
  }
}
