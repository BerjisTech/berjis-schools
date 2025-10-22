import { Component } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { RouterLink } from '@angular/router';
import { SchoolsService } from '../services/schools.service';
import { Subject } from '../interfaces/course';

@Component({
  selector: 'app-create-course-page',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterLink],
  templateUrl: './create-course.page.html'
})
export class CreateCoursePage {
  title = '';
  description = '';
  isPaid = false;
  priceCents: number = 0;
  createdCourseId: string | null = null;
  creating = false;

  subjects: Subject[] = [];
  newSubjectTitle = '';

  constructor(private svc: SchoolsService) {}

  async createCourse() {
    if (!this.title.trim()) return;
    this.creating = true;
    try {
      const c = await this.svc.createCourse({ title: this.title.trim(), description: this.description.trim(), isPaid: this.isPaid, priceCents: this.priceCents });
      this.createdCourseId = `${c.id}`;
    } finally { this.creating = false }
  }

  async addSubject() {
    if (!this.createdCourseId || !this.newSubjectTitle.trim()) return;
    const s = await this.svc.createSubject({ classId: this.createdCourseId, title: this.newSubjectTitle.trim() });
    this.subjects.push(s);
    this.newSubjectTitle = '';
  }
}

