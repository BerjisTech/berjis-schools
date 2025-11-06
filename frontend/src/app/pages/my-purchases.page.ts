import { Component, OnInit } from '@angular/core';
import { CommonModule, DatePipe, CurrencyPipe } from '@angular/common';
import { RouterLink } from '@angular/router';
import { urlFor } from '../../app/util';

@Component({
  selector: 'app-my-purchases',
  standalone: true,
  imports: [CommonModule, RouterLink, DatePipe, CurrencyPipe],
  templateUrl: './my-purchases.page.html'
})
export class MyPurchasesPage implements OnInit {
  loading = true;
  rows: Array<any> = [];
  api = urlFor('schools-api');
  async ngOnInit() {
    this.loading = true;
    try {
      const res = await fetch(`${this.api}/v1/orders`, { credentials: 'include' });
      const j = await res.json();
      this.rows = j?.data || [];
    } finally { this.loading = false; }
  }
}

