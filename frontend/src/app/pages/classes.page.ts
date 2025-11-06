import { Component, OnInit } from '@angular/core';
import { CommonModule } from '@angular/common';
import { RouterLink } from '@angular/router';
import { SchoolsService } from '../services/schools.service';
import { Course } from '../interfaces/course';

@Component({
  selector: 'app-classes-page',
  standalone: true,
  imports: [CommonModule, RouterLink],
  templateUrl: './classes.page.html'
})
export class ClassesPage implements OnInit {
  loading = true;
  courses: Course[] = [];

  constructor(private svc: SchoolsService) {}
  async ngOnInit() {
    this.loading = true;
    try { this.courses = await this.svc.listCourses(); } finally { this.loading = false }
  }

  async buy(c: Course) {
    try {
      const gw = (localStorage.getItem('payments.gateway') as any) || 'stripe';
      const phone = localStorage.getItem('payments.mpesaPhone') || undefined;
      const coupon = localStorage.getItem('payments.coupon') || undefined;
      const result = await this.svc.checkout(`${c.id}`, { gateway: gw, mode: 'hosted', currency: 'USD', successUrl: window.location.origin + `/course/${c.id}`, phone });
      if (result?.url) {
        window.location.href = result.url;
      }
    } catch {}
  }

  onCouponChange(e: any) {
    const val = String(e?.target?.value || '').trim();
    if (val) localStorage.setItem('payments.coupon', val); else localStorage.removeItem('payments.coupon');
  }
}
