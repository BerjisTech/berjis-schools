import { Component, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { RouterModule, ActivatedRoute } from '@angular/router';
import { SchoolsService } from '../services/schools.service';

@Component({
  selector: 'app-class-appointments',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterModule],
  templateUrl: './class-appointments.page.html'
})
export class ClassAppointmentsPage implements OnInit {
  classId = '';
  slots = signal<any[]>([]);
  startAt = signal('');
  endAt = signal('');
  capacity = signal<number | null>(1);
  locationUrl = signal('');
  notes = signal('');
  myBookings = signal<any[]>([]);

  constructor(private svc: SchoolsService, private route: ActivatedRoute) {}

  async ngOnInit() {
    this.classId = this.route.snapshot.paramMap.get('id') || '';
    await this.reload();
    this.myBookings.set(await this.svc.listMyAppointments());
  }

  async reload() {
    this.slots.set(await this.svc.listAppointmentSlots(this.classId));
  }

  async createSlot() {
    const input: any = { startAt: this.startAt(), endAt: this.endAt(), capacity: this.capacity(), locationUrl: this.locationUrl() || undefined, notes: this.notes() || undefined };
    const ok = await this.svc.createAppointmentSlot(this.classId, input);
    if (ok) { this.startAt.set(''); this.endAt.set(''); this.capacity.set(1); this.locationUrl.set(''); this.notes.set(''); await this.reload(); }
  }

  async book(slotId: string) {
    const ok = await this.svc.bookAppointmentSlot(slotId);
    if (ok) { this.myBookings.set(await this.svc.listMyAppointments()); }
  }
}
