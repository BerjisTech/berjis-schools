import { Component } from '@angular/core';
import { CommonModule } from '@angular/common';

@Component({
  selector: 'app-tests-page',
  standalone: true,
  imports: [CommonModule],
  template: `
    <div class="p-4">
      <h2 class="mt-0 text-lg font-semibold">Tests</h2>
      <p>Coming soon: take tests and view results.</p>
    </div>
  `
})
export class TestsPage {}
