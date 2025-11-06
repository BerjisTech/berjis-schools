import { Component, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { RouterModule } from '@angular/router';
import { SchoolsService } from '../services/schools.service';

@Component({
  selector: 'app-verify-certificate',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterModule],
  templateUrl: './verify-certificate.page.html'
})
export class VerifyCertificatePage {
  code = signal('');
  loading = signal(false);
  result = signal<any | null>(null);
  error = signal<string | null>(null);

  constructor(private schools: SchoolsService) {}

  async verify() {
    const c = this.code().trim();
    if (!c) return;
    this.loading.set(true); this.error.set(null); this.result.set(null);
    try {
      const data = await this.schools.verifyCertificate(c);
      if (!data) {
        this.error.set('Certificate not found');
      } else {
        this.result.set(data);
      }
    } catch {
      this.error.set('Verification failed');
    } finally { this.loading.set(false); }
  }
}
