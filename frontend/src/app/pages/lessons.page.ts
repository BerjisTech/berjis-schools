import { Component } from '@angular/core';
import { CommonModule } from '@angular/common';

@Component({
  selector: 'app-lessons-page',
  standalone: true,
  imports: [CommonModule],
  template: `
    <div class="p-4">
      <h2 class="mt-0 text-lg font-semibold">Lessons</h2>
      <p>Coming soon: your in-progress and completed lessons.</p>
    </div>
  `
})
export class LessonsPage {}
