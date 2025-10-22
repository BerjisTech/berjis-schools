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
}
