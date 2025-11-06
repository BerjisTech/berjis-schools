import { Component } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { AiService } from '../../services/ai.service';

@Component({
  selector: 'app-ai-chat',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './ai-chat.component.html'
})
export class AiChatComponent {
  open = false;
  input = '';
  busy = false;
  messages: Array<{ role: 'user'|'assistant'; content: string }> = [];
  constructor(private ai: AiService) {}
  toggle() { this.open = !this.open; }
  async send() {
    const text = this.input.trim();
    if (!text) return;
    this.messages.push({ role: 'user', content: text });
    this.input = '';
    this.busy = true;
    try {
      const resp = await this.ai.chat([{ role: 'system', content: 'You are a helpful tutor inside a schools app.' }, ...this.messages]);
      const answer = resp?.data?.answer || resp?.answer || JSON.stringify(resp);
      this.messages.push({ role: 'assistant', content: String(answer) });
    } catch { /* ignore */ }
    this.busy = false;
  }
}

