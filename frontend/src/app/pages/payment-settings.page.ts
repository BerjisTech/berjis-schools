import { Component } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';

@Component({
  selector: 'app-payment-settings',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './payment-settings.page.html'
})
export class PaymentSettingsPage {
  gateway = (localStorage.getItem('payments.gateway') as 'stripe'|'flutterwave'|'mpesa') || 'stripe';
  phone = localStorage.getItem('payments.mpesaPhone') || '';
  coupon = localStorage.getItem('payments.coupon') || '';
  save() {
    localStorage.setItem('payments.gateway', this.gateway);
    localStorage.setItem('payments.mpesaPhone', (this.phone || '').trim());
    localStorage.setItem('payments.coupon', (this.coupon || '').trim());
    alert('Saved');
  }
}

