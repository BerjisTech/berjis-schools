import { Component } from '@angular/core';
import { CommonModule } from '@angular/common';

@Component({
  selector: 'app-subjects-page',
  standalone: true,
  imports: [CommonModule],
  template: `
    <div class="p-4">
      <h2 class="mt-0 text-lg font-semibold">Subjects</h2>
      <p>Coming soon: browse subjects within your classes.</p>
    </div>
  `
})
export class SubjectsPage {}
