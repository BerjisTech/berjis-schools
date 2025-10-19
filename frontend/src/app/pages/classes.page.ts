import { Component } from '@angular/core';
import { CommonModule } from '@angular/common';

@Component({
  selector: 'app-classes-page',
  standalone: true,
  imports: [CommonModule],
  template: `
    <div class="p-4">
      <h2 class="mt-0 text-lg font-semibold">Classes</h2>
      <p>Coming soon: list and manage classes.</p>
    </div>
  `
})
export class ClassesPage {}
