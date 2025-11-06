import { Component, OnInit } from '@angular/core';
import { CommonModule, DatePipe, CurrencyPipe } from '@angular/common';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { urlFor } from '../../app/util';

@Component({
  selector: 'app-receipt',
  standalone: true,
  imports: [CommonModule, RouterLink, DatePipe, CurrencyPipe],
  templateUrl: './receipt.page.html'
})
export class ReceiptPage implements OnInit {
  api = urlFor('schools-api');
  loading = true;
  id = '';
  data: any = null;
  constructor(private route: ActivatedRoute) {}
  async ngOnInit() {
    this.id = this.route.snapshot.paramMap.get('id') || '';
    this.loading = true;
    try {
      const res = await fetch(`${this.api}/v1/orders/${this.id}`, { credentials: 'include' });
      const j = await res.json();
      this.data = j?.data || null;
    } finally { this.loading = false; }
  }
}

