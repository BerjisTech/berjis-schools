import { Component, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { SchoolsService } from '../services/schools.service';

@Component({
  selector: 'app-feedback-page',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './feedback.page.html'
})
export class FeedbackPage implements OnInit {
  tab = signal<'feedback'|'bug'>('feedback');
  // feedback
  fCategory: 'ux'|'feature'|'content'|'other' = 'ux';
  fMessage = '';
  fContext = '';
  fList: any[] = [];
  // bug
  bSeverity: 'low'|'medium'|'high'|'critical' = 'low';
  bTitle = '';
  bDetails = '';
  bUrl = '';
  bList: any[] = [];
  busy = false;

  constructor(private svc: SchoolsService) {}

  async ngOnInit() {
    await this.reload();
  }

  async reload() {
    this.fList = await this.svc.listMyFeedback();
    this.bList = await this.svc.listMyBugs();
  }

  async submitFeedback() {
    if (!this.fMessage.trim()) return;
    this.busy = true;
    try {
      const ok = await this.svc.submitFeedback({ category: this.fCategory, message: this.fMessage.trim(), context: this.fContext || undefined });
      if (ok) {
        this.fMessage = ''; this.fContext = ''; await this.reload();
      }
    } finally { this.busy = false; }
  }

  async reportBug() {
    if (!this.bTitle.trim()) return;
    this.busy = true;
    try {
      const ok = await this.svc.reportBug({ severity: this.bSeverity, title: this.bTitle.trim(), details: this.bDetails || undefined, url: this.bUrl || undefined });
      if (ok) {
        this.bTitle = ''; this.bDetails = ''; await this.reload();
      }
    } finally { this.busy = false; }
  }
}

