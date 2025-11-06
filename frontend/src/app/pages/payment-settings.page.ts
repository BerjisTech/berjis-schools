import { Component, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { SchoolsService } from '../services/schools.service';

@Component({
  selector: 'app-payment-settings',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './payment-settings.page.html'
})
export class PaymentSettingsPage implements OnInit {
  gateway = (localStorage.getItem('payments.gateway') as 'stripe'|'flutterwave'|'mpesa') || 'stripe';
  phone = localStorage.getItem('payments.mpesaPhone') || '';
  coupon = localStorage.getItem('payments.coupon') || '';
  schools: Array<{ id: string; name: string }> = [];
  selectedSchool = signal<string>('');
  plans: Array<{ id: string; key: string; name: string; price: number; currency: string; interval: string }> = [];
  currentSub: { status?: string; plan?: string; periodEnd?: string } | null = null;
  busy = false;
  constructor(private svc: SchoolsService) {}
  async ngOnInit() {
    this.schools = await this.svc.listAdminSchools();
    this.plans = await this.svc.listPlans();
    if (this.schools.length) {
      this.selectedSchool.set(this.schools[0].id);
      await this.reloadSub();
    }
  }
  save() {
    localStorage.setItem('payments.gateway', this.gateway);
    localStorage.setItem('payments.mpesaPhone', (this.phone || '').trim());
    localStorage.setItem('payments.coupon', (this.coupon || '').trim());
    alert('Saved');
  }
  async reloadSub() {
    const sid = this.selectedSchool(); if (!sid) return;
    this.currentSub = await this.svc.getSchoolSubscription(sid);
  }
  async subscribe(planKey: string) {
    const sid = this.selectedSchool(); if (!sid) return;
    this.busy = true;
    try {
      const resp = await this.svc.subscribeSchool(sid, planKey, { successUrl: window.location.origin + '/settings/payments' });
      if (resp?.url) window.location.href = resp.url;
    } finally { this.busy = false; }
  }
}
