import { Component } from '@angular/core';
import { ActivatedRoute } from '@angular/router';
import { FormsModule } from '@angular/forms';
import { AiService } from '../services/ai.service';

@Component({
  standalone: true,
  selector: 'app-study-tools',
  imports: [FormsModule],
  templateUrl: './study-tools.page.html'
})
export class StudyToolsPage {
  text = '';
  level = 'general';
  result = '';
  classId = '';
  insights: any = null;
  suggestions = '';
  loading = false;
  subject = '';
  question = '';
  helpText = '';
  constructor(private ai: AiService, private route: ActivatedRoute) {
    const cid = this.route.snapshot.queryParamMap.get('classId');
    if (cid) {
      this.classId = cid;
      // optionally auto-load insights for convenience
      this.loadInsights();
    }
  }
  async generatePack() {
    this.loading = true;
    try {
      const r = await this.ai.studyMaterials(this.text, this.level);
      this.result = String(r?.data ?? r?.message ?? '');
    } finally { this.loading = false; }
  }
  async loadInsights() {
    if (!this.classId) return;
    this.loading = true;
    try {
      const api = (window as any).urlFor ? (window as any).urlFor('schools-api') : '';
      const res = await fetch(`${api}/v1/classes/${encodeURIComponent(this.classId)}/insights`, { credentials: 'include' });
      const j = await res.json();
      this.insights = j?.data || null;
    } finally { this.loading = false; }
  }
  async suggest() {
    if (!this.classId) return;
    this.loading = true;
    try {
      const api = (window as any).urlFor ? (window as any).urlFor('schools-api') : '';
      const res = await fetch(`${api}/v1/classes/${encodeURIComponent(this.classId)}/insights/suggest`, { method: 'POST', credentials: 'include' });
      const j = await res.json();
      this.suggestions = String(j?.data ?? j?.message ?? '');
    } finally { this.loading = false; }
  }
  async help() {
    if (!this.question) return;
    this.loading = true;
    try {
      const api = (window as any).urlFor ? (window as any).urlFor('schools-api') : '';
      const res = await fetch(`${api}/v1/ai/homework-help`, { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ question: this.question, subject: this.subject }) });
      const j = await res.json();
      this.helpText = String(j?.data ?? j?.message ?? '');
    } finally { this.loading = false; }
  }
}
