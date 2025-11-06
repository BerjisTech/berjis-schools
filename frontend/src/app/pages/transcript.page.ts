import { Component, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { RouterModule } from '@angular/router';
import { urlFor } from '../util';
import { JSONObject } from '../types/json';

@Component({
  selector: 'app-transcript',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterModule],
  templateUrl: './transcript.page.html'
})
export class TranscriptPage implements OnInit {
  api = urlFor('schools-api');
  loading = signal(false);
  name = signal('');
  overall = signal<number | null>(null);
  classes = signal<Array<{ title: string; percent?: number | null }>>([]);

  async ngOnInit() {
    await this.load();
  }

  async load() {
    this.loading.set(true);
    try {
      const res = await fetch(`${this.api}/v1/transcripts/me`, { credentials: 'include' });
      const j = await res.json();
      const d = j?.data || {};
      this.name.set(d.name || '');
      this.overall.set(d.overallPercent ?? null);
      const rows = Array.isArray(d.classes) ? d.classes as JSONObject[] : [];
      this.classes.set(rows.map((r) => ({ title: String(r.title ?? ''), percent: (r.percent as number | null | undefined) ?? null })));
    } finally {
      this.loading.set(false);
    }
  }

  openPdf() {
    window.open(`${this.api}/v1/transcripts/me.pdf`, '_blank');
  }
}
