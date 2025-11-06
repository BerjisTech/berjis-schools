import { Component, Input } from '@angular/core';
import { CommonModule } from '@angular/common';
import { urlFor } from '../../app/util';

@Component({
  selector: 'app-ai-panel',
  standalone: true,
  imports: [CommonModule],
  template: `
  <div class="fixed bottom-5 right-5 z-[99999]">
    <button (click)="open = !open" class="rounded-full bg-blue-600 text-white shadow-lg px-4 py-2">{{ open ? 'Close AI' : 'Ask AI' }}</button>
  </div>
  <div *ngIf="open" class="fixed bottom-16 right-5 z-[99999] w-[360px] h-[520px] bg-white border rounded-xl shadow-2xl overflow-hidden">
    <iframe [src]="iframeUrl" class="w-full h-full" title="AI Assistant" referrerpolicy="no-referrer"></iframe>
  </div>
  `
})
export class AiPanelComponent {
  @Input() context: string | null = null;
  open = false;
  get iframeUrl() {
    const base = urlFor('ai');
    const q = new URLSearchParams({ app: 'schools', context: this.context || window.location.pathname }).toString();
    return base + '/embed?' + q;
  }
}

