import { Component, OnInit, signal } from '@angular/core';
import { CommonModule } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { Router, RouterLink, RouterModule } from '@angular/router';
import { loginUrl, verifySession } from './util';

@Component({
  selector: 'app-root',
  standalone: true,
  imports: [CommonModule, FormsModule, RouterModule, RouterLink],
  templateUrl: './app.component.html'
})
export class AppComponent implements OnInit {
  authed = signal(false);
  showSidebar = signal(true);
  showAccount = signal(false);
  search = signal('');
  loginHref = loginUrl();

  constructor(private router: Router) {}

  async ngOnInit() {
    try {
      const result = await verifySession({ attemptRefresh: true });
      this.authed.set(!!result.valid);
    } catch {
      this.authed.set(false);
    }
  }

  toggleAccount() { this.showAccount.update(x => !x) }
  toggleSidebar() { this.showSidebar.update(x => !x) }
  onSearchSubmit(ev: Event) {
    ev.preventDefault();
    const q = this.search().trim();
    this.router.navigate(['/search'], { queryParams: { q } });
  }
}
